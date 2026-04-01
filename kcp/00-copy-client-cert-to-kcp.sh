echo "Extracting certs..."
CLIENT_CRT=$(kubectl -n etcd get secret etcd-client-kcp-tls -o jsonpath='{.data.tls\.crt}')
CLIENT_KEY=$(kubectl -n etcd get secret etcd-client-kcp-tls -o jsonpath='{.data.tls\.key}')
CA_CRT=$(kubectl -n etcd get secret etcd-ca -o jsonpath='{.data.ca\.crt}')

echo "Creating Secret in kcp namespace..."
kubectl -n kcp create secret generic kcp-etcd-client \
  --from-literal="tls.crt=${CLIENT_CRT}" \
  --from-literal="tls.key=${CLIENT_KEY}" \
  --from-literal="ca.crt=${CA_CRT}"

echo ""
echo "Done! Secret 'kcp-etcd-client' created in namespace 'kcp'."
echo ""
echo "kcp server should use:"
echo "  --etcd-servers=https://etcd-client.etcd.svc:2379"
echo "  --etcd-certfile=/etc/kcp/tls/etcd-client/tls.crt"
echo "  --etcd-keyfile=/etc/kcp/tls/etcd-client/tls.key"
echo "  --etcd-cafile=/etc/kcp/tls/etcd-client/ca.crt"