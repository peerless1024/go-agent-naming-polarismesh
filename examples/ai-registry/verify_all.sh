#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GO_BIN="${GO_BIN:-go}"
BUILD_DIR="${SCRIPT_DIR}/.build"
RESOURCE_POLICY="${POLARIS_E2E_RESOURCES:-}"
SERVER="${POLARIS_SERVER:-}"

for arg in "$@"; do
    case "$arg" in
        --resources=*|-resources=*) RESOURCE_POLICY="${arg#*=}" ;;
        --server=*|-server=*) SERVER="${arg#*=}" ;;
    esac
done

if [[ "${RESOURCE_POLICY}" != "keep" && "${RESOURCE_POLICY}" != "cleanup" ]]; then
    printf '%s\n' 'Choose --resources=keep or --resources=cleanup explicitly.' >&2
    exit 2
fi
if [[ -z "${SERVER}" ]]; then
    printf '%s\n' 'Choose --server=<host> or set POLARIS_SERVER.' >&2
    exit 2
fi

export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/go-agent-naming-e2e-cache}"
mkdir -p "${BUILD_DIR}"
cd "${ROOT_DIR}"

"${GO_BIN}" build -race -o "${BUILD_DIR}/provider" ./examples/ai-registry/provider
"${GO_BIN}" build -race -o "${BUILD_DIR}/consumer" ./examples/ai-registry/consumer
"${GO_BIN}" build -race -o "${BUILD_DIR}/e2e" ./examples/ai-registry/e2e

cd "${SCRIPT_DIR}"
NO_PROXY_HOSTS="127.0.0.1,localhost"
NO_PROXY_HOSTS="${SERVER},${NO_PROXY_HOSTS}"
export NO_PROXY="${NO_PROXY:+${NO_PROXY},}${NO_PROXY_HOSTS}"
export no_proxy="${NO_PROXY}"

"${BUILD_DIR}/e2e" -mode=verify -provider-binary="${BUILD_DIR}/provider" "$@"
