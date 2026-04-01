# Copy client cert to kcp namespace (creates 'kcp' namespace if needed)
kubectl apply -f 04-copy-client-cert-to-kcp.yaml
kubectl -n etcd wait --for=condition=Complete job/copy-etcd-client-cert-to-kcp --timeout=120s

# Verify the secret landed in kcp namespace
kubectl -n kcp get secret kcp-etcd-client