#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER="${POLARIS_SERVER:-}"
TOTAL_TIMEOUT="${POLARIS_E2E_TOTAL_TIMEOUT:-20m}"
RUN_ID="${POLARIS_E2E_RUN_ID:-}"
LOG_DIR="${POLARIS_E2E_LOG_DIR:-${ROOT_DIR}/integration/.logs}"
GO_ARGS=()

usage() {
    cat <<'EOF'
Usage:
  POLARIS_SERVER=host bash integration/verify_all.sh [options]

Options:
  --server=host       Polaris host
  --namespace=name    Test namespace (default: go-agent-naming-e2e)
  --run-id=id         Run identifier
  --timeout=30s       Per-operation timeout
  -v                  Verbose go test output
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
            export POLARIS_E2E_RUN_ID="${arg#*=}"
            ;;
        --timeout=*)
            export POLARIS_E2E_TIMEOUT="${arg#*=}"
            ;;
        -v|--verbose)
            GO_ARGS+=("-v")
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
export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/go-agent-naming-e2e-cache}"
mkdir -p "${LOG_DIR}"
LOG_DIR="$(cd "${LOG_DIR}" && pwd)"
LOG_FILE="${LOG_DIR}/${RUN_ID}.log"

cd "${ROOT_DIR}"
go test \
    -tags=integration \
    -count=1 \
    -timeout="${TOTAL_TIMEOUT}" \
    "${GO_ARGS[@]}" \
    ./integration/... 2>&1 | tee "${LOG_FILE}"

printf '[verify] log: %s\n' "${LOG_FILE}" >&2
