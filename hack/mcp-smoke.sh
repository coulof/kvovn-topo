#!/usr/bin/env bash
set -euo pipefail

# Smoke test for `kvovn-topo mcp`: runs one stdio session (initialize, tools/list,
# one call per tool) and prints each result's summary. Arguments are passed to
# `kvovn-topo mcp` and pick the backend, e.g.:
#   hack/mcp-smoke.sh --fixture-dir testdata/scenarios/hv-01-lab
#   hack/mcp-smoke.sh --daemon-url http://127.0.0.1:8888/api/v1
# FIND_QUERY (default: kube-ovn's "join" switch) drives the find/get_node calls.

BIN="${BIN:-./bin/kvovn-topo}"
FIND_QUERY="${FIND_QUERY:-join}"
TIMEOUT="${TIMEOUT:-30}"

# Named pipes rather than coproc: coproc fds are closed in subshells, and
# recv runs in one ($(…) and pipelines).
FIFO_DIR=$(mktemp -d)
mkfifo "${FIFO_DIR}/in" "${FIFO_DIR}/out"
"${BIN}" mcp "$@" <"${FIFO_DIR}/in" >"${FIFO_DIR}/out" &
MCP_PID=$!
exec 3>"${FIFO_DIR}/in" 4<"${FIFO_DIR}/out"
# On exit, close the server's stdin so it stops cleanly; kill it only if it hangs.
cleanup() {
  exec 3>&-
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    kill -0 "${MCP_PID}" 2>/dev/null || break
    sleep 0.2
  done
  kill "${MCP_PID}" 2>/dev/null || true
  rm -rf "${FIFO_DIR}"
}
trap cleanup EXIT

send() { printf '%s\n' "$1" >&3; }

# recv ID: read frames until the response with that id; print it.
recv() {
  local line
  while IFS= read -r -t "${TIMEOUT}" -u 4 line; do
    if [[ "$(jq -r '.id // empty' <<<"${line}")" == "$1" ]]; then
      printf '%s\n' "${line}"
      return 0
    fi
  done
  echo "no response for request $1 within ${TIMEOUT}s" >&2
  return 1
}

# call ID TOOL ARGS_JSON: prints "tool: summary" or the error; the response is
# left in ${FIFO_DIR}/last. Runs in this shell so a failure sets status.
call() {
  local id=$1 tool=$2 args=$3 resp
  send "$(jq -cn --argjson id "${id}" --arg t "${tool}" --argjson a "${args}" \
    '{jsonrpc:"2.0",id:$id,method:"tools/call",params:{name:$t,arguments:$a}}')"
  resp=$(recv "${id}")
  if [[ "$(jq -r '.result.isError // false' <<<"${resp}")" == "true" ]]; then
    printf '  %-13s ERROR %s\n' "${tool}" "$(jq -r '.result.content[0].text' <<<"${resp}")" >&2
    status=1
  else
    printf '  %-13s %s\n' "${tool}" "$(jq -r '.result.structuredContent | "[\(.backend) rev \(.revision)] \(.summary)"' <<<"${resp}")"
  fi
  printf '%s\n' "${resp}" >"${FIFO_DIR}/last"
}

status=0
send '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"mcp-smoke","version":"0"}}}'
recv 1 | jq -r '"==> \(.result.serverInfo.name) \(.result.serverInfo.version), protocol \(.result.protocolVersion)"'
send '{"jsonrpc":"2.0","method":"notifications/initialized"}'

send '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
recv 2 | jq -r '"==> tools: " + ([.result.tools[] | .name + (if .annotations.readOnlyHint then "" else " (NOT read-only!)" end)] | join(", "))'

echo "==> calls (find query: ${FIND_QUERY})"
call 3 find "$(jq -cn --arg q "${FIND_QUERY}" '{query:$q, limit:5}')"
first_id=$(jq -r '.result.structuredContent.data.items[0].id // empty' "${FIFO_DIR}/last")
call 4 get_graph '{}'
call 5 get_graph '{"view":"chassis"}'
call 6 list_chassis '{}'
if [[ -n "${first_id}" ]]; then
  call 7 get_node "$(jq -cn --arg id "${first_id}" '{id:$id}')"
  call 8 get_graph "$(jq -cn --arg id "${first_id}" '{focus:$id, depth:1}')"
else
  echo "  (find returned nothing; skipping get_node and focused get_graph)"
fi

[[ ${status} -eq 0 ]] && echo "==> MCP smoke test passed" || echo "==> MCP smoke test FAILED" >&2
exit "${status}"
