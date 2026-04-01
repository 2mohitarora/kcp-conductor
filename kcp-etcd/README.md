kubectl apply -f 00-namespace.yaml

kubectl apply -f 01-pki.yaml

# 2. Wait for CA to be issued
kubectl -n etcd wait --for=condition=Ready certificate/etcd-ca --timeout=120s

# 3. Create certificates
kubectl apply -f 02-certificates.yaml

# 4. Wait for all certs
kubectl -n etcd get certificates -w
# (wait until all 3 show READY=True)

# 5. Deploy etcd
kubectl apply -f 03-etcd.yaml
kubectl -n etcd rollout status statefulset/etcd --timeout=180s

# 6. Verify etcd is healthy
kubectl -n etcd exec etcd-0 -- etcdctl endpoint health \
  --endpoints=https://localhost:2379 \
  --cacert=/etc/etcd/tls/ca/ca.crt \
  --cert=/etc/etcd/tls/server/tls.crt \
  --key=/etc/etcd/tls/server/tls.key

# 7. Copy client cert to kcp namespace (creates 'kcp' namespace if needed)
kubectl apply -f 04-copy-client-cert-to-kcp.yaml
kubectl -n etcd wait --for=condition=Complete job/copy-etcd-client-cert-to-kcp --timeout=120s

# 8. Verify the secret landed in kcp namespace
kubectl -n kcp get secret kcp-etcd-client