# kcp: Kubernetes-like Control Planes for Multi-Tenant APIs

## What is kcp

kcp is a Kubernetes API server that provides the Kubernetes API machinery — resource schemas, RBAC, admission, WATCH, declarative reconciliation — without any of the container orchestration. There are no Pods, no Deployments, no Services. It strips Kubernetes down to its API layer and rebuilds it as a **generic, multi-tenant control plane** for managing any kind of declarative resource.

The core insight: most of what makes Kubernetes powerful isn't container scheduling — it's the API model. The declarative YAML, the watch-based reconciliation, the schema validation, the RBAC, the eventual consistency model. These patterns work for any domain where you have declared desired state and controllers that drive toward it. kcp extracts that machinery and makes it available for arbitrary resource types at massive multi-tenant scale.

kcp serves the standard Kubernetes API. Any tool that speaks the Kubernetes API (kubectl, controller-runtime, client-go, Helm, ArgoCD, Crossplane) works with kcp. The difference is what's behind that API: not a cluster of nodes running containers, but a hierarchy of isolated **workspaces**, each with its own set of APIs, RBAC policies, and data.

## Core Concepts

### Workspaces

A workspace is the fundamental isolation boundary in kcp. It is analogous to a Kubernetes namespace, but much stronger: each workspace has its own API surface, its own RBAC, its own set of CRD-like resources, and complete data isolation. One workspace cannot see resources in another workspace.

Workspaces are hierarchical. A root workspace can contain child workspaces, which can contain their own children. This models organizational structures naturally: root → organization → team → project.

When a user navigates into a workspace, the API server URL changes to include the workspace path:

```
https://kcp-server:6443/clusters/root:team-frontend
```

From the user's perspective, it looks and feels like a dedicated Kubernetes API server.

### LogicalCluster

Behind every workspace is a **LogicalCluster** — the actual data container. It's an isolated slice of etcd with a unique key prefix (a random hash like `1mq3akk12u3u9tvs`). All resources in a workspace are stored under this prefix, providing complete data separation at the storage layer.

The workspace is a pointer object living in the parent workspace. The LogicalCluster is the storage it points to. They can live on different shards in a multi-shard deployment.

Every workspace has a hidden `cluster` object (its "dot file") that contains metadata about the LogicalCluster, including which shard hosts it.

### APIResourceSchema

kcp's equivalent of a CRD, with one critical difference: it's **immutable**. Once created, it cannot be modified. When you need to change the schema for a resource type, you create a new APIResourceSchema with a new name.

An APIResourceSchema defines the shape of a custom resource — its fields, validation rules, versions, and subresources. It follows the exact same OpenAPI v3 schema format as Kubernetes CRDs.

```yaml
apiVersion: apis.kcp.io/v1alpha1
kind: APIResourceSchema
metadata:
  name: v1.workflowdefinitions.conductor.io
spec:
  group: conductor.io
  names:
    kind: WorkflowDefinition
    plural: workflowdefinitions
    singular: workflowdefinition
  scope: Namespaced
  versions:
    - name: v1alpha1
      served: true
      storage: true
      schema:
        type: object
        properties:
          spec:
            type: object
            # ... field definitions
      subresources:
        status: {}
```

This defines the resource type but does not serve it. To make it available to users, it must be packaged in an APIExport.

### APIExport

An APIExport bundles one or more APIResourceSchemas into a package that can be consumed by other workspaces. It lives in a "provider" workspace and declares: "I am offering these APIs for others to use."

```yaml
apiVersion: apis.kcp.io/v1alpha2
kind: APIExport
metadata:
  name: order-fulfillment
spec:
  resources:
    - group: conductor.io
      name: workflowdefinitions
      schema: v1.workflowdefinitions.conductor.io
      storage:
        crd: {}
    - group: conductor.io
      name: workflowruns
      schema: v1.workflowruns.conductor.io
      storage:
        crd: {}
```

Each resource entry in the export references an APIResourceSchema and must match three things: the schema's `metadata.name`, its `spec.group`, and its `spec.names.plural`.

Creating an APIExport alone does nothing. It becomes active only when consumer workspaces create APIBindings to it.

### APIBinding

An APIBinding is the consumer-side pull. Created in a consumer workspace, it binds to a specific APIExport by path and name, making all the resources in that export available in the consumer's workspace.

```yaml
apiVersion: apis.kcp.io/v1alpha1
kind: APIBinding
metadata:
  name: bind-workflows
spec:
  reference:
    export:
      path: "root:workflow-admin"
      name: "order-fulfillment"
```

A single workspace can have multiple APIBindings, each pulling in APIs from different exports. Binding is all-or-nothing per export — you cannot cherry-pick individual resources from within an export.

A key architectural point: the APIExport provider defines the schema, but the consumer owns the data. When a user creates a WorkflowDefinition in their workspace, it's stored in their workspace's LogicalCluster, not in the provider's workspace. The provider never sees the actual instances unless they use a Virtual Workspace (see below).

### APIExportEndpointSlice

When an APIExport is created, kcp automatically generates an APIExportEndpointSlice. This object contains the Virtual Workspace URLs — one per shard — where controllers can connect to manage resources across all consumer workspaces.

```yaml
apiVersion: apis.kcp.io/v1alpha1
kind: APIExportEndpointSlice
metadata:
  name: order-fulfillment
endpoints:
  - url: https://kcp:6443/services/apiexport/root/order-fulfillment
```

Controllers watch this object to discover how to reach their resources. When new shards are added, new URLs appear automatically. When shards are removed, URLs disappear. This is the dynamic discovery mechanism that makes multi-shard deployments work without hardcoding.

### The Provider-Consumer Relationship

```
root:workflow-admin (provider workspace)
├── APIResourceSchema: WorkflowDefinition
├── APIResourceSchema: WorkflowRun
├── APIExport: order-fulfillment
│   └── APIExportEndpointSlice (auto-created, one URL per shard)
│
├── root:team-marketplace (consumer workspace)
│   ├── APIBinding → order-fulfillment
│   ├── WorkflowDefinition/order-fulfillment  (user's data, stored here)
│   └── WorkflowRun/order-12345              (user's data, stored here)
│
└── root:team-payments (consumer workspace)
    ├── APIBinding → order-fulfillment
    └── WorkflowDefinition/payment-retry     (different tenant, isolated data)
```

## Architecture

### Front-Proxy

The front-proxy is the single entry point for all clients. It's a stateless reverse proxy that routes requests by resolving workspace paths to their hosting shard.

When a client sends a request to `https://kcp-server/clusters/root:team-frontend/api/v1/configmaps`, the front-proxy resolves `root:team-frontend` to the LogicalCluster hash, determines which shard hosts it, and forwards the request there.

The front-proxy has no database. It builds an in-memory routing table using Kubernetes informers (LIST then WATCH on Shard and Workspace objects). On restart, it rebuilds from scratch in seconds. Multiple replicas can run for high availability.

### Shards

A shard is an independent kcp API server instance with its own etcd. It hosts a subset of workspaces. Shards never communicate directly with each other — all cross-shard communication goes through the front-proxy or cache server.

The root shard is special: it hosts the `root` logical cluster, which contains administrative objects like Shard definitions, WorkspaceTypes, and system APIExports.

Each shard runs its own set of controllers for workspace lifecycle management, API binding, and other internal operations.

In a single-shard deployment (the most common starting point), the root shard handles everything. Sharding becomes necessary when etcd is saturated, when you need multi-region or compliance boundaries, or when you want blast radius isolation.

### Virtual Workspaces

Virtual Workspaces (VW) are proxy-like API servers that provide **filtered, cross-workspace views** of resources. They are per-shard, not global.

The most important VW for API providers is the **apiexport VW**. When a controller connects to the APIExport's VW URL, it sees all instances of its resources across every consumer workspace on that shard — merged into a single API endpoint. A WATCH on this endpoint fires for creates, updates, and deletes in any consumer workspace.

This is how a single controller instance can manage resources across thousands of tenant workspaces without needing individual connections to each one.

The VW URL format is:
```
https://<shard>:6443/services/apiexport/<shard-name>/<export-name>
```

Another important VW is `initializingworkspaces`, which allows workspace initialization controllers to access workspaces that are still in the `Initializing` phase (normally, regular access to initializing workspaces is blocked).

### Cache Server

The cache server provides a central CRUD API for cross-shard data sharing. Write controllers on each shard push data to it; read controllers on other shards consume it. This avoids direct shard-to-shard connections, which would create n*(n-1) communication paths.

In a single-shard deployment, the cache server typically runs embedded within the shard. In multi-shard deployments, it runs as a standalone component.

The cache server is used for system-level data like Shard objects and cached resource endpoint slices — not for user data.

### CachedResourceEndpointSlice

Similar to APIExportEndpointSlice but for internal kcp controllers. It tracks where cached/replicated system resources are served per shard. kcp's internal controllers use these endpoints; API providers generally don't interact with them directly.

## Workspace Types and Initialization

### WorkspaceType

A WorkspaceType defines a template for workspaces — what APIs they get, what children they can have, where they can be created, and whether they require initialization before becoming ready.

```yaml
apiVersion: tenancy.kcp.io/v1alpha1
kind: WorkspaceType
metadata:
  name: workflow
spec:
  initializer: true
  limitAllowedChildren:
    none: true
  limitAllowedParents:
    types:
      - name: root
        path: root
```

`initializer: true` blocks the workspace in an `Initializing` phase until an init controller removes the initializer. `limitAllowedChildren.none: true` prevents users from creating child workspaces inside it. `limitAllowedParents` restricts where this workspace type can be used.

### Workspace Initialization with init-agent

The [init-agent](https://github.com/kcp-dev/init-agent) is a Kubernetes controller that watches for newly created workspaces of a specific type and bootstraps them with predefined resources — APIBindings, RBAC, ConfigMaps, or anything else.

The initialization flow:

1. Admin creates a WorkspaceType with `initializer: true`
2. Admin creates `InitTemplate` objects containing Go-templated YAML manifests
3. Admin creates an `InitTarget` connecting the WorkspaceType to the templates
4. When a user creates a workspace of that type, kcp blocks it in `Initializing` phase
5. The init-agent detects it via the `initializingworkspaces` VW
6. It renders the templates and applies the resources into the new workspace
7. It removes the initializer from the LogicalCluster
8. The workspace transitions to `Ready`

```yaml
apiVersion: initialization.kcp.io/v1alpha1
kind: InitTemplate
metadata:
  name: workflow-ws-init-template
spec:
  template: |
    apiVersion: apis.kcp.io/v1alpha1
    kind: APIBinding
    metadata:
      name: bind-workflows
    spec:
      reference:
        export:
          path: "root:workflow-admin"
          name: "order-fulfillment"
```

Templates support Go template syntax with sprig functions. Context variables `{{ .ClusterName }}` and `{{ .ClusterPath }}` are available for dynamic content.

The `InitTarget` connects the template to the workspace type:

```yaml
apiVersion: initialization.kcp.io/v1alpha1
kind: InitTarget
metadata:
  name: workflow-ws-init-target
spec:
  workspaceTypeRef:
    name: workflow
    path: root
  sources:
    - template:
        name: workflow-ws-init-template
```

The init-agent is installed via Helm in three parts: the agent pod on the hosting cluster, CRDs and RBAC in the config workspace, and RBAC in the workspace where the WorkspaceType lives.

## Sharding in Detail

### When to Add Shards

A single shard can host tens of thousands of workspaces. Sharding becomes necessary for:

- **etcd saturation**: When a single etcd cluster can't handle the write volume
- **Multi-region / compliance**: When data must reside in specific geographic regions
- **Blast radius isolation**: When you want failures in one set of workspaces to not affect others

### How a New Shard Joins

1. Admin creates a `Shard` object in the root workspace
2. Root shard stores it, cache server picks it up
3. All existing shards update their scheduling rings
4. APIExportEndpointSlice and CachedResourceEndpointSlice controllers detect the new shard
5. New VW URLs are added to the endpoint slices (one per shard)
6. Controllers watching the endpoint slices spin up new informers for the new shard's VW
7. The system is now aware of the new shard — new workspaces can be scheduled to it

### Workspace Scheduling

When a user creates a workspace, the scheduler on the hosting shard generates a random cluster name, hashes it against the known shard list from the cache server, creates a LogicalCluster on the target shard, watches it during initialization, and updates the Workspace status when ready.

In a multi-shard deployment, the workspace and its LogicalCluster can live on different shards. The front-proxy handles routing regardless of which shard hosts the data.

## Writing Controllers for kcp

### The multicluster-provider

The recommended approach for writing kcp-aware controllers is using [kcp-dev/multicluster-provider](https://github.com/kcp-dev/multicluster-provider) with [multicluster-runtime](https://github.com/kubernetes-sigs/multicluster-runtime). This is a controller-runtime addon that provides multi-cluster awareness without forking controller-runtime.

The `apiexport` provider handles VW URL discovery from APIExportEndpointSlice, automatic per-shard connection management, per-workspace client scoping, and proper lifecycle management as shards come and go.

A kcp controller connects to the provider workspace (where the APIExport lives) via a kubeconfig with a workspace-scoped server URL:

```
server: "https://kcp:6443/clusters/root:workflow-admin"
```

The provider reads the APIExportEndpointSlice, discovers VW URLs, and opens WATCH connections. When resources are created in any consumer workspace, the reconciler is called with a `mcreconcile.Request` that includes `.Cluster()` — the LogicalCluster hash identifying which workspace the event came from.

```go
func reconcileWorkflow(
    ctx context.Context,
    mgr mcmanager.Manager,
    req mcreconcile.Request,
) (reconcile.Result, error) {
    // Get a client scoped to the specific workspace
    cl, err := mgr.GetCluster(ctx, req.Cluster())

    // Fetch the resource from that workspace
    obj := &unstructured.Unstructured{}
    obj.SetGroupVersionKind(workflowDefGVK)
    err = cl.GetClient().Get(ctx, req.NamespacedName, obj)

    // Your business logic here
    return reconcile.Result{}, nil
}
```

### Scheme Registration

Controllers must register kcp API types in the runtime scheme. The types must come from `github.com/kcp-dev/sdk` (the standalone module), not from `github.com/kcp-dev/kcp/sdk` (the nested module within the kcp monorepo). The multicluster-provider depends on the standalone module — using the wrong one causes type mismatches where the scheme registration appears to work but the provider can't find the types.

```go
import (
    apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
    corev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

var scheme = runtime.NewScheme()

func init() {
    utilruntime.Must(clientgoscheme.AddToScheme(scheme))
    utilruntime.Must(apisv1alpha1.AddToScheme(scheme))
    utilruntime.Must(corev1alpha1.AddToScheme(scheme))
}
```

### Unstructured vs Typed Resources

Since resources defined via APIResourceSchema don't have corresponding Go types, controllers commonly use `unstructured.Unstructured` with the GVK set manually. This avoids code generation and works for any resource shape:

```go
var workflowDefGVK = schema.GroupVersionKind{
    Group: "conductor.io", Version: "v1alpha1", Kind: "WorkflowDefinition",
}

obj := &unstructured.Unstructured{}
obj.SetGroupVersionKind(workflowDefGVK)
```

Fields are read with `unstructured.NestedString()`, `unstructured.NestedMap()`, etc.

### The API Sync Agent

For cases where a real Kubernetes cluster has CRDs with controllers (cert-manager, Crossplane, database operators), the [api-syncagent](https://github.com/kcp-dev/api-syncagent) bridges those CRDs into kcp. It syncs objects bidirectionally: user creates a `Certificate` in kcp → agent syncs it to the real cluster → cert-manager processes it → agent syncs the resulting `Secret` back to kcp. This is distinct from the multicluster-provider pattern where kcp is the sole data store and the controller calls external APIs.

## Built-in APIs

kcp provides a subset of the standard Kubernetes API — only the parts that make sense without container orchestration:

- ConfigMaps, Secrets
- Namespaces
- ServiceAccounts
- RBAC (Roles, ClusterRoles, RoleBindings, ClusterRoleBindings)
- CRDs and APIResourceSchemas
- Admission webhooks

Notably absent: Pods, Deployments, Services, Ingress, StatefulSets, DaemonSets — anything related to workload scheduling. kcp is not a cluster. It's a control plane.

## Authentication and Authorization

kcp supports token-based authentication (CSV token files for development), client certificate authentication (for service-to-service communication), and OIDC (for production user authentication).

Authorization follows the standard Kubernetes RBAC model. Each workspace has its own RBAC policies. The init-agent can bootstrap RBAC during workspace initialization, creating roles and bindings scoped to each tenant.

For multi-tenant isolation, RBAC groups can be dynamically named using workspace path templates:

```yaml
subjects:
  - kind: Group
    name: "workspace:{{ .ClusterPath | replace ":" "-" }}-admin"
```

This creates unique group names per workspace, allowing fine-grained access control without manual per-tenant configuration.

## Use Case: Workflow Management with Conductor

### Why Workflow Management Fits kcp

Workflow management systems face a fundamental multi-tenancy challenge. Multiple teams need to define, execute, and monitor workflows — but their definitions must be isolated, their executions must be independent, and the system must scale without per-tenant infrastructure.

Traditional approaches either run a single shared instance (risking noisy neighbors and naming collisions) or deploy per-tenant instances (expensive and operationally complex). kcp provides a third path: a single control plane that provides each team with what feels like their own dedicated workflow service.

kcp's workspace model maps naturally to workflow management:

- Each team gets an isolated workspace with its own WorkflowDefinitions and WorkflowRuns
- The same API surface is available in every workspace via APIBinding
- RBAC controls who can define workflows vs who can only execute them
- A single controller instance manages all workspaces through the Virtual Workspace

### Architecture

The implementation consists of two custom resource types packaged in a single APIExport:

**WorkflowDefinition** — represents a workflow template registered in Conductor. Defines the task graph (HTTP calls, decision logic, parallel execution, sub-workflows, wait states), retry policies, timeouts, and input/output parameters. The schema uses `x-kubernetes-preserve-unknown-fields: true` on task definitions, ensuring any Conductor capability can be expressed — the kcp layer never limits what Conductor supports.

**WorkflowRun** — triggers an execution of a WorkflowDefinition. References the definition by name (same namespace), provides input parameters, and tracks execution status. The controller polls Conductor for execution progress and syncs task-level status back to the WorkflowRun's status subresource.

The data flow:

```
User in their workspace
    → kubectl apply WorkflowDefinition
        → kcp stores in workspace's LogicalCluster
            → VW surfaces it to the controller
                → Controller converts KRM → Conductor JSON
                    → PUT /api/metadata/workflow (registers template)
                        → Status updated: Registered

User in their workspace
    → kubectl apply WorkflowRun
        → Controller looks up WorkflowDefinition status
            → POST /api/workflow (starts execution)
                → Poll GET /api/workflow/{id} every 5 seconds
                    → Sync task statuses back to KRM status
                        → User sees: kubectl get workflowruns -w
```

### Tenant Isolation in Conductor

Conductor has a flat namespace — all workflow definitions are global. The controller prefixes every Conductor workflow name with the workspace's LogicalCluster hash:

```
1mq3akk12u3u9tvs__default__order-fulfillment
```

Two teams can both name their workflow "order-fulfillment" without collision. The prefix is invisible to users — they see and manage their workflows using the simple name.

### The Passthrough Design

Rather than modeling every Conductor field explicitly (which would lag behind Conductor releases and limit capabilities), the schema validates common fields for developer experience while accepting any additional fields via `x-kubernetes-preserve-unknown-fields: true`.

The converter starts with a complete copy of the user's spec and only transforms the two places where the KRM convention differs from Conductor's expected format:

1. `http:` → `inputParameters.http_request:` (Conductor expects HTTP config nested inside inputParameters)
2. `wait:` → `inputParameters.duration/until:` (same pattern)

Everything else passes through untouched. The Conductor client uses `map[string]interface{}` instead of typed Go structs, ensuring zero information loss during serialization.

This means: if a user can write it in Conductor's JSON, they can write the equivalent in KRM YAML. The kcp layer adds multi-tenancy, RBAC, and workspace isolation without limiting capabilities.

### Workspace Onboarding Flow

New teams are onboarded by creating a workspace of the workflow type:

```bash
kubectl ws create team-marketplace --type workflow
```

The init-agent automatically:

1. Creates an APIBinding to the `order-fulfillment` export
2. Sets up workspace-scoped RBAC (admin and viewer roles)
3. Creates a workspace-info ConfigMap with metadata
4. Removes the initializer — workspace transitions to Ready

The team can immediately start defining and running workflows:

```bash
kubectl get workflowdefinitions
kubectl get workflowruns -w
```

### What Users See

End users interact with a familiar Kubernetes experience:

```bash
# Define a workflow
kubectl apply -f order-fulfillment.yaml

# Check registration status
kubectl get workflowdefinitions
# NAME               PHASE       CONDUCTOR NAME                            VERSION   AGE
# order-fulfillment  Registered  1mq3akk12u3u9tvs__default__order-fulfil.  1        30s

# Trigger an execution
kubectl apply -f order-run.yaml

# Watch execution progress
kubectl get workflowruns -w
# NAME                     DEFINITION          PHASE      CURRENT TASK      AGE
# order-12345-fulfillment  order-fulfillment   Running    process_payment   30s
# order-12345-fulfillment  order-fulfillment   Completed                    2m
```

The entire Conductor complexity — registration API, execution API, status polling — is abstracted behind declarative KRM resources. Teams don't need Conductor credentials, don't need to know the Conductor API, and can't interfere with other teams' workflows.
