#!/usr/bin/env bash
# Apply or delete one scenario on the local lab (hack/dev-cluster.sh).
#
#   test/e2e/scenarios/apply.sh <scenario> [apply|delete]
#
# manifests.yaml is filled from hack/lab/lab.env with envsubst (only the
# variables below). An optional node-labels file lists "<node> <key>=<value>"
# lines, applied before the manifests and removed after them.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
# shellcheck source=../../../hack/lab/lab.env
source "${ROOT_DIR}/hack/lab/lab.env"
export KUBE_OVN_VERSION EGRESS_CIDR EGRESS_GATEWAY LAB_NAME
export EGRESS_NET="${EGRESS_GATEWAY%.*}" # 192.168.107
export EGRESS_GW_CIDR="${EGRESS_GATEWAY}/${EGRESS_CIDR#*/}" # 192.168.107.1/24
KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/ovn-topology-lab.yaml}"
export KUBECONFIG

name="${1:-}"; action="${2:-apply}"
dir="${SCRIPT_DIR}/${name}"
[[ -n "${name}" && -f "${dir}/manifests.yaml" ]] || { sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 1; }

render() {
  envsubst '${KUBE_OVN_VERSION} ${EGRESS_CIDR} ${EGRESS_GATEWAY} ${EGRESS_NET} ${EGRESS_GW_CIDR} ${LAB_NAME}' < "${dir}/manifests.yaml"
}
labels() { # suffix: "" to set, "-" to remove
  [[ -f "${dir}/node-labels" ]] || return 0
  while read -r node label; do
    [[ -z "${node}" || "${node}" == \#* ]] && continue
    node=$(LAB_NAME="${LAB_NAME}" envsubst '${LAB_NAME}' <<<"${node}")
    if [[ -z "$1" ]]; then kubectl label --overwrite node "${node}" "${label}"
    else kubectl label node "${node}" "${label%%=*}-" || true; fi
  done < "${dir}/node-labels"
}

case "${action}" in
  apply)  labels ""; render | kubectl apply -f - ;;
  delete) render | kubectl delete --ignore-not-found --wait --timeout=5m -f -; labels - ;;
  *) echo "unknown action ${action}" >&2; exit 1 ;;
esac
