#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER="${POLARIS_SERVER:-}"
TOTAL_TIMEOUT="${POLARIS_E2E_TOTAL_TIMEOUT:-20m}"
RUN_ID="${POLARIS_E2E_RUN_ID:-}"
LOG_DIR="${POLARIS_E2E_LOG_DIR:-${ROOT_DIR}/integration/.logs}"
BIN_DIR="${POLARIS_E2E_BIN_DIR:-}"
VERBOSE=false

usage() {
    cat <<'EOF'
Usage:
  POLARIS_SERVER=host bash integration/verify_all.sh [options]

Options:
  --server=host       Polaris host
  --namespace=name    Test namespace (default: go-agent-naming-e2e)
  --run-id=id         Run identifier
  --timeout=30s       Per-operation timeout
  --bin-dir=DIR       Prebuilt integration binaries
  -v                  Verbose test output
  -h, --help          Show this help
EOF
}

for arg in "$@"; do
    case "${arg}" in
        --server=*)
            SERVER="${arg#*=}"
            ;;
        --namespace=*)
            export POLARIS_E2E_NAMESPACE="${arg#*=}"
            ;;
        --run-id=*)
            RUN_ID="${arg#*=}"
            export POLARIS_E2E_RUN_ID="${RUN_ID}"
            ;;
        --timeout=*)
            export POLARIS_E2E_TIMEOUT="${arg#*=}"
            ;;
        --bin-dir=*)
            BIN_DIR="${arg#*=}"
            ;;
        -v|--verbose)
            VERBOSE=true
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            printf 'unknown option: %s\n' "${arg}" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ -z "${SERVER}" ]]; then
    printf 'POLARIS_SERVER or --server is required\n' >&2
    exit 2
fi

if [[ -z "${RUN_ID}" ]]; then
    RUN_ID="$(date '+%Y%m%d-%H%M%S')-$$"
fi
if [[ ! "${RUN_ID}" =~ ^[A-Za-z0-9._-]+$ ]]; then
    printf 'invalid run ID: %s\n' "${RUN_ID}" >&2
    exit 2
fi

export POLARIS_SERVER="${SERVER}"
export POLARIS_E2E_RUN_ID="${RUN_ID}"
mkdir -p "${LOG_DIR}"
LOG_DIR="$(cd "${LOG_DIR}" && pwd)"
LOG_FILE="${LOG_DIR}/${RUN_ID}.log"

cd "${ROOT_DIR}"

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
    printf 'binary package target is %s, cloud host is %s\n' \
        "${package_target}" "${host_target}" >&2
    exit 2
fi
[[ -n "${BIN_DIR}" ]] ||
    BIN_DIR="${ROOT_DIR}/integration/bin/${host_os}-${host_arch}"

[[ "${BIN_DIR}" == /* ]] || BIN_DIR="${ROOT_DIR}/${BIN_DIR}"
[[ -d "${BIN_DIR}" ]] || {
    printf 'binary directory not found: %s; run make -C integration build\n' \
        "${BIN_DIR}" >&2
    exit 2
}
for binary in a2a.test mcp.test skill.test a2a-agentcard.test harness.test \
    agentcard-provider agentcard-client; do
    [[ -x "${BIN_DIR}/${binary}" ]] || {
        printf 'binary not found or not executable: %s\n' "${BIN_DIR}/${binary}" >&2
        exit 2
    }
done
export POLARIS_E2E_BIN_DIR="${BIN_DIR}"
export POLARIS_E2E_AGENTCARD_PROVIDER_BIN="${BIN_DIR}/agentcard-provider"
export POLARIS_E2E_AGENTCARD_CLIENT_BIN="${BIN_DIR}/agentcard-client"

set +e
test_args=(-test.count=1 "-test.timeout=${TOTAL_TIMEOUT}")
if [[ "${VERBOSE}" == "true" ]]; then
    test_args+=(-test.v)
fi
{
    status=0
    for binary in a2a.test mcp.test skill.test a2a-agentcard.test harness.test; do
        "${BIN_DIR}/${binary}" "${test_args[@]}" || status=1
    done
    exit "${status}"
} 2>&1 | tee "${LOG_FILE}"
status="${PIPESTATUS[0]}"
set -e

printf '[verify] log: %s\n' "${LOG_FILE}" >&2
exit "${status}"
