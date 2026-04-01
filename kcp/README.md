# Create kcp namespace
kubectl apply -f 00-namespace.yaml

# Copy client cert to kcp namespace 
chmod +x 00-copy-client-cert-to-kcp.sh
./00-copy-client-cert-to-kcp.sh

# Verify the secret landed in kcp namespace
kubectl -n kcp get secret kcp-etcd-client

# ─── Step 1: PKI bootstrap ──────────────────────────────────────
kubectl apply -f 01-pki-issuers.yaml
kubectl -n kcp wait --for=condition=Ready certificate/kcp-pki-ca --timeout=120s

# ─── Step 2: Intermediate CAs ───────────────────────────────────
kubectl apply -f 02-intermediate-cas.yaml
# Wait for all CAs to be Ready
kubectl -n kcp get certificates -w

# ─── Step 3: Issuers ────────────────────────────────────────────
kubectl apply -f 03-issuers.yaml

# ─── Step 4: All certificates ───────────────────────────────────
kubectl apply -f 06-server-certificates.yaml
kubectl apply -f 07-front-proxy-certificates.yaml
kubectl apply -f 08-server-kubeconfigs.yaml
kubectl apply -f 09-front-proxy-kubeconfig.yaml
# Wait for all certs
kubectl -n kcp get certificates -w

# ─── Step 5: Generate front-proxy kubeconfig ─────────────────────
kubectl apply -f 14-front-proxy-kubeconfig-job.yaml
kubectl -n kcp wait --for=condition=Complete \
  job/kcp-front-proxy-generate-kubeconfig --timeout=120s

# ─── Step 6: Deploy kcp server + front-proxy ─────────────────────
kubectl apply -f 10-server.yaml
kubectl apply -f 11-front-proxy-configmap.yaml
kubectl apply -f 12-front-proxy.yaml

kubectl -n kcp rollout status deployment/kcp --timeout=300s
kubectl -n kcp rollout status deployment/kcp-front-proxy --timeout=120s

# ─── Step 7: Generate admin kubeconfig ───────────────────────────
kubectl apply -f 13-admin-kubeconfig-job.yaml
kubectl -n kcp wait --for=condition=Complete \
  job/kcp-generate-admin-kubeconfig --timeout=120s

# ─── Step 8: Extract admin kubeconfig ────────────────────────────
kubectl -n kcp get secret kcp-external-admin-kubeconfig \
  -o jsonpath='{.data.kubeconfig}' | base64 -d > kcp-admin.kubeconfig

## Verify

```bash
KUBECONFIG=./kcp-admin.kubeconfig kubectl api-resources
KUBECONFIG=./kcp-admin.kubeconfig kubectl get workspaces
```

## Files

```
00-namespace.yaml               # kcp namespace
01-pki-issuers.yaml             # Self-signed bootstrap → root CA → intermediate issuer
02-intermediate-cas.yaml        # 5 intermediate CAs (server, front-proxy, SA, etc.)
03-issuers.yaml                 # 5 cert-manager Issuers
06-server-certificates.yaml     # kcp server + virtual workspace + SA signing certs
07-front-proxy-certificates.yaml  # front-proxy serving, requestheader, VW, admin certs
08-server-kubeconfigs.yaml      # Internal admin client cert
09-front-proxy-kubeconfig.yaml  # Front-proxy → kcp client cert
10-server.yaml                  # kcp server Deployment + Service
                                #   connects to etcd via: etcd-client.etcd.svc:2379
                                #   uses secret: kcp-etcd-client (from etcd namespace)
11-front-proxy-configmap.yaml   # Path mapping config
12-front-proxy.yaml             # Front-proxy Deployment + Service (:8443)
13-admin-kubeconfig-job.yaml    # Job to assemble admin kubeconfig Secret
14-front-proxy-kubeconfig-job.yaml  # Job to assemble front-proxy kubeconfig
```