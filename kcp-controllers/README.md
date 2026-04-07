## Workflow Controller using https://github.com/kcp-dev/multicluster-provider 

A kcp-aware controller that watches Workflow custom resources across all
consumer workspaces using the `multicluster-provider` apiexport provider.

## Architecture

```
                        APIExportEndpointSlice
                        (discovered automatically)
                                │
                    ┌───────────┼───────────┐
                    ▼           ▼           ▼
              VW: shard A  VW: shard B  VW: shard C
                    │           │           │
                    └─────┬─────┘           │
                          ▼                 ▼
                ┌──────────────────────────────────┐
                │     multicluster-provider        │
                │     (apiexport provider)          │
                │                                  │
                │  Merges all Workflow events into  │
                │  a single reconcile queue         │
                └──────────┬───────────────────────┘
                           ▼
                ┌──────────────────────┐
                │  Your Reconciler     │
                │                      │
                │  reconcileWorkflow() │
                │  - logs the event    │
                │  - knows which       │
                │    workspace it's    │
                │    from              │
                └──────────────────────┘
```

## How it works

1. The controller connects to the `root:workflow-admin` workspace where
   the `workflow-specs` APIExport lives.

2. The `apiexport` provider reads the `APIExportEndpointSlice` for that
   export, which lists VW URLs — one per kcp shard.

3. For each shard, it opens a WATCH on the VW endpoint. The VW only
   surfaces Workflow resources from workspaces that have an APIBinding
   to the `workflow-specs` export.

4. Any create/update/delete of a WorkflowDefinition or WorkflowRun in ANY consumer workspace
   triggers `reconcileWorkflowDefinition()` or `reconcileWorkflowRun()` with the cluster (workspace) name and the resource details.

5. When a new shard is added, a new VW URL appears in the endpoint
   slice — the provider automatically starts watching it. No restart
   needed.

## Setup

### 1. Build the kubeconfig

The controller needs a kubeconfig that points to the workspace where
the APIExport lives (`root:workflow-admin`). Build one:
```
kubectl create secret generic workflow-controller-kubeconfig \
  -n kcp \
  --from-file=kubeconfig=./kubeconfig/workflow-admin.kubeconfig
```

### 2. Build and deploy

```bash
export KO_DOCKER_REPO=localhost:5050
export DOCKER_HOST="unix:///Users/mua0008/.orbstack/run/docker.sock"
ko build -B ./workflow-controller --platform=linux/arm64

kubectl apply -f manifest/deployment.yaml
```

### 3. Test

Create a Workflow in any consumer workspace and Watch the controller logs:

```bash
kubectl logs -n kcp -l app=workflow-controller -f
```

## Key concepts

### Why unstructured?

We use `unstructured.Unstructured` instead of typed Go structs because:
- The Workflow CRD is defined via APIResourceSchema in kcp, not as Go types
- No code generation needed — the controller works immediately
- For a logging controller, unstructured is simpler
- In production, you'd generate types with controller-gen for type safety

### Why apiexport provider (not direct VW)?

The apiexport provider handles:
- VW URL discovery from APIExportEndpointSlice
- Automatic shard tracking (new shards = new VW connections)
- Per-workspace client scoping via `mgr.GetCluster()`
- Proper lifecycle management (connect/disconnect as shards come and go)

Doing this manually means writing ~200 lines of informer management code.

### mcreconcile.Request vs reconcile.Request

Standard controller-runtime gives you `reconcile.Request` with just
`Name` and `Namespace`. The multicluster version adds `ClusterName` —
this is the LogicalCluster hash (e.g., `1mq3akk12u3u9tvs`) telling
you which workspace the event came from.

Use `mgr.GetCluster(ctx, req.ClusterName)` to get a client scoped to
that specific workspace for reading/writing resources.
