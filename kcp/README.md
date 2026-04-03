### Create kcp namespace
kubectl apply -f 00-namespace.yaml

# Copy the CA secret to kcp namespace
```
# Find the etcd CA secret
kubectl get secrets -n etcd | grep ca

kubectl get secret etcd-ca -n cert-manager -o yaml | \
  sed '/namespace:/d' | \
  kubectl apply -n kcp -f -

# Verify
kubectl get secret etcd-ca -n kcp

# Also verify in yaml format
kubectl get secret etcd-ca -n kcp -o yaml
```

### Create etcd client certificate
```
kubectl apply -f 01-kcp-etcd-client-cert.yaml

# Make sure certificate is ready
kubectl get certificate -n kcp
```

### Create kcp required certificates
```
# Change dnsNames in 02-kcp-certificates.yaml (2 places)

kubectl apply -f 02-kcp-certificates.yaml
```

### Install kcp via helm
```
helm repo add kcp https://kcp-dev.github.io/helm-charts
helm repo update

# Inspect the chart before applying values
helm show values kcp/kcp > 03-kcp-defaults.yaml

# Compare with your values to catch any key mismatches
# Change externalHostname in 03-kcp-values.yaml

# Install kcp
helm install kcp kcp/kcp \
  --namespace kcp \
  --values 03-kcp-values.yaml \
  --wait \
  --timeout 5m
```
### Verify

```
# Check pods
kubectl get pods -n kcp

# You should see:
# kcp-<hash>              1/1  Running
# kcp-front-proxy-<hash>  1/1  Running

# Check logs for errors
kubectl logs -n kcp -l app=kcp --tail=50
kubectl logs -n kcp -l app=kcp-front-proxy --tail=50
```
### Add kcp front end proxy route
```
# Change hostname in 04-kcp-route.yaml

kubectl apply -f 04-kcp-route.yaml
```

### Configure DNS
```
# Get the Gateway's external IP
kubectl get gateway default-gateway -n cilium

# Create a DNS A record:
# kcp.example.com → <Gateway External IP>
```

### Verify kcp is reachable
```
# Test with curl (use -k for self-signed certs)
curl -k https://kcp.example.com/readyz
# Expected: "ok"

# Test with kubectl
# First, extract the admin kubeconfig (the Helm chart creates one)
kubectl get secret -n kcp kcp-admin-kubeconfig -o jsonpath='{.data.kubeconfig}' \
  | base64 -d > kcp-admin.kubeconfig

# Use it
KUBECONFIG=kcp-admin.kubeconfig kubectl api-resources
``` 

### Install kcp kubectl plugin
```
# Install the workspace plugin
kubectl krew install kcp

# Or download directly from GitHub releases:
# https://github.com/kcp-dev/kcp/releases
```

### Test workspace operations
```
export KUBECONFIG=kcp-admin.kubeconfig

# Check current workspace
kubectl ws .

# List workspaces
kubectl get workspaces

# Create a workspace
kubectl ws create my-first-workspace --enter

# Verify you're in it
kubectl ws .
# Should show: root:my-first-workspace

# Go back to root
kubectl ws root
```