# Create kcp namespace
kubectl apply -f 00-namespace.yaml

# Copy client cert to kcp namespace 
chmod +x 01-copy-client-cert-to-kcp.sh
./01-copy-client-cert-to-kcp.sh

# Verify the secret landed in kcp namespace
kubectl -n kcp get secret kcp-etcd-client