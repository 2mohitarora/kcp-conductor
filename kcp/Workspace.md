### what is tenancy.kcp.io

-   It's kcp's core multi-tenancy API group. It provides the Workspace resource — the fundamental unit of isolation in kcp.
- A Workspace in kcp is like a virtual cluster. Each one gets its own independent Kubernetes-like API surface with its own CRDs, RBAC, namespaces, and resources — fully isolated from other workspaces. The key resources in this API group:
  - workspaces.tenancy.kcp.io — what you create when you run kubectl ws create. Each workspace maps to a path like /clusters/root:my-workspace on the API server. You can nest them: root:team-a:project-1.
  - workspacetypes.tenancy.kcp.io — templates that define what a workspace comes pre-configured with (which APIs are available, default RBAC, etc.). The universal type is the default general-purpose one.
- Each workspace is a fully independent API surface that just happens to share the same backing etcd and server process.
