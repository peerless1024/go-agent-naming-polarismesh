#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER="${POLARIS_SERVER:-}"
RUN_ID="${POLARIS_E2E_RUN_ID:-}"
NAMESPACE="${POLARIS_E2E_NAMESPACE:-go-agent-naming-e2e}"
SUITE_FILTER="${POLARIS_E2E_SUITES:-}"
START_TIMEOUT="${POLARIS_E2E_START_TIMEOUT:-240}"
GO_BIN="${GO_BIN:-go}"

usage() {
    cat <<'EOF'
Usage:
  POLARIS_SERVER=host bash integration/run_retained.sh [options]

Options:
  --server=host          Polaris host (or POLARIS_SERVER)
  --run-id=id            Shared run ID for all retained services
  --namespace=name       Polaris namespace (default go-agent-naming-e2e)
  --suite=list           Comma-separated suites to start:
                         a2a,mcp,skill,a2a-agentcard,agentcard-10,agentcard-03,agentcard-noserve
  --start-timeout=secs   Wait for each suite to print its KEEP marker
  -h, --help             Show this help

Each test runs in a detached nohup process. After assertions pass, the test
blocks (POLARIS_E2E_KEEP=1), keeping in-process servers, child providers,
service instances, and Skills alive. This script never cleans up.
Use integration/cleanup.sh --run-id=<id> --delete-service --force later.
EOF
}

for arg in "$@"; do
    case "${arg}" in
        --server=*) SERVER="${arg#*=}" ;;
        --run-id=*) RUN_ID="${arg#*=}" ;;
        --namespace=*) NAMESPACE="${arg#*=}" ;;
        --suite=*) SUITE_FILTER="${arg#*=}" ;;
        --start-timeout=*) START_TIMEOUT="${arg#*=}" ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: ${arg}" >&2; usage >&2; exit 2 ;;
    esac
done

if [[ -z "${SERVER}" ]]; then
    echo "POLARIS_SERVER or --server is required" >&2
    exit 2
fi
if [[ "${GO_BIN}" != /* ]]; then
    GO_BIN="$(command -v "${GO_BIN}" 2>/dev/null || true)"
fi
if [[ -z "${GO_BIN}" || ! -x "${GO_BIN}" ]]; then
    echo "Go binary was not found; set GO_BIN" >&2
    exit 2
fi
if [[ -z "${RUN_ID}" ]]; then
    RUN_ID="$(date +%Y%m%d-%H%M%S)-$$"
fi
if ! [[ "${RUN_ID}" =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "run ID may only contain letters, digits, '.', '_' and '-'" >&2
    exit 2
fi
if ! [[ "${START_TIMEOUT}" =~ ^[0-9]+$ ]] || ((START_TIMEOUT < 10)); then
    echo "start timeout must be at least 10 seconds" >&2
    exit 2
fi

RET_DIR="${ROOT_DIR}/integration/.retained/${RUN_ID}"
ENV_FILE="${RET_DIR}/env"
MANIFEST="${RET_DIR}/manifest.tsv"
mkdir -p "${RET_DIR}"
chmod 700 "${RET_DIR}"
umask 077

write_env() {
    printf 'export %s=%q\n' "$1" "$2" >> "${ENV_FILE}"
}

: > "${ENV_FILE}"
write_env POLARIS_SERVER "${SERVER}"
write_env POLARIS_GRPC_PORT "${POLARIS_GRPC_PORT:-8091}"
write_env POLARIS_HTTP_PORT "${POLARIS_HTTP_PORT:-8090}"
write_env POLARIS_SKILL_GRPC_PORT "${POLARIS_SKILL_GRPC_PORT:-8094}"
write_env POLARIS_TOKEN "${POLARIS_TOKEN:-}"
write_env POLARIS_CONSOLE_TOKEN "${POLARIS_CONSOLE_TOKEN:-}"
write_env POLARIS_SKILL_TOKEN "${POLARIS_SKILL_TOKEN:-}"
write_env POLARIS_SERVICE_TOKEN "${POLARIS_SERVICE_TOKEN:-}"
write_env POLARIS_E2E_NAMESPACE "${NAMESPACE}"
write_env POLARIS_E2E_RUN_ID "${RUN_ID}"
write_env POLARIS_E2E_TIMEOUT "${POLARIS_E2E_TIMEOUT:-30s}"
write_env POLARIS_E2E_KEEP "1"
write_env GOCACHE "${GOCACHE:-/tmp/go-agent-naming-retained-cache}"
chmod 600 "${ENV_FILE}"

if [[ ! -f "${MANIFEST}" ]]; then
    printf 'label\tpid\tlog\ttest\tstart\tcommand\n' > "${MANIFEST}"
fi

should_start() {
    local suite="$1"
    if [[ -z "${SUITE_FILTER}" ]]; then
        return 0
    fi
    case ",${SUITE_FILTER}," in
        *",${suite},"*) return 0 ;;
        *) return 1 ;;
    esac
}

start_process() {
    local name="$1"
    local log="$2"
    shift 2
    if ! should_start "${name}"; then
        return 0
    fi
    local ready="${log}.ready"
    rm -f "${ready}"
    local quoted_args=""
    local arg
    for arg in "$@"; do
        quoted_args="${quoted_args} $(printf '%q' "${arg}")"
    done
    local command
    command="set -a; source '${ENV_FILE}'; set +a; export POLARIS_E2E_KEEP_READY_FILE='${ready}'; cd '${ROOT_DIR}'; $(printf '%q' "${GO_BIN}") test -mod=readonly -tags=integration -count=1 -v -timeout=0${quoted_args} > '${log}' 2>&1 & child=\$!; trap 'kill -TERM \$child 2>/dev/null; wait \$child; exit 0' TERM INT HUP; wait \$child; exit 0"
    local label=""
    local pid=""
    if [[ "$(uname -s)" == "Darwin" ]] && command -v launchctl >/dev/null 2>&1; then
        label="com.trpc.gan.retain.${RUN_ID}.${name}"
        local existing_pid
        existing_pid="$(launchctl list 2>/dev/null | awk -v label="${label}" '$3 == label {print $1}')"
        if [[ -n "${existing_pid}" && "${existing_pid}" != "-" ]]; then
            echo "launchctl job ${label} is already running (pid=${existing_pid})" >&2
            return 1
        fi
        if [[ "${existing_pid}" == "-" ]]; then
            launchctl remove "${label}" >/dev/null 2>&1 || true
        fi
        if ! launchctl submit -l "${label}" -- /bin/bash -lc "${command}" >/dev/null 2>&1; then
            echo "launchctl submit failed for ${name}" >&2
            return 1
        fi
        local attempt
        for attempt in $(seq 1 40); do
            pid="$(launchctl list 2>/dev/null | awk -v label="${label}" '$3 == label {print $1}')"
            [[ -n "${pid}" && "${pid}" != "-" ]] && break
            sleep 0.25
        done
        if [[ -z "${pid}" || "${pid}" == "-" ]]; then
            echo "launchctl job ${label} did not start" >&2
            launchctl remove "${label}" >/dev/null 2>&1 || true
            tail -40 "${log}" >&2 || true
            return 1
        fi
    else
        nohup bash -lc "${command}" </dev/null >/dev/null 2>&1 &
        pid=$!
    fi
    sleep 0.3
    if ! kill -0 "${pid}" 2>/dev/null; then
        echo "${name} exited during startup" >&2
        tail -40 "${log}" >&2 || true
        return 1
    fi
    local start command_line
    start="$(ps -p "${pid}" -o lstart= 2>/dev/null | xargs || true)"
    command_line="$(ps -p "${pid}" -o command= 2>/dev/null | xargs || true)"
    if [[ -z "${start}" || -z "${command_line}" ]]; then
        echo "cannot record process identity for ${name} (pid=${pid})" >&2
        kill -TERM "${pid}" 2>/dev/null || true
        return 1
    fi
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
        "${label}" "${pid}" "${log}" "$*" "${start}" "${command_line}" >> "${MANIFEST}"
    printf '%s|%s\n' "${name}" "${pid}"
}

wait_for_keep() {
    local name="$1"
    local pid="$2"
    local log="$3"
    local ready="${log}.ready"
    local deadline=$((SECONDS + START_TIMEOUT))
    while ((SECONDS < deadline)); do
        if [[ -f "${ready}" ]] && kill -0 "${pid}" 2>/dev/null; then
            echo "PASS ${name} (pid=${pid})"
            return 0
        fi
        if grep -q -- '--- FAIL:' "${log}" 2>/dev/null ||
            grep -q '^FAIL' "${log}" 2>/dev/null; then
            echo "FAIL ${name} (pid=${pid})" >&2
            tail -40 "${log}" >&2 || true
            return 1
        fi
        if ! kill -0 "${pid}" 2>/dev/null; then
            echo "EXIT ${name} before KEEP marker" >&2
            tail -40 "${log}" >&2 || true
            return 1
        fi
        sleep 0.5
    done
    echo "TIMEOUT ${name} waiting for KEEP marker" >&2
    tail -40 "${log}" >&2 || true
    return 1
}

declare -a STARTED=()

start() {
    local name="$1"
    local log="${RET_DIR}/${name}.log"
    shift
    if ! should_start "${name}"; then
        return 0
    fi
    local record
    record="$(start_process "${name}" "${log}" "$@")" || return 1
    STARTED+=("${record}")
}

start a2a ./integration/a2a
start mcp ./integration/mcp
start skill ./integration/skill
start a2a-agentcard \
    -run '^TestA2AAgentCardExampleAcrossProcesses$' ./integration/a2a-agentcard
start agentcard-10 \
    -run '^TestAgentCardMultiProtocolProvider$/^schema-10$' ./integration/a2a-agentcard
start agentcard-03 \
    -run '^TestAgentCardMultiProtocolProvider$/^schema-03$' ./integration/a2a-agentcard
start agentcard-noserve \
    -run '^TestAgentCardMultiProtocolProvider$/^no-serve$' ./integration/a2a-agentcard

failures=0
for record in ${STARTED[@]+"${STARTED[@]}"}; do
    name="${record%%|*}"
    pid="${record#*|}"
    if ! wait_for_keep "${name}" "${pid}" "${RET_DIR}/${name}.log"; then
        failures=$((failures + 1))
    fi
done

echo
echo "retained run: ${RUN_ID}"
echo "run directory: ${RET_DIR}"
echo "manifest: ${MANIFEST}"
echo "server: ${SERVER}"
echo "namespace: ${NAMESPACE}"
echo "processes:"
while IFS=$'\t' read -r label pid log test start command_line; do
    [[ "${label}" == "label" || -z "${pid}" ]] && continue
    if kill -0 "${pid}" 2>/dev/null; then
        echo "  pid=${pid} test=${test} log=${log}"
    fi
done < "${MANIFEST}"
echo
echo "Cleanup when authorized:"
echo "  ./integration/cleanup.sh --run-id=${RUN_ID} --delete-service --force --polaris-server=${SERVER} --namespace=${NAMESPACE}"

if ((failures > 0)); then
    echo "${failures} retained suite(s) failed before the KEEP marker" >&2
    exit 1
fi
