#!/bin/bash
set -e

# User-data script for OpenBench test nodes. Installs Docker everywhere, and
# when enable_k3s is true bootstraps a real multi-node k3s cluster: node 0 is
# the server and every other node joins as an agent. No pre-shared SSH keys or
# secrets are needed — bootstrap coordination flows through S3 handoff objects
# scoped to this run (see main.tf).

export DEBIAN_FRONTEND=noninteractive

apt-get update -y
apt-get install -y curl ca-certificates unzip awscli

# Docker (the container runtime for test pods)
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
usermod -aG docker ubuntu || true

# Node facts (metadata keeps both IPs regardless of whether a public one exists)
PRIVATE_IP=$(curl -s http://169.254.169.254/latest/meta-data/local-ipv4)
PUBLIC_IP=$(curl -s http://169.254.169.254/latest/meta-data/public-ipv4 || curl -s https://checkip.amazonaws.com)

if [ "${enable_k3s}" = "true" ]; then
  # Deterministic per-run token, shared by the server and every agent so nothing
  # secret needs to be persisted or transferred. The run id already carries a
  # random suffix, so the token is unique per cluster.
  export K3S_TOKEN="openbench-${run_id}"

  if [ "${node_index}" = "0" ]; then
    # ------------------------------------------------------------------ Server
    # Bind the API on all interfaces, add the public IP as a TLS SAN so the
    # orchestrator's kubeconfig validates when reached via the public IP, and
    # write the kubeconfig 0600 (only copied, never exposed via SSH).
    curl -sfL https://get.k3s.io | sh -s - server \
      --node-name "openbench-${run_id}-server" \
      --tls-san "$${PUBLIC_IP}" \
      --write-kubeconfig-mode 600

    until [ -f /etc/rancher/k3s/k3s.yaml ]; do sleep 2; done
    cp /etc/rancher/k3s/k3s.yaml /root/k3s.yaml

    if [ -n "${state_bucket}" ]; then
      # Orchestrator handoff: rewrite the loopback server address to this node's
      # public IP, base64 it, and stage it in S3 for the orchestrator to fetch.
      sed -i "s|server: https://127.0.0.1:6443|server: https://$${PUBLIC_IP}:6443|" /root/k3s.yaml
      base64 -w0 /root/k3s.yaml > /tmp/openbench-k3s.b64
      aws s3 cp /tmp/openbench-k3s.b64 "s3://${state_bucket}/kubeconfig/${run_id}/k3s.yaml.b64" \
        --region "${state_region}" --content-type text/plain || true

      # Agent handoff: publish the cluster URL the workers should join. Agents
      # stay on the private network (same VPC), so advertise the private IP.
      echo "https://$${PRIVATE_IP}:6443" > /tmp/openbench-server-url
      aws s3 cp /tmp/openbench-server-url "s3://${state_bucket}/kubeconfig/${run_id}/server-url" \
        --region "${state_region}" --content-type text/plain || true
    fi
  else
    # ------------------------------------------------------------------- Agent
    # Workers start before the server endpoint is up, so poll S3 for the server
    # URL handoff before installing k3s. Best effort: if it never appears, the
    # server still runs the job.
    SERVER_URL=""
    for _ in $(seq 1 60); do
      if [ -n "${state_bucket}" ] && aws s3 cp "s3://${state_bucket}/kubeconfig/${run_id}/server-url" \
          /tmp/openbench-server-url --region "${state_region}" >/dev/null 2>&1; then
        SERVER_URL=$(cat /tmp/openbench-server-url)
        break
      fi
      sleep 5
    done

    if [ -n "$${SERVER_URL}" ]; then
      export K3S_URL="$${SERVER_URL}"
      curl -fsSL https://get.k3s.io | sh -s - agent \
        --node-name "openbench-${run_id}-agent-${node_index}"
    fi
  fi
fi