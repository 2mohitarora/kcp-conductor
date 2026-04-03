# Install Tools
```
brew install kubectl helm docker go ko vcluster cilium-cli
```

# Install Orbstack
```
brew install --cask orbstack

# Add local registries that will be created later
# Add registry to docker daemon in ~/.docker/daemon.json
{
  "insecure-registries": ["localhost:5050"]
}

# Start Orbstack
```

# Configure docker
```
docker context use orbstack
export DOCKER_HOST="unix:///Users/mua0008/.orbstack/run/docker.sock"
docker context list
```

# Create your first vcluster
```
sudo vcluster create cluster-1 --driver docker --values cluster-1.yaml

helm repo add cilium https://helm.cilium.io/
helm repo add jetstack https://charts.jetstack.io
helm repo update

kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/experimental-install.yaml

helm install cilium cilium/cilium --version 1.19.1 --set kubeProxyReplacement=true --set gatewayAPI.enabled=true --namespace cilium --create-namespace --set ipam.operator.clusterPoolIPv4PodCIDRList=10.1.0.0/16

# After CNI is installed, wait for pods to become Ready:
kubectl get pods --all-namespaces -w

# Check cilium status
cilium status --namespace cilium

# Note: Make sure to configure the CNI plugin according to your cluster's pod CIDR
kubectl get configmap cilium-config -n cilium -o yaml | grep -i cidr
```

# Check Gateway Class and Create Cilium Gateway
```
kubectl get gatewayclasses -o wide

kubectl apply -f cilium-gateway.yaml

kubectl get gateways -n cilium

# Debug Cilium Gateway
# See the service Cilium created
kubectl get svc -l io.cilium.gateway/owning-gateway=default-gateway -n cilium
# See the Cilium Envoy proxy pod
kubectl -n kube-system logs -l app.kubernetes.io/name=cilium-envoy -f -n cilium

# Create a route for conductor ui service
kubectl apply -f ./conductor/conductor-ui-route.yaml
```
# Configure Registry for first cluster
```
# Start a local registry on the same Docker network as your vind cluster
docker run -d --name registry-1 --network vind-cluster-1 -p 5050:5000 registry:2

# Configure registry for cluster-1 so that nodes can pull from insecure registry
chmod +x ./cluster-1-script.sh
./cluster-1-script.sh
```

# Configure conductor 
```
# Create namespace
kubectl apply -f ./conductor/00-namespace.yaml

# Install Redis
kubectl apply -f ./conductor/01-redis.yaml
# Verify redis
kubectl -n conductor exec deploy/redis -- redis-cli ping

# Install elasticsearch
kubectl apply -f ./conductor/02-elasticsearch.yaml
# Verify Elasticsearch
kubectl -n conductor port-forward svc/elasticsearch 9200:9200

curl -s http://localhost:9200 | python3 -m json.tool
# Look for "distribution": "opensearch", "version.number": "2.18.0"

curl -s http://localhost:9200/_cluster/health | python3 -m json.tool
# Look for "status": "green" or "yellow"

# Install conductor server
kubectl apply -f ./conductor/03-conductor-server.yaml

# Get Gatway External IP
kubectl get svc cilium-gateway-default-gateway -n cilium

# Hit the URL in browser: http://<GATEWAY-EXTERNAL-IP>

```
# Install cert-manager, various components need it
```
# Install cert-manager
helm install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set crds.enabled=true

# After cert-manager, wait for pods to become Ready:
kubectl get pods --all-namespaces -w -o wide
```

# Move to kcp etcd installation

# Move to kcp installation