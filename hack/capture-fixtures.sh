#!/usr/bin/env bash
# Capture an OVN NB/SB snapshot plus the Kubernetes identity data of a live
# cluster into testdata/scenarios/<scenario>/, in the format read by
# pkg/ovsdb.LoadFixture.
#
#   hack/capture-fixtures.sh <scenario> [--flavor kube-ovn|ovnk] [--context <kubectx>]
#                            [-n <namespace>] [--out <dir>] [--setup <text>]
#
# kube-ovn (default): one NB/SB, read in the ovn-central pod of kube-system.
# --setup describes what was deployed for the scenario; it goes to meta.json.
#
# ovnk: ovn-kubernetes interconnect mode (OpenShift 4.14+) runs one NB/SB per node
# in its ovnkube-node pod; each zone goes to <scenario>/zone-<node>/ (ADR-0004, Q14).
#
# Read-only: OVSDB is read with `ovsdb-client query` select operations; Kubernetes
# with `kubectl get`. Nothing is written to the cluster. KUBECTL=oc and
# PYTHON=python override the binaries.
#
# The output contains real names, IPs, MACs and hostnames of the cluster. Review it
# before committing.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

usage() {
  sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

SCENARIO=""
FLAVOR="kube-ovn"
SETUP=""
NAMESPACE=""
OUT_BASE="${ROOT_DIR}/testdata/scenarios"
KUBECTL=("${KUBECTL:-kubectl}")
PYTHON="${PYTHON:-python3}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage 0 ;;
    --flavor) FLAVOR="$2"; shift 2 ;;
    --context) KUBECTL+=(--context "$2"); shift 2 ;;
    -n|--namespace) NAMESPACE="$2"; shift 2 ;;
    --out) OUT_BASE="$2"; shift 2 ;;
    --setup) SETUP="$2"; shift 2 ;;
    -*) echo "unknown option: $1" >&2; usage 1 ;;
    *) SCENARIO="$1"; shift ;;
  esac
done
[[ -n "${SCENARIO}" ]] || usage 1
[[ "${SCENARIO}" =~ ^[a-z0-9][a-z0-9-]*$ ]] || { echo "scenario must be lower-case kebab-case: ${SCENARIO}" >&2; exit 1; }

# Tables modelled by this build (hack/generate.sh); others are ignored by the loader.
NB_TABLES=(Logical_Router Logical_Router_Port Logical_Router_Static_Route Logical_Router_Policy
  NAT Gateway_Chassis HA_Chassis_Group HA_Chassis Logical_Switch Logical_Switch_Port
  Load_Balancer ACL Port_Group Address_Set NB_Global)
SB_TABLES=(Chassis Encap Port_Binding Datapath_Binding BFD SB_Global)
NB_SERVER="${OVN_NB_SERVER:-unix:/var/run/ovn/ovnnb_db.sock}"
SB_SERVER="${OVN_SB_SERVER:-unix:/var/run/ovn/ovnsb_db.sock}"

case "${FLAVOR}" in
  kube-ovn)
    NAMESPACE="${NAMESPACE:-kube-system}"
    SELECTOR="app=ovn-central"; NB_CONTAINER="ovn-central"; SB_CONTAINER="ovn-central" ;;
  ovnk)
    if [[ -z "${NAMESPACE}" ]]; then
      NAMESPACE="ovn-kubernetes"
      "${KUBECTL[@]}" get namespace openshift-ovn-kubernetes > /dev/null 2>&1 && NAMESPACE="openshift-ovn-kubernetes"
    fi
    SELECTOR="app=ovnkube-node"; NB_CONTAINER="nbdb"; SB_CONTAINER="sbdb" ;;
  *) echo "unknown flavor: ${FLAVOR}" >&2; usage 1 ;;
esac

OUT="${OUT_BASE}/${SCENARIO}"
mkdir -p "${OUT}"
TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT

# query_db <server> <db> <out.json> <tables...>: one select per table that exists on
# the server (older OVN versions lack some tables), converted to
# {"_date": …, "<db>": {"<table>": {"<uuid>": row}}}.
query_db() {
  local server="$1" db="$2" out="$3"; shift 3
  local present
  present=$("${EXEC[@]}" ovsdb-client list-tables "${server}" "${db}" | tr -d '\r' | tail -n +3 | awk '{print $1}')
  local tables=()
  for t in "$@"; do
    grep -qx "${t}" <<<"${present}" && tables+=("${t}")
  done
  local txn="[\"${db}\""
  for t in "${tables[@]}"; do
    txn+=",{\"op\":\"select\",\"table\":\"${t}\",\"where\":[]}"
  done
  txn+="]"
  "${EXEC[@]}" ovsdb-client query "${server}" "${txn}" > "${TMP}/${db}.result"
  printf '%s\n' "${tables[@]}" > "${TMP}/${db}.tables"
  "${PYTHON}" - "${db}" "${TMP}/${db}.tables" "${TMP}/${db}.result" "${out}" <<'PY'
import json, sys, time
db, tables_file, result_file, out = sys.argv[1:]
tables = [t for t in open(tables_file).read().split("\n") if t]
results = json.load(open(result_file))
dump = {}
for table, res in zip(tables, results):
    if "error" in res:
        sys.exit(f"{db}.{table}: {res['error']}: {res.get('details', '')}")
    rows = {}
    for row in res["rows"]:
        uuid = row.pop("_uuid")[1]
        row.pop("_version", None)
        rows[uuid] = row
    dump[table] = rows
json.dump({"_date": int(time.time()), db: dump}, open(out, "w"), indent=1, sort_keys=True)
print(f"    {db}: " + ", ".join(f"{t} {len(r)}" for t, r in dump.items()))
PY
}

# capture_zone <pod> <dir>: the NB and SB served by one pod.
capture_zone() {
  local pod="$1" dir="$2"
  echo "==> reading OVSDB through ${NAMESPACE}/${pod}"
  mkdir -p "${dir}"
  EXEC=("${KUBECTL[@]}" -n "${NAMESPACE}" exec "${pod}" -c "${NB_CONTAINER}" --)
  query_db "${NB_SERVER}" OVN_Northbound "${dir}/ovn-nb.json" "${NB_TABLES[@]}"
  EXEC=("${KUBECTL[@]}" -n "${NAMESPACE}" exec "${pod}" -c "${SB_CONTAINER}" --)
  query_db "${SB_SERVER}" OVN_Southbound "${dir}/ovn-sb.json" "${SB_TABLES[@]}"
}

ZONES=""
if [[ "${FLAVOR}" == "kube-ovn" ]]; then
  POD=$("${KUBECTL[@]}" -n "${NAMESPACE}" get pods -l "${SELECTOR}" \
    --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')
  [[ -n "${POD}" ]] || { echo "no running ovn-central pod in ${NAMESPACE}" >&2; exit 1; }
  capture_zone "${POD}" "${OUT}"
else
  # Interconnect mode: each ovnkube-node pod serves its own node's zone.
  mapfile -t ZONE_PODS < <("${KUBECTL[@]}" -n "${NAMESPACE}" get pods -l "${SELECTOR}" \
    --field-selector=status.phase=Running \
    -o jsonpath='{range .items[*]}{.metadata.name} {.spec.nodeName}{"\n"}{end}' | tr -d '\r')
  [[ -n "${ZONE_PODS[0]:-}" ]] || { echo "no running ovnkube-node pod in ${NAMESPACE}" >&2; exit 1; }
  for entry in "${ZONE_PODS[@]}"; do
    read -r POD node <<<"${entry}"
    capture_zone "${POD}" "${OUT}/zone-${node}"
    ZONES+="${ZONES:+, }\"${node}\""
  done
fi

# Kubernetes identity, trimmed to what the daemon's informers keep (pkg/graph).
echo "==> reading Kubernetes nodes and pods"
"${KUBECTL[@]}" get nodes -o json > "${TMP}/nodes.json"
"${KUBECTL[@]}" get pods -A -o json > "${TMP}/pods.json"
"${KUBECTL[@]}" get virtualmachines.kubevirt.io -A -o json > "${TMP}/vms.json" 2>/dev/null || true
"${KUBECTL[@]}" get virtualmachineinstances.kubevirt.io -A -o json > "${TMP}/vmis.json" 2>/dev/null || true
"${KUBECTL[@]}" get net-attach-def -A -o json > "${TMP}/nads.json" 2>/dev/null || true

"${PYTHON}" - "${TMP}/nodes.json" "${TMP}/pods.json" "${TMP}/vms.json" "${TMP}/vmis.json" "${TMP}/nads.json" "${OUT}" <<'PY'
import json, os, sys
nodes_in, pods_in, vms_in, vmis_in, nads_in, out = sys.argv[1:]
KEEP_NODE_ANN = {"ovn.kubernetes.io/chassis", "k8s.ovn.org/node-chassis-id", "k8s.ovn.org/zone-name"}
KEEP_POD_ANN = {"k8s.v1.cni.cncf.io/network-status"}
def meta(m, keep_ann=None):
    r = {"name": m["name"]}
    if "namespace" in m:
        r["namespace"] = m["namespace"]
    if m.get("labels"):
        r["labels"] = m["labels"]
    if keep_ann is not None:
        ann = {k: v for k, v in (m.get("annotations") or {}).items() if k in keep_ann}
        if ann:
            r["annotations"] = ann
    elif m.get("annotations"):
        r["annotations"] = m["annotations"]
    if m.get("ownerReferences"):
        r["ownerReferences"] = [{k: o[k] for k in ("apiVersion", "kind", "name", "uid") if k in o}
                                for o in m["ownerReferences"]]
    return r

nodes = [{"metadata": meta(n["metadata"], KEEP_NODE_ANN),
          "status": {"addresses": [a for a in n["status"].get("addresses", []) if a["type"] == "Hostname"]}}
         for n in json.load(open(nodes_in))["items"]]
pods = [{"metadata": meta(p["metadata"], KEEP_POD_ANN), "spec": {"nodeName": p["spec"].get("nodeName", "")}}
        for p in json.load(open(pods_in))["items"]]
nodes.sort(key=lambda n: n["metadata"]["name"])
pods.sort(key=lambda p: (p["metadata"]["namespace"], p["metadata"]["name"]))
json.dump(nodes, open(f"{out}/nodes.json", "w"), indent=1, sort_keys=True)
json.dump(pods, open(f"{out}/pods.json", "w"), indent=1, sort_keys=True)
msg = f"    {len(nodes)} nodes, {len(pods)} pods"

if os.path.exists(vms_in) and os.path.getsize(vms_in) > 0:
    raw = json.load(open(vms_in)).get("items", [])
    vms = [{"metadata": meta(v["metadata"]),
            "spec": {"running": v.get("spec", {}).get("running"),
                     "runStrategy": v.get("spec", {}).get("runStrategy"),
                     "template": {"spec": {
                         "networks": v.get("spec", {}).get("template", {}).get("spec", {}).get("networks", []),
                         "domain": {"devices": {"interfaces": v.get("spec", {}).get("template", {}).get("spec", {}).get("domain", {}).get("devices", {}).get("interfaces", [])}}
                     }}},
            "status": {"printableStatus": v.get("status", {}).get("printableStatus"),
                       "ready": v.get("status", {}).get("ready", False)}}
           for v in raw]
    vms.sort(key=lambda v: (v["metadata"].get("namespace", ""), v["metadata"]["name"]))
    json.dump(vms, open(f"{out}/virtualmachines.json", "w"), indent=1, sort_keys=True)
    msg += f", {len(vms)} VMs"

if os.path.exists(vmis_in) and os.path.getsize(vmis_in) > 0:
    raw = json.load(open(vmis_in)).get("items", [])
    vmis = [{"metadata": meta(v["metadata"]),
             "spec": {"nodeName": v.get("spec", {}).get("nodeName", ""),
                      "networks": v.get("spec", {}).get("networks", []),
                      "domain": {"devices": {"interfaces": v.get("spec", {}).get("domain", {}).get("devices", {}).get("interfaces", [])}}},
             "status": {"phase": v.get("status", {}).get("phase", ""),
                        "nodeName": v.get("status", {}).get("nodeName", ""),
                        "interfaces": v.get("status", {}).get("interfaces", [])}}
            for v in raw]
    vmis.sort(key=lambda v: (v["metadata"].get("namespace", ""), v["metadata"]["name"]))
    json.dump(vmis, open(f"{out}/virtualmachineinstances.json", "w"), indent=1, sort_keys=True)
    msg += f", {len(vmis)} VMIs"

if os.path.exists(nads_in) and os.path.getsize(nads_in) > 0:
    raw = json.load(open(nads_in)).get("items", [])
    nads = [{"metadata": meta(n["metadata"]),
             "spec": {"config": n.get("spec", {}).get("config", "")}}
            for n in raw]
    nads.sort(key=lambda n: (n["metadata"].get("namespace", ""), n["metadata"]["name"]))
    json.dump(nads, open(f"{out}/network-attachment-definitions.json", "w"), indent=1, sort_keys=True)
    msg += f", {len(nads)} NADs"

print(msg)
PY

IMAGE=$("${KUBECTL[@]}" -n "${NAMESPACE}" get pod "${POD}" -o jsonpath="{.spec.containers[?(@.name==\"${NB_CONTAINER}\")].image}")
OVN_VERSION=$("${EXEC[@]}" ovn-nbctl --version 2>/dev/null | head -1 | tr -d '\r' || true)
K8S_VERSION=$("${KUBECTL[@]}" get nodes -o jsonpath='{.items[0].status.nodeInfo.kubeletVersion}')
"${PYTHON}" - "${OUT}/meta.json" "${SCENARIO}" "${FLAVOR}" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  "${IMAGE}" "${OVN_VERSION}" "${K8S_VERSION}" "${SETUP}" "${ZONES}" <<'PY'
import json, sys
out, scenario, flavor, captured, image, ovn, k8s, setup, zones = sys.argv[1:]
meta = {"scenario": scenario, "flavor": flavor, "captured": captured, "ovnCentralImage": image,
        "ovnVersion": ovn, "kubernetesVersion": k8s,
        "zones": [z.strip().strip('"') for z in zones.split(",") if z.strip()]}
if setup:
    meta["setup"] = setup
json.dump(meta, open(out, "w"), indent=1)
open(out, "a").write("\n")
PY

echo "==> wrote ${OUT}"
echo "    next: go test ./pkg/ovsdb -run TestScenarioGoldens -update   (then review the goldens)"
echo "    the files contain real names, IPs and MACs of the cluster: review before committing"
