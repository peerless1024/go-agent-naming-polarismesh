#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../internal/scripts" && pwd)"
source "${COMMON_DIR}/cleanup_common.sh"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cleanup_init integration
CLEANUP_NAMESPACE="${POLARIS_E2E_NAMESPACE:-go-agent-naming-e2e}"
CLEANUP_SERVICE_OWNER="trpc-agent-naming-polarismesh-e2e"
CLEANUP_SKILL_OWNER="trpc-agent-naming-polarismesh-e2e"

RETAINED_DIR="${ROOT_DIR}/integration/.retained"

stop_retained_processes() {
    local manifest="${RETAINED_DIR}/${RUN_ID}/manifest.tsv"
    [[ -f "${manifest}" ]] || return 0
    local -a pids=()
    local label pid _log _test expected_start expected_command actual_start actual_command
    while IFS=$'\t' read -r label pid _log _test expected_start expected_command; do
        [[ "${label}" == "label" || -z "${pid}" ]] && continue
        local adopted=false
        if [[ -n "${label}" ]] && command -v launchctl >/dev/null 2>&1; then
            local launch_pid
            launch_pid="$(launchctl list 2>/dev/null | awk -v label="${label}" '$3 == label {print $1}')"
            if [[ -n "${launch_pid}" && "${launch_pid}" != "-" && "${launch_pid}" != "${pid}" ]]; then
                pid="${launch_pid}"
                adopted=true
            fi
        fi
        if ! kill -0 "${pid}" 2>/dev/null; then
            if [[ -n "${label}" ]] && command -v launchctl >/dev/null 2>&1; then
                launchctl remove "${label}" >/dev/null 2>&1 || true
            fi
            continue
        fi
        actual_start="$(ps -p "${pid}" -o lstart= 2>/dev/null | xargs || true)"
        actual_command="$(ps -p "${pid}" -o command= 2>/dev/null | xargs || true)"
        if [[ "${adopted}" == "true" ]]; then
            if [[ "${actual_command}" != *"${RUN_ID}"* ]] ||
                { [[ "${actual_command}" != *"-tags=integration"* ]] &&
                    [[ "${actual_command}" != *".test"* ]]; }; then
                echo "stale adopted launchctl record ignored: pid=${pid}" >&2
                continue
            fi
        elif [[ "${actual_start}" != "${expected_start}" ||
            "${actual_command}" != "${expected_command}" ]]; then
            echo "stale retained record ignored: pid=${pid}" >&2
            continue
        fi
        if [[ -n "${label}" ]] && command -v launchctl >/dev/null 2>&1; then
            echo "removing retained launchctl job: ${label}"
            launchctl remove "${label}" >/dev/null 2>&1 || true
        fi
        if ! kill -0 "${pid}" 2>/dev/null; then
            continue
        fi
        echo "stopping retained process: pid=${pid}"
        if kill -TERM "${pid}" 2>/dev/null; then
            pids+=("${pid}")
        fi
    done < "${manifest}"
    local _ attempt remaining
    for attempt in $(seq 1 40); do
        remaining=0
        for pid in ${pids[@]+"${pids[@]}"}; do
            if kill -0 "${pid}" 2>/dev/null; then
                remaining=1
                break
            fi
        done
        if ((remaining == 0)); then
            return 0
        fi
        sleep 0.25
    done
    for pid in ${pids[@]+"${pids[@]}"}; do
        if kill -0 "${pid}" 2>/dev/null; then
            echo "force-stopping retained process: pid=${pid}" >&2
            kill -KILL "${pid}" 2>/dev/null || true
        fi
    done
}

cleanup_before_delete() {
    stop_retained_processes
}

cleanup_usage() {
    cat <<'EOF'
Usage: ./integration/cleanup.sh [options]
  --run-id ID                delete resources named gan-e2e-<ID>-*
  --service NAME             additional service to delete
  --contract-service NAME    additional service whose contracts to delete
  --skill NAME               additional Skill to delete
  -f, --force                apply without prompting
  --dry-run                  query and print the planned cleanup
  --delete-service           required to delete remote resources
  --polaris-server HOST      required with --delete-service
  --polaris-http-port PORT   Console HTTP port (default 8090)
  --namespace NAME           integration namespace
  --owner NAME               expected Skill owner
  --polaris-token TOKEN      Console token

Normal integration tests clean up after themselves. Use this script only for
an abandoned run whose resources still contain the printed run ID. With
--run-id, retained test processes from run_retained.sh are stopped before
remote services and Skills are deleted.
EOF
}

declare -a REMAINING_ARGS=()
RUN_ID=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --run-id=*)
            RUN_ID="${1#*=}"
            shift
            ;;
        --run-id)
            RUN_ID="${2:-}"
            shift 2
            ;;
        *)
            REMAINING_ARGS+=("$1")
            shift
            ;;
    esac
done

if [[ -n "${RUN_ID}" ]]; then
    cleanup_add_service "gan-e2e-${RUN_ID}-a2a"
    cleanup_add_service "gan-e2e-${RUN_ID}-mcp" true
    cleanup_add_service "gan-e2e-${RUN_ID}-skill-agent"
    cleanup_add_service "gan-e2e-${RUN_ID}-agent-a"
    cleanup_add_service "gan-e2e-${RUN_ID}-agent-b"
    cleanup_add_service "gan-e2e-${RUN_ID}-agentcard-10"
    cleanup_add_service "gan-e2e-${RUN_ID}-agentcard-03"
    cleanup_add_service "gan-e2e-${RUN_ID}-agentcard-noserve"
    cleanup_add_skill "gan-e2e-${RUN_ID}-skill"
fi

if ((${#REMAINING_ARGS[@]})); then
    cleanup_parse_args "${REMAINING_ARGS[@]}"
else
    cleanup_parse_args
fi

if [[ -n "${RUN_ID}" && -f "${RETAINED_DIR}/${RUN_ID}/manifest.tsv" ]]; then
    if [[ "${CLEANUP_DRY_RUN}" == "true" ]]; then
        echo "retained processes from ${RUN_ID}:"
        while IFS=$'\t' read -r label pid log test _start _command; do
            [[ "${label}" == "label" || -z "${pid}" ]] && continue
            echo "  pid=${pid} test=${test} log=${log}"
        done < "${RETAINED_DIR}/${RUN_ID}/manifest.tsv"
    fi
fi

cleanup_run
