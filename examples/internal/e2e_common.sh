#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${COMMON_DIR}/../.." && pwd)"
DEMO_OWNER="trpc-agent-naming-polarismesh-demo"

feature_common_init() {
    FEATURE_NAME="$1"
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[1]}")" && pwd)"
    BUILD_DIR="${SCRIPT_DIR}/.build"
    LOG_DIR="${SCRIPT_DIR}/.logs"
    PID_FILE="${SCRIPT_DIR}/.${FEATURE_NAME}-pids"
    NAMESPACE="go-agent-naming-e2e"
    POLARIS_SERVER="${POLARIS_SERVER:-}"
    POLARIS_GRPC_PORT="${POLARIS_GRPC_PORT:-8091}"
    POLARIS_HTTP_PORT="${POLARIS_HTTP_PORT:-8090}"
    POLARIS_SKILL_GRPC_PORT="${POLARIS_SKILL_GRPC_PORT:-8094}"
    POLARIS_TOKEN="${POLARIS_TOKEN:-}"
}

ensure_runtime_dirs() {
    mkdir -p "${BUILD_DIR}" "${LOG_DIR}"
}

feature_set_console() {
    CONSOLE="http://${POLARIS_SERVER}:${POLARIS_HTTP_PORT}"
    api_headers=(-H "Content-Type: application/json")
    if [[ -n "${POLARIS_TOKEN}" ]]; then
        api_headers+=(-H "X-Polaris-Token: ${POLARIS_TOKEN}")
    fi
}

feature_parse_transport_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --polaris-server) POLARIS_SERVER="$2"; shift 2 ;;
            --polaris-grpc-port) POLARIS_GRPC_PORT="$2"; shift 2 ;;
            --polaris-http-port) POLARIS_HTTP_PORT="$2"; shift 2 ;;
            --polaris-skill-port) POLARIS_SKILL_GRPC_PORT="$2"; shift 2 ;;
            --polaris-token) POLARIS_TOKEN="$2"; shift 2 ;;
            --debug) DEBUG_MODE=true; shift ;;
            -h|--help) feature_usage; exit 0 ;;
            *) echo "unknown option: $1" >&2; exit 2 ;;
        esac
    done
    if [[ -z "${POLARIS_SERVER}" ]]; then
        echo "--polaris-server or POLARIS_SERVER is required" >&2
        exit 2
    fi
    ensure_runtime_dirs
    feature_set_console
}

api_request() {
    local method="$1"
    local path="$2"
    local payload="${3:-}"
    if [[ -n "${payload}" ]]; then
        curl -sS --max-time 20 -X "${method}" "${api_headers[@]}" \
            -d "${payload}" "${CONSOLE}${path}"
    else
        curl -sS --max-time 20 -X "${method}" "${api_headers[@]}" "${CONSOLE}${path}"
    fi
}

api_success() {
    jq -e \
        '.code == 200000 and ([.responses[]? | select(.code != 200000 and .code != 200002)] | length == 0)' \
        >/dev/null 2>&1 <<<"$1"
}

create_success() {
    jq -e \
        '(.code == 200000 or .code == 400201) and ([.responses[]? | select(.code != 200000 and .code != 200002 and .code != 400201)] | length == 0)' \
        >/dev/null 2>&1 <<<"$1"
}

ensure_namespace() {
    local response
    response="$(api_request POST /naming/v1/namespaces \
        "[{\"name\":\"${NAMESPACE}\",\"comment\":\"trpc-agent-naming-polarismesh examples\"}]")"
    create_success "${response}" || {
        echo "create namespace failed: ${response}" >&2
        return 1
    }
}

ensure_service() {
    local service="$1"
    local response
    response="$(api_request POST /naming/v1/services \
        "[{\"name\":\"${service}\",\"namespace\":\"${NAMESPACE}\",\"comment\":\"trpc-agent-naming-polarismesh demo\",\"metadata\":{\"examples.owner\":\"${DEMO_OWNER}\"}}]")"
    create_success "${response}" || {
        echo "create service ${service} failed: ${response}" >&2
        return 1
    }
    response="$(api_request GET "/naming/v1/services?namespace=${NAMESPACE}&name=${service}")"
    if ! jq -e --arg service "${service}" --arg owner "${DEMO_OWNER}" '
        [.services[]? | select(.name == $service and .metadata["examples.owner"] == $owner)] | length == 1
    ' >/dev/null 2>&1 <<<"${response}"; then
        echo "refuse to use unowned service ${NAMESPACE}/${service}: ${response}" >&2
        return 1
    fi
}

wait_for_instances() {
    local service="$1"
    local expected="$2"
    local pid="${3:-}"
    local response count
    for _ in $(seq 1 80); do
        if [[ -n "${pid}" ]] && ! process_alive "${pid}"; then
            echo "Provider exited before registering ${service}; inspect ${LOG_DIR}/provider.log" >&2
            return 1
        fi
        response="$(api_request GET "/naming/v1/instances?namespace=${NAMESPACE}&service=${service}")"
        count="$(jq '[.instances[]? | select(. != null)] | length' <<<"${response}" 2>/dev/null || echo 0)"
        if [[ "${count}" -ge "${expected}" ]]; then
            return 0
        fi
        sleep 0.25
    done
    echo "timed out waiting for ${expected} instances of ${service}: ${response}" >&2
    return 1
}

stop_pid() {
    local pid="$1"
    if [[ -z "${pid}" ]]; then
        return 1
    fi
    if ! kill -0 "${pid}" 2>/dev/null; then
        return 0
    fi
    if ! kill -TERM "${pid}" 2>/dev/null; then
        if ! kill -0 "${pid}" 2>/dev/null; then
            return 0
        fi
        echo "cannot send SIGTERM to process: pid=${pid}" >&2
        return 1
    fi
    for _ in $(seq 1 40); do
        if ! kill -0 "${pid}" 2>/dev/null; then
            return 0
        fi
        sleep 0.25
    done
    if ! kill -KILL "${pid}" 2>/dev/null; then
        if ! kill -0 "${pid}" 2>/dev/null; then
            return 0
        fi
        echo "cannot send SIGKILL to process: pid=${pid}" >&2
        return 1
    fi
    for _ in $(seq 1 20); do
        if ! kill -0 "${pid}" 2>/dev/null; then
            return 0
        fi
        sleep 0.25
    done
    echo "process remains alive after SIGKILL: pid=${pid}" >&2
    return 1
}

process_alive() {
    local pid="$1"
    if kill -0 "${pid}" 2>/dev/null; then
        return 0
    fi
    return 1
}

stop_feature_processes() {
    [[ -f "${PID_FILE}" ]] || return 0
    local failed=false
    while IFS='|' read -r pid _ _ expected_start expected_command; do
        [[ -z "${pid}" ]] && continue
        if [[ ! "${pid}" =~ ^[0-9]+$ || -z "${expected_start}" || -z "${expected_command}" ]]; then
            echo "invalid process record in ${PID_FILE}" >&2
            failed=true
            break
        fi
        if ! process_alive "${pid}"; then
            continue
        fi
        if ! actual_start="$(ps -p "${pid}" -o lstart= 2>/dev/null | xargs)"; then
            if ! process_alive "${pid}"; then
                continue
            fi
            echo "cannot verify process start time: pid=${pid}" >&2
            failed=true
            break
        fi
        if ! actual_command="$(ps -p "${pid}" -o command= 2>/dev/null | xargs)"; then
            if ! process_alive "${pid}"; then
                continue
            fi
            echo "cannot verify process command: pid=${pid}" >&2
            failed=true
            break
        fi
        if [[ "${actual_start}" != "${expected_start}" || "${actual_command}" != "${expected_command}" ]]; then
            echo "stale process record ignored: pid=${pid}" >&2
            continue
        fi
        if ! stop_pid "${pid}"; then
            failed=true
            break
        fi
    done < "${PID_FILE}"
    if [[ "${failed}" == "true" ]]; then
        return 1
    fi
    rm -f "${PID_FILE}"
}

record_process() {
    local pid="$1"
    local role="$2"
    local port="$3"
    local start command
    start="$(ps -p "${pid}" -o lstart= 2>/dev/null | xargs || true)"
    command="$(ps -p "${pid}" -o command= 2>/dev/null | xargs || true)"
    if [[ -z "${start}" || -z "${command}" ]]; then
        echo "cannot record process identity: pid=${pid}" >&2
        return 1
    fi
    printf '%s|%s|%s|%s|%s\n' "${pid}" "${role}" "${port}" "${start}" "${command}" >> "${PID_FILE}"
}

delete_instances_and_service() {
    local service="$1"
    local response instance_id delete_response
    if ! response="$(api_request GET "/naming/v1/services?namespace=${NAMESPACE}&name=${service}")"; then
        echo "query service ${NAMESPACE}/${service} failed" >&2
        return 1
    fi
    if ! jq -e '.code == 200000' >/dev/null 2>&1 <<<"${response}"; then
        echo "query service ${NAMESPACE}/${service} failed: ${response}" >&2
        return 1
    fi
    local service_count
    service_count="$(jq --arg service "${service}" '[.services[]? | select(.name == $service)] | length' <<<"${response}" 2>/dev/null || echo 0)"
    if [[ "${service_count}" -eq 0 ]]; then
        return 0
    fi
    if ! jq -e --arg service "${service}" --arg owner "${DEMO_OWNER}" '
        [.services[]? | select(.name == $service and .metadata["examples.owner"] == $owner)] | length == 1
    ' >/dev/null 2>&1 <<<"${response}"; then
        echo "refuse to delete unowned service ${NAMESPACE}/${service}: ${response}" >&2
        return 1
    fi
    if ! response="$(api_request GET "/naming/v1/instances?namespace=${NAMESPACE}&service=${service}")"; then
        echo "query instances ${NAMESPACE}/${service} failed" >&2
        return 1
    fi
    if ! jq -e '.code == 200000' >/dev/null 2>&1 <<<"${response}"; then
        echo "query instances ${NAMESPACE}/${service} failed: ${response}" >&2
        return 1
    fi
    if ! jq -e --arg owner "${DEMO_OWNER}" '
        all(.instances[]?; .metadata["examples.owner"] == $owner)
    ' >/dev/null 2>&1 <<<"${response}"; then
        echo "refuse to delete service with unowned instances ${NAMESPACE}/${service}: ${response}" >&2
        return 1
    fi
    while IFS= read -r instance_id; do
        [[ -n "${instance_id}" ]] || continue
        if ! delete_response="$(api_request POST /naming/v1/instances/delete \
            "[{\"id\":\"${instance_id}\",\"namespace\":\"${NAMESPACE}\",\"service\":\"${service}\"}]")"; then
            echo "delete instance ${instance_id} transport failed" >&2
            return 1
        fi
        if ! api_success "${delete_response}"; then
            echo "delete instance ${instance_id} failed: ${delete_response}" >&2
            return 1
        fi
    done < <(jq -r '.instances[]?.id // empty' <<<"${response}" 2>/dev/null || true)
    for _ in $(seq 1 10); do
        if ! response="$(api_request POST /naming/v1/services/delete \
            "[{\"name\":\"${service}\",\"namespace\":\"${NAMESPACE}\"}]")"; then
            sleep 0.5
            continue
        fi
        if api_success "${response}"; then
            return 0
        fi
        sleep 0.5
    done
    echo "delete service ${service} failed: ${response}" >&2
    return 1
}

delete_owned_skill() {
    local skill_name="$1"
    local response delete_response
    if ! response="$(api_request GET "/skill/v1/skills?namespace=${NAMESPACE}&name=${skill_name}")"; then
        echo "query Skill ${NAMESPACE}/${skill_name} failed" >&2
        return 1
    fi
    if jq -e '.code == 400202' >/dev/null 2>&1 <<<"${response}"; then
        return 0
    fi
    if ! jq -e --arg owner "${DEMO_OWNER}" '
        .code == 200000 and .resource != null and .resource.ext["examples.owner"] == $owner
    ' >/dev/null 2>&1 <<<"${response}"; then
        echo "refuse to delete unowned or unreadable Skill ${NAMESPACE}/${skill_name}: ${response}" >&2
        return 1
    fi
    if ! delete_response="$(api_request DELETE "/skill/v1/skills?namespace=${NAMESPACE}&name=${skill_name}")"; then
        echo "delete Skill ${NAMESPACE}/${skill_name} transport failed" >&2
        return 1
    fi
    if ! api_success "${delete_response}"; then
        echo "delete Skill ${NAMESPACE}/${skill_name} failed: ${delete_response}" >&2
        return 1
    fi
}

feature_cleanup() {
    local service="$1"
    local skill_name="${2:-}"
    shift 2
    local force=false
    local dry_run=false
    local keep_logs=false
    local delete_service=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--force) force=true; shift ;;
            --dry-run) dry_run=true; shift ;;
            --keep-logs|--processes-only) keep_logs=true; shift ;;
            --delete-service|--delete-services|--remote) delete_service=true; shift ;;
            --polaris-server) POLARIS_SERVER="$2"; shift 2 ;;
            --polaris-grpc-port) POLARIS_GRPC_PORT="$2"; shift 2 ;;
            --polaris-http-port) POLARIS_HTTP_PORT="$2"; shift 2 ;;
            --polaris-skill-port) POLARIS_SKILL_GRPC_PORT="$2"; shift 2 ;;
            --polaris-token) POLARIS_TOKEN="$2"; shift 2 ;;
            -h|--help) feature_usage; exit 0 ;;
            *) echo "unknown cleanup option: $1" >&2; exit 2 ;;
        esac
    done
    if [[ "${delete_service}" == "true" ]]; then
        if [[ -z "${POLARIS_SERVER}" ]]; then
            echo "--delete-service requires --polaris-server or POLARIS_SERVER" >&2
            exit 2
        fi
        feature_set_console
    fi

    local process_count=0
    if [[ -f "${PID_FILE}" ]]; then
        process_count="$(wc -l < "${PID_FILE}" | tr -d ' ')"
    fi
    echo "cleanup feature=${FEATURE_NAME} processes=${process_count} delete-service=${delete_service} keep-logs=${keep_logs}"

    if [[ "${dry_run}" == "true" ]]; then
        if [[ -f "${PID_FILE}" ]]; then
            while IFS='|' read -r pid role port _ _; do
                echo "process: pid=${pid} role=${role} port=${port}"
            done < "${PID_FILE}"
        fi
        if [[ "${delete_service}" == "true" ]]; then
            echo "service: ${NAMESPACE}/${service}"
            if [[ -n "${skill_name}" ]]; then
                echo "skill: ${NAMESPACE}/${skill_name}"
            fi
        fi
        echo "dry-run: nothing changed"
        return 0
    fi
    if [[ "${force}" != "true" ]]; then
        if ! read -r -p "Apply cleanup? [y/N] " answer; then
            answer=""
        fi
        if [[ "${answer}" != "y" && "${answer}" != "Y" ]]; then
            echo "cleanup cancelled; use --force for noninteractive cleanup"
            return 0
        fi
    fi

    if ! stop_feature_processes; then
        return 1
    fi
    if [[ "${delete_service}" == "true" ]]; then
        if [[ -n "${skill_name}" ]]; then
            if ! delete_owned_skill "${skill_name}"; then
                return 1
            fi
        fi
        delete_instances_and_service "${service}"
    fi
    if [[ "${keep_logs}" != "true" ]]; then
        rm -rf "${BUILD_DIR}" "${LOG_DIR}"
    fi
    rm -f "${PID_FILE}"
}
