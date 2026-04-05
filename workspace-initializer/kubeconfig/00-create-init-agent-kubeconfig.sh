# Get the CA cert
CA_DATA=$(kubectl get secret kcp-ca -n kcp -o jsonpath='{.data.ca\.crt}')

# Get the client cert and key (the admin client cert)
CLIENT_CERT=$(kubectl get secret kcp-internal-admin-kubeconfig-cert -n kcp -o jsonpath='{.data.tls\.crt}')
CLIENT_KEY=$(kubectl get secret kcp-internal-admin-kubeconfig-cert -n kcp -o jsonpath='{.data.tls\.key}')

# Build the self-contained kubeconfig
cat <<EOF > ./init-agent-kubeconfig.yaml
apiVersion: v1
kind: Config
clusters:
  - name: kcp
    cluster:
      certificate-authority-data: ${CA_DATA}
      server: "https://kcp:6443"
contexts:
  - name: kcp
    context:
      cluster: kcp
      user: admin
current-context: kcp
users:
  - name: admin
    user:
      client-certificate-data: ${CLIENT_CERT}
      client-key-data: ${CLIENT_KEY}
EOF


# Create the secret for the init-agent
kubectl create secret generic kcp-init-agent-kubeconfig \
  -n kcp \
  --from-file=kubeconfig=./init-agent-kubeconfig.yaml

rm ./init-agent-kubeconfig.yaml