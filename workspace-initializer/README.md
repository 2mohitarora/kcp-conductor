### Pre-requisites - Create kubeconfig for init-agent
```
chmod +x kubeconfig/00-create-init-agent-kubeconfig.sh
./kubeconfig/00-create-init-agent-kubeconfig.sh
```

### Install kcp init-agent on hosting cluster 
```
helm repo add kcp https://kcp-dev.github.io/helm-charts
helm repo update

helm upgrade --install init-agent-host kcp/init-agent \
  --namespace kcp \
  --values helm/00-kcp-init-agent-values.yaml \
  --set "targets={host}" \
  --wait \
  --timeout 5m 
```

### Switch to root:init-agent workspace and setup the workspace
```
## init-agent workspace is where your InitTarget and InitTemplate objects live. This Helm install creates the CRDs and RBAC granting the agent permission to read InitTargets and InitTemplates in this workspace. Please note this is now happening within kcp

helm upgrade --install init-agent-crds kcp/init-agent \
  --namespace kcp-init-agent \
  --create-namespace \
  --values helm/00-kcp-init-agent-values.yaml \
  --set "targets={configcluster}" \
  --kubeconfig ./kubeconfig/kcp-init-agent.kubeconfig

## Verify CRDs are installed
kubectl get crds --kubeconfig=./kubeconfig/kcp-init-agent.kubeconfig
```

### Switch to root workspace and install RBAC
```
# Install 3 — RBAC into root
helm upgrade --install init-agent-rbac kcp/init-agent \
  --namespace kcp \
  --create-namespace \
  --values helm/00-kcp-init-agent-values.yaml \
  --set "targets={wstcluster}" \
  --kubeconfig ./kubeconfig/kcp-external-admin.kubeconfig
  
# Verify
kubectl get clusterrolebinding --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig
kubectl get clusterrole --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig  
```

### Steps to create workspace of type workflow and see if it is getting initialized properly

1. Create the WorkspaceType in root
```
kubectl apply -f 01-workflow-workspacetype.yaml --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig
```

2. Create InitTemplate + InitTarget in root:init-agent
```
kubectl apply -f 02-workflow-ws-init-target.yaml --kubeconfig=./kubeconfig/kcp-init-agent.kubeconfig
```

3. Create workflowdefinition and workflowrun specs in worflow-admin workspace and create an API export for it
```
kubectl apply -f 03-workflow-export.yaml --kubeconfig=./kubeconfig/workflow-admin.kubeconfig
```

4. Create a workspace with the workflow type
```
kubectl create-workspace workflow-user --type=workflow \
  --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig
```  

5. Verify initialization is happening
```
kubectl get workspaces --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig

kubectl get apibindings --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig --server=https://kcp.example.com:443/clusters/root:workflow-user
```

6. Try something really cool 
```
kubectl get wfdef --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig --server=https://kcp.example.com:443/clusters/root:workflow-user

kubectl get wfrun --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig --server=https://kcp.example.com:443/clusters/root:workflow-user

```