#!/usr/bin/env bash
set -euo pipefail

echo "==> Kube-OVN Dev Cluster Setup"
echo "This script prepares a local kind cluster with Kube-OVN for topology testing."

CLUSTER_NAME="${CLUSTER_NAME:-ovn-topology-dev}"

if command -v kind >/dev/null 2>&1; then
  echo "kind found. To create cluster:"
  echo "  kind create cluster --name ${CLUSTER_NAME}"
  echo "  kubectl apply -f https://raw.githubusercontent.com/kubeovn/kube-ovn/master/dist/images/install.sh"
else
  echo "kind or lima required to bootstrap local Kube-OVN cluster."
fi
