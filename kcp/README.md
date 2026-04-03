### Create kcp namespace
```
kubectl apply -f 00-namespace.yaml
```
### Copy the CA secret to kcp namespace
```
# Find the etcd CA secret
kubectl get secrets -n etcd | grep ca

kubectl get secret etcd-ca -n etcd -o yaml | \
  sed '/namespace:/d' | \
  kubectl apply -n kcp -f -

# Verify
kubectl get secret etcd-ca -n kcp
```

### Create etcd client certificate issuer and client certificate
```
kubectl apply -f 01-etcd-ca-issuer.yaml

kubectl get issuer -n kcp

kubectl apply -f 02-kcp-etcd-client.yaml

kubectl get certificate -n kcp
```

### Create etcd server CA (so kcp can verify etcd's TLS)
```
kubectl create secret generic kcp-etcd-client-ca -n kcp \
  --from-literal=tls.crt="$(kubectl get secret etcd-ca -n kcp -o jsonpath='{.data.tls\.crt}' | base64 -d)"

# Verify
kubectl get secret kcp-etcd-client-ca -n kcp

```

### Install kcp via helm
```
helm repo add kcp https://kcp-dev.github.io/helm-charts
helm repo update

# Install kcp
helm install kcp kcp/kcp \
  --namespace kcp \
  --values 03-kcp-values.yaml \
  --wait \
  --timeout 5m

# Once services are created get clusterip of kcp-front-proxy

kubectl get svc -n kcp | grep front-proxy

Update 031-kcp-upgrade-values.yaml with clusterip of kcp-front-proxy

helm upgrade kcp kcp/kcp \
  --namespace kcp \
  --values 031-kcp-upgrade-values.yaml \
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
curl -k --resolve kcp.example.com:443:192.168.97.254 https://kcp.example.com/readyz
# Expected: "ok"

# Test with kubectl

# Add Gateway IP to /etc/hosts
kubectl get svc -n cilium
echo "192.168.97.254 kcp.example.com" | sudo tee -a /etc/hosts


# Extract client cert and key
kubectl get secret kcp-external-admin-kubeconfig-cert -n kcp -o jsonpath='{.data.tls\.crt}' | base64 -d > /tmp/kcp-client.crt
kubectl get secret kcp-external-admin-kubeconfig-cert -n kcp -o jsonpath='{.data.tls\.key}' | base64 -d > /tmp/kcp-client.key

# Use it
KUBECONFIG=kcp-external-admin.kubeconfig kubectl api-resources --insecure-skip-tls-verify
export KUBECONFIG=kcp-external-admin.kubeconfig
# List workspaces
kubectl get workspaces --insecure-skip-tls-verify
``` 

### Install kcp kubectl plugin
```
kubectl krew index add kcp-dev https://github.com/kcp-dev/krew-index.git
kubectl krew update

kubectl krew install kcp-dev/kcp
kubectl krew install kcp-dev/ws
kubectl krew install kcp-dev/create-workspace

# Verify
kubectl kcp --version
```

### Test workspace operations
```
# Check current workspace
kubectl ws . --insecure-skip-tls-verify

# Create a workspace
kubectl create-workspace my-first-workspace --enter --insecure-skip-tls-verify

# Verify you're in it
kubectl ws . --insecure-skip-tls-verify
# Should show: root:my-first-workspace

# Go back to root
kubectl ws root --insecure-skip-tls-verify
```