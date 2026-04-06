# Workflow Controller using https://github.com/kcp-dev/multicluster-provider 

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
   the `workflows` APIExport lives.

2. The `apiexport` provider reads the `APIExportEndpointSlice` for that
   export, which lists VW URLs — one per kcp shard.

3. For each shard, it opens a WATCH on the VW endpoint. The VW only
   surfaces Workflow resources from workspaces that have an APIBinding
   to the `workflows` export.

4. Any create/update/delete of a Workflow in ANY consumer workspace
   triggers `reconcileWorkflow()` with the cluster (workspace) name
   and the resource details.

5. When a new shard is added, a new VW URL appears in the endpoint
   slice — the provider automatically starts watching it. No restart
   needed.

## Project structure

```
├── cmd/
│   └── main.go            # Entrypoint + reconciler
├── deploy/
│   └── deployment.yaml    # K8s Deployment for hosting cluster
├── Dockerfile
├── go.mod
└── README.md
```

## Setup

### 1. Build the kubeconfig

The controller needs a kubeconfig that points to the workspace where
the APIExport lives (`root:workflow-admin`). Build one:

```bash
# Navigate to the workflow-admin workspace
KUBECONFIG=kcp-admin.kubeconfig kubectl ws root:workflow-admin

# The kubeconfig now points to root:workflow-admin
# Copy it for the controller
cp kcp-admin.kubeconfig workflow-controller.kubeconfig
```

Or build a self-contained kubeconfig like we did for the init-agent.

### 2. Create the kubeconfig Secret

```bash
kubectl create secret generic workflow-controller-kubeconfig \
  -n kcp \
  --from-file=kubeconfig=workflow-controller.kubeconfig
```

### 3. Build and deploy

```bash
docker build -t yourorg/workflow-controller:latest .
docker push yourorg/workflow-controller:latest

kubectl apply -f deploy/deployment.yaml
```

### 4. Test

Create a Workflow in any consumer workspace:

```bash
KUBECONFIG=workflow-user.kubeconfig kubectl apply -f - <<EOF
apiVersion: example.com/v1alpha1
kind: Workflow
metadata:
  name: test-pipeline
  namespace: default
spec:
  description: "Test workflow"
  steps:
    - name: build
    - name: test
    - name: deploy
EOF
```

Watch the controller logs:

```bash
kubectl logs -n kcp -l app=workflow-controller -f
```

You should see:
```
INFO  Reconciling Workflow  cluster=<workspace-hash>  namespace=default  name=test-pipeline  description="Test workflow"  stepCount=3
INFO    Step  index=0  name=build
INFO    Step  index=1  name=test
INFO    Step  index=2  name=deploy
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
