#!/usr/bin/env bash
# The ovn-topology lab: k3s with Kube-OVN as the CNI, in three local Lima VMs
# (one server, two agents), for the scenario captures in test/e2e/scenarios/
# (ROADMAP B1). Versions are pinned in hack/lab/lab.env.
#
#   hack/dev-cluster.sh create   create the VMs and write ~/.kube/ovn-topology-lab.yaml
#   hack/dev-cluster.sh status   VMs, nodes and Kube-OVN pods
#   hack/dev-cluster.sh delete   delete the VMs
#
# Needs limactl (rootless, qemu + /dev/kvm). Never uses sudo on this machine.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lab/lab.env
source "${SCRIPT_DIR}/lab/lab.env"
SERVER="${LAB_NAME}-server"
AGENTS=("${LAB_NAME}-agent1" "${LAB_NAME}-agent2")
KUBECONFIG_OUT="${HOME}/.kube/ovn-topology-lab.yaml"
CTX=ovn-topology-lab

die() { echo "error: $*" >&2; exit 1; }
kc() { kubectl --kubeconfig "${KUBECONFIG_OUT}" "$@"; }
exists() { limactl list -q 2>/dev/null | grep -qx "$1"; }

create_vm() { # name template params...
  local name=$1 tmpl=$2; shift 2
  exists "${name}" && { limactl start "${name}" 2>/dev/null || true; return; }
  local sets=()
  for kv in "$@"; do sets+=(--set ".param.${kv%%=*} = \"${kv#*=}\""); done
  limactl create --tty=false --name "${name}" "${sets[@]}" "${SCRIPT_DIR}/lab/${tmpl}"
  limactl start --tty=false "${name}"
  # Lima reports READY even when the provision script failed.
  if limactl shell "${name}" sudo grep -q 'Failed to execute /mnt/lima-cidata/provision' /var/log/cloud-init-output.log; then
    limactl shell "${name}" sudo tail -20 /var/log/cloud-init-output.log >&2
    die "provisioning failed on ${name}"
  fi
}

create() {
  command -v limactl >/dev/null || die "limactl not found; install Lima (https://lima-vm.io/docs/installation/), e.g.:
  curl -sSfL https://github.com/lima-vm/lima/releases/download/v2.2.1/lima-2.2.1-Linux-x86_64.tar.gz | tar -xz -C ~/.local"
  command -v kubectl >/dev/null || die "kubectl not found"
  [[ -w /dev/kvm ]] || die "/dev/kvm is not writable: Lima needs KVM"


  create_vm "${SERVER}" k3s-server.yaml K3S_VERSION="${K3S_VERSION}" KUBE_OVN_VERSION="${KUBE_OVN_VERSION}" \
    MULTUS_VERSION="${MULTUS_VERSION}" LAB_TOKEN="${LAB_TOKEN}"
  local server_ip
  server_ip=$(limactl shell "${SERVER}" ip -4 -o addr show eth0 | awk '{print $4}' | cut -d/ -f1)
  [[ -n "${server_ip}" ]] || die "no eth0 address on ${SERVER}"
  for a in "${AGENTS[@]}"; do
    create_vm "${a}" k3s-agent.yaml K3S_VERSION="${K3S_VERSION}" LAB_TOKEN="${LAB_TOKEN}" SERVER_IP="${server_ip}"
  done

  mkdir -p "$(dirname "${KUBECONFIG_OUT}")"
  limactl shell "${SERVER}" sudo cat /etc/rancher/k3s/k3s.yaml |
    sed -e "s#https://127.0.0.1:6443#https://127.0.0.1:16443#" -e "s/: default$/: ${CTX}/" > "${KUBECONFIG_OUT}"
  chmod 600 "${KUBECONFIG_OUT}"

  echo "==> waiting for Kube-OVN and the nodes"
  kc wait --for=condition=Ready nodes --all --timeout=15m
  for r in deploy/ovn-central deploy/kube-ovn-controller ds/kube-ovn-cni ds/kube-multus-ds; do
    kc -n kube-system rollout status "${r}" --timeout=10m
  done
  status
  echo "Ready: export KUBECONFIG=${KUBECONFIG_OUT}  (context ${CTX})"
}

status() {
  limactl list "${SERVER}" "${AGENTS[@]}" 2>/dev/null || true
  [[ -f "${KUBECONFIG_OUT}" ]] || return 0
  kc get nodes -o wide
  kc -n kube-system get pods -o wide | grep -E 'ovn|ovs|multus' || true
}

delete() {
  for vm in "${AGENTS[@]}" "${SERVER}"; do
    exists "${vm}" && limactl delete --force "${vm}"
  done
  rm -f "${KUBECONFIG_OUT}"
}

case "${1:-}" in
  create) create ;;
  status) status ;;
  delete) delete ;;
  *) sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
