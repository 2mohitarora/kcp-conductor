1. Submit a workflow
```
kubectl apply -f define-orderprocessing-wf.yaml --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig --server=https://kcp.example.com:443/clusters/root:workflow-user

kubectl get wfdef --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig --server=https://kcp.example.com:443/clusters/root:workflow-user

# Check "CONDUCTOR NAME" in the output

kubectl get wfdef order-fulfillment -o yaml --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig --server=https://kcp.example.com:443/clusters/root:workflow-user

kubectl apply -f run-orderprocessing-wf.yaml --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig --server=https://kcp.example.com:443/clusters/root:workflow-user

kubectl get wfrun --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig --server=https://kcp.example.com:443/clusters/root:workflow-user

# Check "CONDUCTOR ID" in the output

kubectl get wfrun order-12345-fulfillment -o yaml --kubeconfig=./kubeconfig/kcp-external-admin.kubeconfig --server=https://kcp.example.com:443/clusters/root:workflow-user

```