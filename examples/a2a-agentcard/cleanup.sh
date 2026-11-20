#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../internal" && pwd)"
source "${COMMON_DIR}/e2e_common.sh"
CLEANUP_COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../internal/scripts" && pwd)"
source "${CLEANUP_COMMON_DIR}/cleanup_common.sh"

feature_common_init a2a-agentcard
SERVICE_A="trpc.polaris.demo.agent.a"
SERVICE_B="trpc.polaris.demo.agent.b"

usage() {
    cat <<'EOF'
Usage: ./examples/a2a-agentcard/cleanup.sh [options]
  -f, --force             apply without prompting
  --dry-run               show planned cleanup
  --keep-logs             keep .build/.logs
  --delete-service        also delete both demo services
  --service NAME          additional service to delete (default multi-interface mode)
  --polaris-server HOST   required with --delete-service
EOF
}

force=false
dry_run=false
keep_logs=false
delete_service=false
EXTRA_SERVICES=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--force) force=true; shift ;;
        --dry-run) dry_run=true; shift ;;
        --keep-logs|--processes-only) keep_logs=true; shift ;;
        --delete-service|--delete-services|--remote) delete_service=true; shift ;;
        --service=*) EXTRA_SERVICES+=("${1#*=}"); shift ;;
        --service) EXTRA_SERVICES+=("$2"); shift 2 ;;
        --polaris-server) POLARIS_SERVER="$2"; shift 2 ;;
        --polaris-grpc-port) POLARIS_GRPC_PORT="$2"; shift 2 ;;
        --polaris-http-port) POLARIS_HTTP_PORT="$2"; shift 2 ;;
        --polaris-token) POLARIS_TOKEN="$2"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
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

process_count=0
if [[ -f "${PID_FILE}" ]]; then
    process_count="$(wc -l < "${PID_FILE}" | tr -d ' ')"
fi
echo "cleanup feature=a2a-agentcard processes=${process_count} delete-service=${delete_service} keep-logs=${keep_logs}"

if [[ "${dry_run}" == "true" ]]; then
    if [[ -f "${PID_FILE}" ]]; then
        while IFS='|' read -r pid role port _ _; do
            echo "process: pid=${pid} role=${role} port=${port}"
        done < "${PID_FILE}"
    fi
    if [[ "${delete_service}" == "true" ]]; then
        echo "service: ${NAMESPACE}/${SERVICE_A}"
        echo "service: ${NAMESPACE}/${SERVICE_B}"
        for service in ${EXTRA_SERVICES[@]+"${EXTRA_SERVICES[@]}"}; do
            echo "service: ${NAMESPACE}/${service}"
        done
    fi
    echo "dry-run: nothing changed"
    exit 0
fi

if [[ "${force}" != "true" ]]; then
    if ! read -r -p "Apply cleanup? [y/N] " answer; then
        answer=""
    fi
    if [[ "${answer}" != "y" && "${answer}" != "Y" ]]; then
        echo "cleanup cancelled; use --force for noninteractive cleanup"
        exit 0
    fi
fi

stop_feature_processes
if [[ "${delete_service}" == "true" ]]; then
    delete_instances_and_service "${SERVICE_A}"
    delete_instances_and_service "${SERVICE_B}"
    if ((${#EXTRA_SERVICES[@]})); then
        cleanup_init a2a-agentcard-extra
        CLEANUP_NAMESPACE="${NAMESPACE}"
        CLEANUP_SERVER="${POLARIS_SERVER}"
        CLEANUP_HTTP_PORT="${POLARIS_HTTP_PORT}"
        CLEANUP_TOKEN="${POLARIS_CONSOLE_TOKEN:-${POLARIS_TOKEN}}"
        CLEANUP_DELETE=true
        CLEANUP_DRY_RUN=false
        CLEANUP_FORCE=true
        CLEANUP_SERVICE_OWNER="trpc-agent-naming-polarismesh-demo"
        if ! cleanup_state_ready; then
            exit 2
        fi
        for service in ${EXTRA_SERVICES[@]+"${EXTRA_SERVICES[@]}"}; do
            cleanup_delete_service "${service}" false
        done
    fi
fi
if [[ "${keep_logs}" != "true" ]]; then
    rm -rf "${BUILD_DIR}" "${LOG_DIR}"
fi
rm -f "${PID_FILE}"
