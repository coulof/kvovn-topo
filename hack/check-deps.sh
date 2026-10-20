#!/usr/bin/env bash
set -euo pipefail

# ADR-0001 dependency guard:
# cmd/ovn-topologyd must NEVER depend on pkg/diag or k8s.io/client-go/tools/remotecommand

FORBIDDEN_DEPS=$(go list -deps ./cmd/ovn-topologyd | grep -E "(github.com/coulof/ovn-topology/pkg/diag|k8s.io/client-go/tools/remotecommand)" || true)

if [ -n "${FORBIDDEN_DEPS}" ]; then
  echo "ERROR: cmd/ovn-topologyd violates ADR-0001 by depending on forbidden packages:"
  echo "${FORBIDDEN_DEPS}"
  exit 1
fi

echo "ADR-0001 dependency check passed: cmd/ovn-topologyd does not depend on pkg/diag or remotecommand."
