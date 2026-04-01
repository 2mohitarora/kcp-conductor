for node in vcluster.cp.cluster-2 vcluster.node.cluster-2.worker-1; do
  docker exec "$node" mkdir -p /etc/containerd/certs.d/registry-2:5000
  docker exec "$node" sh -c 'cat > /etc/containerd/certs.d/registry-2:5000/hosts.toml << EOF
server = "http://registry-2:5000"

[host."http://registry-2:5000"]
  capabilities = ["pull", "resolve"]
  skip_verify = true
EOF'

  # Set config_path in containerd config
  docker exec "$node" sed -i 's|config_path = ""|config_path = "/etc/containerd/certs.d"|' /etc/containerd/config.toml

  # Restart containerd
  docker exec "$node" systemctl restart containerd

  echo "✅ Done: $node"
done