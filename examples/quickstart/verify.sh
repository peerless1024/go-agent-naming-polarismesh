#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../internal" && pwd)"
source "${COMMON_DIR}/e2e_common.sh"

feature_common_init quickstart
feature_usage() {
    echo "Usage: ./examples/quickstart/verify.sh --polaris-server <host> [--debug]"
}
feature_parse_transport_args "$@"

SERVICE_NAME="trpc.polaris.demo.quickstart"
PROVIDER_PORT="${PROVIDER_PORT:-28211}"

build_demo() {
    (
        cd "${REPO_ROOT}"
        env GOCACHE="${GOCACHE:-/private/tmp/go-agent-naming-demo-cache}" \
            go build -o "${BUILD_DIR}/provider" ./examples/quickstart/provider
        env GOCACHE="${GOCACHE:-/private/tmp/go-agent-naming-demo-cache}" \
            go build -o "${BUILD_DIR}/client" ./examples/quickstart/client
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
if ! jq -e '
    length == 2 and
    all(.[]; .success == true and (.response | length) > 0) and
    (map(select(.mode == "unary")) | length == 1) and
    (map(select(.mode == "stream" and .events > 1)) | length == 1)
' >/dev/null <<<"${result}"; then
    echo "quickstart unary/stream failed: ${result}" >&2
    exit 1
fi
echo "[case 1.1 unary discovery and call] PASS"
echo "[case 1.2 streaming discovery and call] PASS"
echo "verification complete; Provider remains running until cleanup.sh"
