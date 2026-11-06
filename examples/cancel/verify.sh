#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../internal" && pwd)"
source "${COMMON_DIR}/e2e_common.sh"

feature_common_init cancel
feature_usage() {
    echo "Usage: ./examples/cancel/verify.sh --polaris-server <host> [--debug]"
}
feature_parse_transport_args "$@"

SERVICE_NAME="trpc.polaris.demo.cancel"
PROVIDER_PORT="${PROVIDER_PORT:-28213}"

build_demo() {
    (
        cd "${REPO_ROOT}"
        env GOCACHE="${GOCACHE:-/private/tmp/go-agent-naming-demo-cache}" \
            go build -o "${BUILD_DIR}/provider" ./examples/cancel/provider
        env GOCACHE="${GOCACHE:-/private/tmp/go-agent-naming-demo-cache}" \
            go build -o "${BUILD_DIR}/client" ./examples/cancel/client
    )
}

start_provider() {
    local debug_flag=()
    if [[ "${DEBUG_MODE:-false}" == "true" ]]; then
        debug_flag=(-debug)
    fi
    (
        cd "${BUILD_DIR}"
        exec ./provider \
            -server "${POLARIS_SERVER}" \
            -polaris-port "${POLARIS_GRPC_PORT}" \
            -namespace "${NAMESPACE}" \
            -service "${SERVICE_NAME}" \
            -port "${PROVIDER_PORT}" \
            ${debug_flag[@]+"${debug_flag[@]}"}
    ) > "${LOG_DIR}/provider.log" 2>&1 &
    local pid=$!
    if ! record_process "${pid}" provider "${PROVIDER_PORT}"; then
        stop_pid "${pid}"
        return 1
    fi
    echo "${pid}"
}

build_demo
ensure_namespace
ensure_service "${SERVICE_NAME}"
provider_pid="$(start_provider)"
wait_for_instances "${SERVICE_NAME}" 1 "${provider_pid}"

(
    cd "${BUILD_DIR}"
    exec ./client \
        -server "${POLARIS_SERVER}" \
        -polaris-port "${POLARIS_GRPC_PORT}" \
        -namespace "${NAMESPACE}" \
        -service "${SERVICE_NAME}"
) > "${LOG_DIR}/client.json" 2> "${LOG_DIR}/client.log"

result="$(cat "${LOG_DIR}/client.json")"
if ! jq -e '(.first_event | length) > 0' >/dev/null <<<"${result}"; then
    echo "Polaris-discovered stream produced no first event: ${result}" >&2
    exit 1
fi
echo "[case 3.1 Polaris discovery and streaming] PASS"
if ! jq -e '.stream_closed_ms <= 3000' >/dev/null <<<"${result}"; then
    echo "stream did not stop within 3 seconds: ${result}" >&2
    exit 1
fi
echo "[case 3.2 cancel discovered remote Agent stream] PASS"
if ! jq -e '.active == 0 and .canceled > 0' >/dev/null <<<"${result}"; then
    echo "Provider did not release canceled run: ${result}" >&2
    exit 1
fi
echo "[case 3.3 Provider releases canceled run] PASS"
echo "verification complete; Provider remains running until cleanup.sh"
