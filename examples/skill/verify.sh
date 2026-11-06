#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../internal" && pwd)"
source "${COMMON_DIR}/e2e_common.sh"

feature_common_init skill
feature_usage() {
    echo "Usage: ./examples/skill/verify.sh --polaris-server <host> [--debug]"
}
feature_parse_transport_args "$@"

SERVICE_NAME="trpc.polaris.demo.skill"
SKILL_NAME="trpc-agent-naming-demo-skill"
SKILL_VERSION="1.0.0"
PROVIDER_PORT="${PROVIDER_PORT:-28212}"

build_demo() {
    (
        cd "${REPO_ROOT}"
        env GOCACHE="${GOCACHE:-/private/tmp/go-agent-naming-demo-cache}" \
            go build -o "${BUILD_DIR}/provider" ./examples/skill/provider
        env GOCACHE="${GOCACHE:-/private/tmp/go-agent-naming-demo-cache}" \
            go build -o "${BUILD_DIR}/client" ./examples/skill/client
        env GOCACHE="${GOCACHE:-/private/tmp/go-agent-naming-demo-cache}" \
            go build -o "${BUILD_DIR}/publish" ./examples/skill/publish
    )
}

ensure_skill() {
    local response
    response="$("${BUILD_DIR}/publish" \
        -server "${POLARIS_SERVER}" \
        -console-port "${POLARIS_HTTP_PORT}" \
        -polaris-port "${POLARIS_GRPC_PORT}" \
        -skill-address "${POLARIS_SERVER}:${POLARIS_SKILL_GRPC_PORT}" \
        -namespace "${NAMESPACE}" \
        -skill "${SKILL_NAME}" \
        -skill-version "${SKILL_VERSION}" 2>&1)"
    echo "${response}"
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
            -skill-address "${POLARIS_SERVER}:${POLARIS_SKILL_GRPC_PORT}" \
            -namespace "${NAMESPACE}" \
            -service "${SERVICE_NAME}" \
            -skill "${SKILL_NAME}" \
            -skill-version "${SKILL_VERSION}" \
            -port "${PROVIDER_PORT}" \
            -skill-cache "${BUILD_DIR}/skill-cache" \
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
ensure_skill
echo "[case 2.1 publish real Skill] PASS"

provider_pid="$(start_provider)"
wait_for_instances "${SERVICE_NAME}" 1 "${provider_pid}"

(
    cd "${BUILD_DIR}"
    exec ./client \
        -server "${POLARIS_SERVER}" \
        -polaris-port "${POLARIS_GRPC_PORT}" \
        -namespace "${NAMESPACE}" \
        -service "${SERVICE_NAME}" \
        -skill "${SKILL_NAME}" \
        -skill-version "${SKILL_VERSION}"
) > "${LOG_DIR}/client.json" 2> "${LOG_DIR}/client.log"

result="$(cat "${LOG_DIR}/client.json")"
if ! jq -e --arg skill "${SKILL_NAME}" --arg version "${SKILL_VERSION}" '
    .success == true and
    (.response | contains("skill|" + $skill + "|")) and
    (.response | contains("POLARIS_DEMO_SKILL=" + $version))
' >/dev/null <<<"${result}"; then
    echo "Skill provider did not execute the installed Polaris Skill: ${result}" >&2
    exit 1
fi
echo "[case 2.2 install and execute real Skill] PASS"
echo "verification complete; Provider remains running until cleanup.sh"
