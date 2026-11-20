#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER="${POLARIS_SERVER:-}"
RUN_ID="${POLARIS_E2E_RUN_ID:-}"
NAMESPACE="${POLARIS_E2E_NAMESPACE:-go-agent-naming-e2e}"
SUITE_FILTER="${POLARIS_E2E_SUITES:-}"
START_TIMEOUT="${POLARIS_E2E_START_TIMEOUT:-240}"
BIN_DIR="${POLARIS_E2E_BIN_DIR:-}"

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
  --bin-dir=DIR          Use prebuilt integration binaries
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
        --bin-dir=*) BIN_DIR="${arg#*=}" ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: ${arg}" >&2; usage >&2; exit 2 ;;
    esac
done

if [[ -z "${SERVER}" ]]; then
    echo "POLARIS_SERVER or --server is required" >&2
    exit 2
fi
package_target=""
if [[ -f "${ROOT_DIR}/PACKAGE-INFO.txt" ]]; then
    package_target="$(awk -F= '$1 == "target" {print $2}' "${ROOT_DIR}/PACKAGE-INFO.txt")"
fi
host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
host_arch="$(uname -m)"
case "${host_arch}" in
    x86_64|amd64) host_arch="amd64" ;;
    arm64|aarch64) host_arch="arm64" ;;
esac
host_target="${host_os}/${host_arch}"
if [[ -n "${package_target}" && "${package_target}" != "${host_target}" ]]; then
    echo "binary package target is ${package_target}, cloud host is ${host_target}" >&2
    exit 2
fi
[[ -n "${BIN_DIR}" ]] ||
    BIN_DIR="${ROOT_DIR}/integration/bin/${host_os}-${host_arch}"
[[ "${BIN_DIR}" == /* ]] || BIN_DIR="${ROOT_DIR}/${BIN_DIR}"
for binary in a2a.test mcp.test skill.test a2a-agentcard.test \
    agentcard-provider agentcard-client; do
    if [[ ! -x "${BIN_DIR}/${binary}" ]]; then
        echo "binary not found or not executable: ${BIN_DIR}/${binary}; run make -C integration build" >&2
        exit 2
    fi
done
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
write_env POLARIS_E2E_BIN_DIR "${BIN_DIR}"
write_env POLARIS_E2E_AGENTCARD_PROVIDER_BIN "${BIN_DIR}/agentcard-provider"
write_env POLARIS_E2E_AGENTCARD_CLIENT_BIN "${BIN_DIR}/agentcard-client"
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
    local test_description="$*"
    local name="$1"
    local log="$2"
    shift 2
    local executable="$1"
    shift
    if ! should_start "${name}"; then
        return 0
    fi
    local ready="${log}.ready"
    local quoted_args=""
    local arg
    for arg in "$@"; do
        quoted_args="${quoted_args} $(printf '%q' "${arg}")"
    done
    local command
    command="set -a; source '${ENV_FILE}'; set +a; export POLARIS_E2E_KEEP_READY_FILE='${ready}'; cd '${ROOT_DIR}'; $(printf '%q' "${executable}")${quoted_args} > '${log}' 2>&1 & child=\$!; trap 'kill -TERM \$child 2>/dev/null; wait \$child; exit 0' TERM INT HUP; wait \$child; exit 0"
    local label=""
    local pid=""
    if [[ "$(uname -s)" == "Darwin" ]] && command -v launchctl >/dev/null 2>&1; then
        label="com.trpc.gan.retain.${RUN_ID}.${name}"
        local existing_pid
        existing_pid="$(launchctl list 2>/dev/null | awk -v label="${label}" '$3 == label {print $1}')"
        if [[ -n "${existing_pid}" && "${existing_pid}" != "-" ]]; then
            if grep -q '^KEEP:' "${log}" 2>/dev/null; then
                start="$(ps -p "${existing_pid}" -o lstart= 2>/dev/null | xargs || true)"
                command_line="$(ps -p "${existing_pid}" -o command= 2>/dev/null | xargs || true)"
                if [[ -z "${start}" || -z "${command_line}" ]]; then
                    echo "cannot record existing retained process ${label} (pid=${existing_pid})" >&2
                    return 1
                fi
                if ! awk -F '\t' -v label="${label}" -v pid="${existing_pid}" \
                    '$1 == label && $2 == pid {found=1} END {exit !found}' "${MANIFEST}"; then
                    printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
                        "${label}" "${existing_pid}" "${log}" "${test_description}" "${start}" "${command_line}" >> "${MANIFEST}"
                fi
                printf '%s\n' "${RUN_ID}" > "${ready}"
                printf '%s|%s\n' "${name}" "${existing_pid}"
                return 0
            fi
            echo "launchctl job ${label} is already running without KEEP (pid=${existing_pid})" >&2
            return 1
        fi
        if [[ "${existing_pid}" == "-" ]]; then
            launchctl remove "${label}" >/dev/null 2>&1 || true
        fi
        rm -f "${ready}"
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
        rm -f "${ready}"
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
        "${label}" "${pid}" "${log}" "${test_description}" "${start}" "${command_line}" >> "${MANIFEST}"
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

suite_package() {
    case "$1" in
        agentcard-10|agentcard-03|agentcard-noserve) printf 'a2a-agentcard' ;;
        *) printf '%s' "$1" ;;
    esac
}

start() {
    local name="$1"
    local log="${RET_DIR}/${name}.log"
    shift
    if ! should_start "${name}"; then
        return 0
    fi
    local suite
    suite="$(suite_package "${name}")"
    local executable="${BIN_DIR}/${suite}.test"
    local -a command_args=(-test.count=1 -test.v -test.timeout=0)
    while (($# > 0)); do
        case "$1" in
            -run)
                command_args+=(-test.run "$2")
                shift 2
                ;;
            *)
                command_args+=("$1")
                shift
                ;;
        esac
    done
    local record
    record="$(start_process "${name}" "${log}" "${executable}" "${command_args[@]}")" || return 1
    STARTED+=("${record}")
}

start a2a
start mcp
start skill
start a2a-agentcard \
    -run '^TestA2AAgentCardExampleAcrossProcesses$'
start agentcard-10 \
    -run '^TestAgentCardMultiProtocolProvider$/^schema-10$'
start agentcard-03 \
    -run '^TestAgentCardMultiProtocolProvider$/^schema-03$'
start agentcard-noserve \
    -run '^TestAgentCardMultiProtocolProvider$/^no-serve$'

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
