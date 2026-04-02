### KCP Architecture

1. Front-proxy

This is the single entry point for all external clients. Think of it as a smart reverse proxy sitting in front of all your shards. Here's the request lifecycle:

Step-by-step flow: A client (like kubectl) sends a request to the front-proxy's external hostname → the front-proxy determines which shard hosts the target workspace → it proxies the request to that shard's baseURL → the shard processes it and returns the response through the front-proxy.

Shards only contact the front-proxy in two specific cases: 
- when scheduling a new workspace (to randomly pick a shard) 
- when updating endpoint URLs for APIExportEndpointSlice or CachedResourceEndpointSlice resources.

You configure it with --externalHostname or spec.external.hostname.

2. Shards

Each shard is an independent kcp API server instance that hosts a subset of workspaces. Shards can be exposed publicly (accessible from outside the cluster) or kept private behind the front-proxy. 

The critical rule is that shards never communicate directly with each other. All inter-shard communication goes through the front-proxy or cache server.

Key configuration: Each shard has a spec.baseURL (how the front-proxy reaches it) and optionally a spec.externalURL (how external clients reach it). 

If externalURL is empty, it defaults to baseURL, and if virtualWorkspaceURL is empty, it also defaults to baseURL.

Each shard runs its own etcd instance for storage.

3. Virtual workspaces

These provide computed API views — they don't store data themselves but project and filter data from shards into logical API surfaces. The recommended approach is to run virtual workspaces inside shards rather than separately. 

External clients like syncers need access to the virtualWorkspaceURL endpoints.

### How the front-proxy routes requests to the right shard?

The front-proxy is aware of all logical clusters, their shard, their canonical paths, and all Workspaces. 

Requests to /clusters/<path> are forwarded to the shard via inverse proxying.

The resolution chain works like this: when a client sends a request to /clusters/root:team-a:dev, the front-proxy walks the workspace path segments (root → team-a → dev), resolves each Workspace object to find its referenced LogicalCluster, then looks up which Shard object hosts that logical cluster. It then reverse-proxies the request to that shard's baseURL.

During workspace creation and scheduling, the scheduler running on the shard hosting the Workspace object chooses a random logical cluster name optimistically and picks a shard that name maps to through consistent hashing. If there's a collision, it retries with a different name.

### Cache server
The primary purpose of the cache server is to support cross-shard communication. Direct shard-to-shard communication is not allowed. So instead of every shard talking to every other shard, they all talk to one central cache server.

It works through two modes:
- Write mode — a write controller runs on a shard, holds some state from that shard in memory, and pushes it to the cache server using standard informers. Before pushing, it compares remote data to its local copy to ensure consistency.
- Read mode — a read controller on another shard reads the first shard's data from the cache server, again using standard Kubernetes informers and list/watch semantics.

### Why do shars need to talk to each other?

- Scenario 1 (API exports) is the big one. kcp's whole value proposition is that an API provider in one workspace can export an API, and consumers in any workspace can bind to it — even across shard boundaries. The controller for the API provider needs to see and manage all bound resources across every consuming workspace, regardless of which shard those consumers landed on. Without cross-shard communication, an API export would only work within a single shard, which defeats the purpose of kcp's multi-tenant API model.

- Scenario 2 (scheduling) is inherent to sharding. The scheduler running on the shard hosting the Workspace object chooses a random logical cluster name and picks a shard through consistent hashing. It then tries to create the LogicalCluster on that remote shard, and keeps watching it during initialization. The Workspace "pointer" and the actual LogicalCluster data can live on different shards by design.

- Scenario 3 (global objects) Things like Shard objects (the registry of all shards), WorkspaceType definitions, and APIExport metadata live on the root shard but every shard needs to read them — for scheduling decisions, workspace creation, API resolution, etc.

### What happens when a new shard is created

- You deploy a new etcd instance and a kcp shard process (e.g. kcp-alpha-shard) with its own etcd. At this point the shard is running but nobody knows it exists — it's just a standalone kcp API server with an empty etcd. We need to make sure the front-proxy, cache server, and all existing shards learn the new shard exists.

- Create a Shard object on the root shard. This is the registry entry point. An admin (or the kcp-operator) creates a Shard object in the root logical cluster on the root shard. This object contains the new shard's baseURL (how to reach it internally), externalURL (how external clients reach it, defaults to baseURL), and virtualWorkspaceURL (where VW endpoints are served, defaults to baseURL). The Shard object is stored in root's etcd. 

The front-proxy watches all Shard objects in the root logical cluster. When the new Shard object appears, the front-proxy adds the new shard's baseURL to its routing table. It now knows: "if a workspace's LogicalCluster lives on shard alpha, proxy the request to https://alpha.example.com:6443." Until workspaces are actually scheduled to this shard, the front-proxy won't route anything there yet.

- Root shard replicates the Shard object to the cache server : The root shard's write controller pushes the new Shard object (and other global objects like WorkspaceTypes) to the cache server. Now all other existing shards — which run read controllers watching the cache server — learn about the new shard. This is critical: when any shard's scheduler needs to pick a shard for a new workspace, it reads the list of available shards from the cache server, not from the root shard directly. Without this step, existing shards wouldn't know the new shard is available for scheduling.

- APIExportEndpointSlices grow to include the new shard
The new shard starts its virtual workspace server (usually in-process). Its virtualWorkspaceURL is used to construct new entries in APIExportEndpointSlice and CachedResourceEndpointSlice objects. For example, if there's a tenancy.kcp.io APIExport, a new endpoint URL like https://alpha.example.com:6443/services/apiexport/alpha/tenancy.kcp.io is added. External clients (like syncers) that watch these endpoint slices pick up the new URL and start connecting to the new shard's virtual workspace.

- The new shard is ready to receive workspaces : The new shard is now fully integrated. The scheduler on any shard can select it when creating new workspaces (via consistent hashing on the cluster name). The front-proxy can route requests to it. Other shards know it exists via the cache server. API controllers can reach its virtual workspace endpoints. New workspaces will start landing on it based on the hashing distribution.

### What happens when a new workspace is created

- User sends: create Workspace 'staging' in root:team-a: The user runs kubectl create workspace staging while in the root:team-a context. The request hits the front-proxy at /clusters/root:team-a.
- The front-proxy knows that the logical cluster behind root:team-a lives on the root shard. It reverse-proxies the POST Workspace request to https://root.example.com:6443. The Workspace object is now stored in root shard's etcd, inside the root:team-a logical cluster.
- Scheduler on root shard generates a random cluster name and hashes it to a shard: The workspace scheduling controller running on the root shard sees the new Workspace object. It generates a random logical cluster name (e.g. "3xk9f2m7") and uses consistent hashing to pick a shard for it. Let's say it picks the alpha shard. It creates a LogicalCluster object on the alpha shard with that name, pointing to the new workspace.
- Scheduler reaches across to shard alpha and creates the LogicalCluster: The scheduler on root shard contacts shard alpha directly (via its baseURL) and tries to create a LogicalCluster object named 3xk9f2m7. This is the actual data container — it's like creating a new empty database on alpha.
- Root shard watches the remote LogicalCluster on alpha until it's ready: The workspace controller on the root shard starts watching the LogicalCluster on shard alpha (cross-shard watch). It updates Workspace.spec.cluster to 3xk9f2m7 and sets the phase to Initializing. If the WorkspaceType has initializers defined, initialization controllers (running via virtual workspace endpoints) can now access the new workspace through the VW server on shard alpha to set up default resources, RBAC, etc. Each initializer removes itself from the list when done.
- Workspace object on root shard is updated with the shard location: Once the LogicalCluster is ready and all initializers are done, the workspace controller on the root shard updates the Workspace object's phase to Ready and sets spec.cluster to the logical cluster name (3xk9f2m7). From this point on, the workspace is considered ready for normal use.  The canonical path: root:team-a:staging
- Front-proxy learns the new path → shard mapping: The front-proxy watches Shard objects in the root logical cluster. When the new LogicalCluster is created on shard alpha, the workspace controller on the root shard updates the Workspace object to point to that logical cluster. The front-proxy sees the Workspace object change and updates its internal routing: requests for /clusters/root:team-a now go to https://alpha.example.com:6443. Any future kubectl request to /clusters/root:team-a:staging gets proxied to shard alpha's baseURL.
- User can now work in the new workspace — routed transparently to shard alpha: The user runs kcp workspace use staging and starts creating resources. Every request goes: user → front-proxy → shard alpha. The user has no idea their workspace is on shard alpha — they just see root:team-a:staging. The indirection is completely transparent.

### Why Virtual workapce URL?

- Security isolation. Controllers should not be able to directly access customer kcp workspaces — they should only be able to access the objects connected to their provided APIs. If your Widget controller had a regular kubeconfig to each tenant's workspace, it could see their Secrets, ConfigMaps, RBAC — everything. The VW endpoint is a filtered projection that only exposes Widget resources tied to your APIExport. The tenants' other data is invisible.

- Multi-workspace aggregation. A regular kubeconfig points to one workspace. But your APIExport might be consumed by 500 workspaces across 3 shards. You'd need 500 separate informers. Because multiple workspaces coexist in kcp, we can be much more efficient and have one controller handle objects in multiple workspaces. The VW URL gives you a single endpoint per shard that aggregates all bound resources across every workspace on that shard.

- Shard-aware scaling. As soon as sharding is added, the list of URLs becomes a list. So it might be that 1,000 tenants are accessible under one URL, the next 1,000 under another. The controllers have to watch the URL lists in status of objects and start new instances. Your controller watches the APIExportEndpointSlice — it gets one VW URL per shard. For 3 shards, that's 3 informer connections instead of potentially thousands.






