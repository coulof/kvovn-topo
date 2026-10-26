#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

OVN_VERSION=$(cat "${SCRIPT_DIR}/ovn-version")

SCHEMA_DIR="${SCRIPT_DIR}/schema"
mkdir -p "${SCHEMA_DIR}"

NB_SCHEMA="${SCHEMA_DIR}/ovn-nb.ovsschema"
SB_SCHEMA="${SCHEMA_DIR}/ovn-sb.ovsschema"

if [[ ! -f "${NB_SCHEMA}" ]]; then
  echo "Downloading OVN NB schema for ${OVN_VERSION}..."
  curl -sS -L "https://raw.githubusercontent.com/ovn-org/ovn/${OVN_VERSION}/ovn-nb.ovsschema" -o "${NB_SCHEMA}"
fi

if [[ ! -f "${SB_SCHEMA}" ]]; then
  echo "Downloading OVN SB schema for ${OVN_VERSION}..."
  curl -sS -L "https://raw.githubusercontent.com/ovn-org/ovn/${OVN_VERSION}/ovn-sb.ovsschema" -o "${SB_SCHEMA}"
fi

TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

python3 -c '
import json, sys

with open(sys.argv[1]) as f:
    nb = json.load(f)

used_nb = {
    "Logical_Router", "Logical_Router_Port", "Logical_Router_Static_Route",
    "Logical_Router_Policy", "NAT", "Gateway_Chassis", "HA_Chassis_Group",
    "HA_Chassis", "Logical_Switch", "Logical_Switch_Port", "Load_Balancer",
    "ACL", "Port_Group", "Address_Set", "NB_Global"
}
nb["tables"] = {k: v for k, v in nb["tables"].items() if k in used_nb}

with open(sys.argv[2], "w") as f:
    json.dump(nb, f, indent=2)

with open(sys.argv[3]) as f:
    sb = json.load(f)

used_sb = {
    "Chassis", "Encap", "Port_Binding", "Datapath_Binding", "BFD", "SB_Global"
}
sb["tables"] = {k: v for k, v in sb["tables"].items() if k in used_sb}

with open(sys.argv[4], "w") as f:
    json.dump(sb, f, indent=2)
' "${NB_SCHEMA}" "${TMP_DIR}/nb.ovsschema" "${SB_SCHEMA}" "${TMP_DIR}/sb.ovsschema"

echo "Generating OVN NB models in pkg/ovsdb/nbmodel..."
mkdir -p "${ROOT_DIR}/pkg/ovsdb/nbmodel"
rm -f "${ROOT_DIR}/pkg/ovsdb/nbmodel"/*.go
go run github.com/ovn-kubernetes/libovsdb/cmd/modelgen \
  -p nbmodel \
  -o "${ROOT_DIR}/pkg/ovsdb/nbmodel" \
  "${TMP_DIR}/nb.ovsschema"

echo "Generating OVN SB models in pkg/ovsdb/sbmodel..."
mkdir -p "${ROOT_DIR}/pkg/ovsdb/sbmodel"
rm -f "${ROOT_DIR}/pkg/ovsdb/sbmodel"/*.go
go run github.com/ovn-kubernetes/libovsdb/cmd/modelgen \
  -p sbmodel \
  -o "${ROOT_DIR}/pkg/ovsdb/sbmodel" \
  "${TMP_DIR}/sb.ovsschema"

# modelgen output differs between amd64 and arm64 (libovsdb MaxLength overflow);
# normalize so every machine produces the same files (tech-debt B4).
python3 -I "${SCRIPT_DIR}/normalize-models.py" "${ROOT_DIR}/pkg/ovsdb/nbmodel" "${ROOT_DIR}/pkg/ovsdb/sbmodel"

echo "Generating API types in pkg/api..."
mkdir -p "${ROOT_DIR}/pkg/api"
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
  -package api \
  -generate types,skip-prune \
  -o "${ROOT_DIR}/pkg/api/types.gen.go" \
  "${ROOT_DIR}/api/openapi.yaml"

if [[ -d "${ROOT_DIR}/ui/pkg/ovn-topology" ]]; then
  echo "Generating TypeScript types for UI..."
  mkdir -p "${ROOT_DIR}/ui/pkg/ovn-topology/types"
  npx --yes openapi-typescript "${ROOT_DIR}/api/openapi.yaml" -o "${ROOT_DIR}/ui/pkg/ovn-topology/types/api.ts"
fi

echo "Generation complete."
