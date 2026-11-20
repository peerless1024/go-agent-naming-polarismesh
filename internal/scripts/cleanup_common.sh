#!/bin/bash

# Shared Console cleanup helpers for runnable examples and integration runs.
# The caller defines cleanup_usage, then calls cleanup_parse_args and
# cleanup_run. Deletion requires --delete-service.

cleanup_init() {
    CLEANUP_FEATURE="$1"
    CLEANUP_NAMESPACE="${POLARIS_EXAMPLE_NAMESPACE:-${POLARIS_E2E_NAMESPACE:-go-agent-naming-examples}}"
    CLEANUP_SERVER="${POLARIS_SERVER:-}"
    CLEANUP_HTTP_PORT="${POLARIS_HTTP_PORT:-8090}"
    CLEANUP_TOKEN="${POLARIS_CONSOLE_TOKEN:-${POLARIS_TOKEN:-}}"
    CLEANUP_DRY_RUN=false
    CLEANUP_FORCE=false
    CLEANUP_DELETE=false
    CLEANUP_SERVICE_OWNER="${CLEANUP_SERVICE_OWNER:-trpc-agent-naming-polarismesh-examples}"
    CLEANUP_SKILL_OWNER="${CLEANUP_SKILL_OWNER:-trpc-agent-naming-polarismesh-examples}"
    CLEANUP_SERVICES=()
    CLEANUP_SERVICE_CONTRACTS=()
    CLEANUP_SKILLS=()
}

cleanup_add_service() {
    CLEANUP_SERVICES+=("$1")
    if [[ "${2:-false}" == "true" ]]; then
        CLEANUP_SERVICE_CONTRACTS+=("$1")
    fi
}

cleanup_add_skill() {
    CLEANUP_SKILLS+=("$1")
}

cleanup_parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --namespace=*)
                CLEANUP_NAMESPACE="${1#*=}"
                shift
                ;;
            --namespace)
                CLEANUP_NAMESPACE="${2:-}"
                shift 2
                ;;
            --polaris-server=*)
                CLEANUP_SERVER="${1#*=}"
                shift
                ;;
            --polaris-server)
                CLEANUP_SERVER="${2:-}"
                shift 2
                ;;
            --polaris-http-port=*)
                CLEANUP_HTTP_PORT="${1#*=}"
                shift
                ;;
            --polaris-http-port)
                CLEANUP_HTTP_PORT="${2:-}"
                shift 2
                ;;
            --polaris-token=*)
                CLEANUP_TOKEN="${1#*=}"
                shift
                ;;
            --polaris-token)
                CLEANUP_TOKEN="${2:-}"
                shift 2
                ;;
            --service=*)
                cleanup_add_service "${1#*=}"
                shift
                ;;
            --service)
                cleanup_add_service "${2:-}"
                shift 2
                ;;
            --contract-service=*)
                cleanup_add_service "${1#*=}" true
                shift
                ;;
            --contract-service)
                cleanup_add_service "${2:-}" true
                shift 2
                ;;
            --skill=*)
                cleanup_add_skill "${1#*=}"
                shift
                ;;
            --skill)
                cleanup_add_skill "${2:-}"
                shift 2
                ;;
            --owner=*)
                CLEANUP_SKILL_OWNER="${1#*=}"
                shift
                ;;
            --owner)
                CLEANUP_SKILL_OWNER="${2:-}"
                shift 2
                ;;
            --dry-run)
                CLEANUP_DRY_RUN=true
                shift
                ;;
            -f|--force)
                CLEANUP_FORCE=true
                shift
                ;;
            --delete-service|--delete-services|--remote)
                CLEANUP_DELETE=true
                shift
                ;;
            -h|--help)
                cleanup_usage
                exit 0
                ;;
            *)
                echo "unknown cleanup option: $1" >&2
                cleanup_usage >&2
                exit 2
                ;;
        esac
    done
}

cleanup_state_ready() {
    if [[ -z "${CLEANUP_NAMESPACE}" ]]; then
        echo "namespace is required" >&2
        return 1
    fi
    if [[ "${CLEANUP_DELETE}" != "true" ]]; then
        return 0
    fi
    if [[ -z "${CLEANUP_SERVER}" ]]; then
        echo "--delete-service requires --polaris-server or POLARIS_SERVER" >&2
        return 1
    fi
    if ! command -v curl >/dev/null 2>&1 || ! command -v jq >/dev/null 2>&1; then
        echo "curl and jq are required for remote cleanup" >&2
        return 1
    fi
    if ! [[ "${CLEANUP_HTTP_PORT}" =~ ^[0-9]+$ ]] ||
        ((CLEANUP_HTTP_PORT < 1 || CLEANUP_HTTP_PORT > 65535)); then
        echo "invalid Console HTTP port: ${CLEANUP_HTTP_PORT}" >&2
        return 1
    fi
    CLEANUP_CONSOLE="http://${CLEANUP_SERVER}:${CLEANUP_HTTP_PORT}"
}

cleanup_run() {
    if ! cleanup_state_ready; then
        exit 2
    fi

    local service_count="${#CLEANUP_SERVICES[@]}"
    local skill_count="${#CLEANUP_SKILLS[@]}"
    echo "cleanup feature=${CLEANUP_FEATURE} namespace=${CLEANUP_NAMESPACE} services=${service_count} skills=${skill_count} delete-service=${CLEANUP_DELETE} dry-run=${CLEANUP_DRY_RUN}"

    if [[ "${CLEANUP_DELETE}" != "true" ]]; then
        echo "no remote resources requested; add --delete-service to remove services or Skills"
        return 0
    fi
    if [[ "${CLEANUP_DRY_RUN}" != "true" && "${CLEANUP_FORCE}" != "true" ]]; then
        local answer
        if ! read -r -p "Apply cleanup? [y/N] " answer; then
            answer=""
        fi
        if [[ "${answer}" != "y" && "${answer}" != "Y" ]]; then
            echo "cleanup cancelled; use --force for noninteractive cleanup"
            return 0
        fi
    fi
    if declare -F cleanup_before_delete >/dev/null 2>&1; then
        if ! cleanup_before_delete; then
            return 1
        fi
    fi

    local service
    for service in ${CLEANUP_SERVICES[@]+"${CLEANUP_SERVICES[@]}"}; do
        local contracts=false
        local candidate
        for candidate in ${CLEANUP_SERVICE_CONTRACTS[@]+"${CLEANUP_SERVICE_CONTRACTS[@]}"}; do
            if [[ "${candidate}" == "${service}" ]]; then
                contracts=true
                break
            fi
        done
        cleanup_delete_service "${service}" "${contracts}" || return 1
    done
    local skill
    for skill in ${CLEANUP_SKILLS[@]+"${CLEANUP_SKILLS[@]}"}; do
        cleanup_delete_skill "${skill}" || return 1
    done
}

cleanup_curl() {
    local method="$1"
    local path="$2"
    local payload="${3:-}"
    local args=(-sS --max-time 20 -X "${method}" -H "Content-Type: application/json")
    if [[ -n "${CLEANUP_TOKEN}" ]]; then
        args+=(-H "X-Polaris-Token: ${CLEANUP_TOKEN}")
    fi
    if [[ -n "${payload}" ]]; then
        args+=(-d "${payload}")
    fi
    curl "${args[@]}" "${CLEANUP_CONSOLE}${path}"
}

cleanup_get() {
    local path="$1"
    shift
    local args=(-sS --max-time 20 -G)
    if [[ -n "${CLEANUP_TOKEN}" ]]; then
        args+=(-H "X-Polaris-Token: ${CLEANUP_TOKEN}")
    fi
    while [[ $# -gt 0 ]]; do
        args+=(--data-urlencode "$1")
        shift
    done
    curl "${args[@]}" "${CLEANUP_CONSOLE}${path}"
}

cleanup_delete_service() {
    local service="$1"
    local delete_contracts="${2:-false}"
    local response

    if ! response="$(cleanup_get /naming/v1/services "namespace=${CLEANUP_NAMESPACE}" "name=${service}")"; then
        echo "query service ${CLEANUP_NAMESPACE}/${service} failed" >&2
        return 1
    fi
    if ! jq -e '.code == 200000' >/dev/null 2>&1 <<<"${response}"; then
        echo "query service ${CLEANUP_NAMESPACE}/${service} failed: ${response}" >&2
        return 1
    fi
    if jq -e --arg service "${service}" '
        [.services[]? | select(.name == $service)] | length == 0
    ' >/dev/null 2>&1 <<<"${response}"; then
        echo "service already absent: ${CLEANUP_NAMESPACE}/${service}"
        return 0
    fi
    if [[ -n "${CLEANUP_SERVICE_OWNER}" ]] && ! jq -e \
        --arg service "${service}" \
        --arg owner "${CLEANUP_SERVICE_OWNER}" '
        [.services[]? | select(
            .name == $service and
            ((.metadata["examples.owner"] // "") == "" or
             .metadata["examples.owner"] == $owner)
        )] | length == 1
    ' >/dev/null 2>&1 <<<"${response}"; then
        echo "refuse to delete unowned service ${CLEANUP_NAMESPACE}/${service}: ${response}" >&2
        return 1
    fi

    local instances
    if ! instances="$(cleanup_get /naming/v1/instances "namespace=${CLEANUP_NAMESPACE}" "service=${service}")"; then
        echo "query instances ${CLEANUP_NAMESPACE}/${service} failed" >&2
        return 1
    fi
    if ! jq -e '.code == 200000' >/dev/null 2>&1 <<<"${instances}"; then
        echo "query instances ${CLEANUP_NAMESPACE}/${service} failed: ${instances}" >&2
        return 1
    fi
    if ! jq -e --arg service "${service}" '
        all(.instances[]?;
            .metadata["ai-agent-name"] == $service or
            .metadata["mcp-server-name"] == $service)
    ' >/dev/null 2>&1 <<<"${instances}"; then
        echo "refuse to delete service with foreign instances ${CLEANUP_NAMESPACE}/${service}: ${instances}" >&2
        return 1
    fi

    local ids
    ids="$(jq -r '.instances[]?.id // empty' <<<"${instances}")"
    if [[ "${CLEANUP_DRY_RUN}" == "true" ]]; then
        local id
        while IFS= read -r id; do
            [[ -n "${id}" ]] && echo "instance: ${CLEANUP_NAMESPACE}/${service}/${id}"
        done <<<"${ids}"
        echo "service: ${CLEANUP_NAMESPACE}/${service}"
        [[ "${delete_contracts}" == "true" ]] && echo "service-contracts: ${CLEANUP_NAMESPACE}/${service}"
        echo "dry-run: nothing changed"
        return 0
    fi

    local id
    while IFS= read -r id; do
        [[ -n "${id}" ]] || continue
        local payload
        payload="$(jq -cn --arg id "${id}" --arg ns "${CLEANUP_NAMESPACE}" --arg service "${service}" \
            '[{id:$id,namespace:$ns,service:$service}]')"
        if ! response="$(cleanup_curl POST /naming/v1/instances/delete "${payload}")"; then
            echo "delete instance ${id} transport failed" >&2
            return 1
        fi
        if ! jq -e '.code == 200000 or .code == 200002 or .code == 400202' >/dev/null 2>&1 <<<"${response}"; then
            echo "delete instance ${id} failed: ${response}" >&2
            return 1
        fi
    done <<<"${ids}"

    if [[ "${delete_contracts}" == "true" ]]; then
        cleanup_delete_contracts "${service}" || return 1
    fi

    local payload
    payload="$(jq -cn --arg name "${service}" --arg ns "${CLEANUP_NAMESPACE}" \
        '[{name:$name,namespace:$ns}]')"
    local attempt
    for attempt in $(seq 1 10); do
        if response="$(cleanup_curl POST /naming/v1/services/delete "${payload}")" &&
            jq -e '.code == 200000 or .code == 200002 or .code == 400202' >/dev/null 2>&1 <<<"${response}"; then
            echo "deleted service: ${CLEANUP_NAMESPACE}/${service}"
            return 0
        fi
        sleep 0.5
    done
    echo "delete service ${CLEANUP_NAMESPACE}/${service} failed: ${response}" >&2
    return 1
}

cleanup_delete_contracts() {
    local service="$1"
    local response
    if ! response="$(cleanup_get /naming/v1/service/contracts "namespace=${CLEANUP_NAMESPACE}" "service=${service}")"; then
        echo "query Service Contracts ${CLEANUP_NAMESPACE}/${service} failed" >&2
        return 1
    fi
    if ! jq -e '.code == 200000' >/dev/null 2>&1 <<<"${response}"; then
        echo "query Service Contracts ${CLEANUP_NAMESPACE}/${service} failed: ${response}" >&2
        return 1
    fi
    local contracts
    contracts="$(jq -c --arg ns "${CLEANUP_NAMESPACE}" --arg service "${service}" '
        [.data[]? | select(
            .namespace == $ns and .service == $service and (.id // "") != ""
        ) | {
            id, name, namespace, service, protocol, version, type
        }]
    ' <<<"${response}")"
    if [[ "${contracts}" == "[]" ]]; then
        return 0
    fi
    if [[ "${CLEANUP_DRY_RUN}" == "true" ]]; then
        echo "service-contracts: ${contracts}"
        return 0
    fi
    if ! response="$(cleanup_curl POST /naming/v1/service/contracts/delete "${contracts}")"; then
        echo "delete Service Contracts ${CLEANUP_NAMESPACE}/${service} transport failed" >&2
        return 1
    fi
    if ! jq -e '.code == 200000 or .code == 200002 or .code == 400202' >/dev/null 2>&1 <<<"${response}"; then
        echo "delete Service Contracts ${CLEANUP_NAMESPACE}/${service} failed: ${response}" >&2
        return 1
    fi
}

cleanup_delete_skill() {
    local skill="$1"
    local response
    if ! response="$(cleanup_get /skill/v1/skills "namespace=${CLEANUP_NAMESPACE}" "name=${skill}")"; then
        echo "query Skill ${CLEANUP_NAMESPACE}/${skill} failed" >&2
        return 1
    fi
    if jq -e '.code == 400202' >/dev/null 2>&1 <<<"${response}"; then
        echo "Skill already absent: ${CLEANUP_NAMESPACE}/${skill}"
        return 0
    fi
    if ! jq -e '.code == 200000' >/dev/null 2>&1 <<<"${response}"; then
        echo "query Skill ${CLEANUP_NAMESPACE}/${skill} failed: ${response}" >&2
        return 1
    fi
    if [[ -n "${CLEANUP_SKILL_OWNER}" ]] && ! jq -e \
        --arg owner "${CLEANUP_SKILL_OWNER}" '
        .resource != null and
        .resource.ext["examples.owner"] == $owner
    ' >/dev/null 2>&1 <<<"${response}"; then
        echo "refuse to delete unowned Skill ${CLEANUP_NAMESPACE}/${skill}: ${response}" >&2
        return 1
    fi
    if [[ "${CLEANUP_DRY_RUN}" == "true" ]]; then
        echo "skill: ${CLEANUP_NAMESPACE}/${skill}"
        echo "dry-run: nothing changed"
        return 0
    fi
    local args=(
        -sS --max-time 20 -X DELETE
        -H "Content-Type: application/json"
        -G
        --data-urlencode "namespace=${CLEANUP_NAMESPACE}"
        --data-urlencode "name=${skill}"
    )
    if [[ -n "${CLEANUP_TOKEN}" ]]; then
        args+=(-H "X-Polaris-Token: ${CLEANUP_TOKEN}")
    fi
    if ! response="$(curl "${args[@]}" "${CLEANUP_CONSOLE}/skill/v1/skills")"; then
        echo "delete Skill ${CLEANUP_NAMESPACE}/${skill} transport failed" >&2
        return 1
    fi
    if ! jq -e '.code == 200000 or .code == 200002 or .code == 400202' >/dev/null 2>&1 <<<"${response}"; then
        echo "delete Skill ${CLEANUP_NAMESPACE}/${skill} failed: ${response}" >&2
        return 1
    fi
    echo "deleted Skill: ${CLEANUP_NAMESPACE}/${skill}"
}
