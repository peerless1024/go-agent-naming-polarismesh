#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../internal" && pwd)"
source "${COMMON_DIR}/e2e_common.sh"

feature_common_init a2a-agentcard
feature_usage() {
    echo "Usage: ./examples/a2a-agentcard/verify.sh --polaris-server <host> [--debug]"
}
feature_parse_transport_args "$@"

SERVICE_A="trpc.polaris.demo.agent.a"
SERVICE_B="trpc.polaris.demo.agent.b"
AGENT_A_PORT="${AGENT_A_PORT:-28220}"
AGENT_B_V1_PORT="${AGENT_B_V1_PORT:-28221}"
AGENT_B_V2_PORT="${AGENT_B_V2_PORT:-28222}"
AGENT_A_PID=""
AGENT_B_V1_PID=""
AGENT_B_V2_PID=""

on_error() {
    local pid
    for pid in "${AGENT_A_PID}" "${AGENT_B_V1_PID}" "${AGENT_B_V2_PID}"; do
        if [[ -n "${pid}" ]]; then
            stop_pid "${pid}" || true
        fi
    done
}
trap on_error ERR

build_demo() {
    (
        cd "${REPO_ROOT}"
        env GOCACHE="${GOCACHE:-/private/tmp/go-agent-naming-demo-cache}" \
            go build -o "${BUILD_DIR}/provider" ./examples/a2a-agentcard/provider
        env GOCACHE="${GOCACHE:-/private/tmp/go-agent-naming-demo-cache}" \
            go build -o "${BUILD_DIR}/client" ./examples/a2a-agentcard/client
    )
}

start_provider() {
    local role="$1" service="$2" version="$3" port="$4" log="$5"
    shift 5
    (
        cd "${BUILD_DIR}"
        exec nohup ./provider \
            -server "${POLARIS_SERVER}" \
            -polaris-port "${POLARIS_GRPC_PORT}" \
            -namespace "${NAMESPACE}" \
            -role "${role}" \
            -service "${service}" \
            -version "${version}" \
            -port "${port}" \
            -detach \
            "$@"
    ) > "${LOG_DIR}/${log}" 2>&1 &
    local pid=$!
    if ! record_process "${pid}" "${role}" "${port}"; then
        stop_pid "${pid}"
        return 1
    fi
    printf '%s' "${pid}"
}

build_demo
ensure_namespace
ensure_service "${SERVICE_A}"
ensure_service "${SERVICE_B}"

debug_flags=()
if [[ "${DEBUG_MODE:-false}" == "true" ]]; then
    debug_flags=(-debug)
fi

AGENT_B_V1_PID="$(start_provider agent-b "${SERVICE_B}" v1.0.0 "${AGENT_B_V1_PORT}" agent-b-v1.log)"
AGENT_B_V2_PID="$(start_provider agent-b "${SERVICE_B}" v2.0.0 "${AGENT_B_V2_PORT}" agent-b-v2.log \
    -registration-port 2)"
wait_for_instances "${SERVICE_B}" 2 "${AGENT_B_V1_PID}"
process_alive "${AGENT_B_V2_PID}"

AGENT_A_PID="$(start_provider agent-a "${SERVICE_A}" v1.0.0 "${AGENT_A_PORT}" agent-a.log \
    -target-service "${SERVICE_B}" -target-versions v1.0.0,v2.0.0 "${debug_flags[@]+"${debug_flags[@]}"}")"
wait_for_instances "${SERVICE_A}" 1 "${AGENT_A_PID}"

(
    cd "${BUILD_DIR}"
    exec ./client \
        -server "${POLARIS_SERVER}" \
        -polaris-port "${POLARIS_GRPC_PORT}" \
        -namespace "${NAMESPACE}" \
        -service "${SERVICE_A}" \
        -target-versions v1.0.0,v2.0.0
) > "${LOG_DIR}/client.json" 2> "${LOG_DIR}/client.log"

result="$(cat "${LOG_DIR}/client.json")"
if ! jq -e '
    .success == true and
    (.response | contains("|fanout|")) and
    (.response | contains("b=v1.0.0:agent-b|v1.0.0|")) and
    (.response | contains("b=v2.0.0:agent-b|v2.0.0|"))
' >/dev/null <<<"${result}"; then
    echo "Agent A did not call both Agent B versions: ${result}" >&2
    exit 1
fi

trap - ERR
echo "[case 1.1 register two versioned Agent B instances] PASS"
echo "[case 1.2 registered Agent A calls both Agent B versions] PASS"
echo "verification complete; Agents remain running until cleanup.sh"
