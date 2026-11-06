#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GO_BIN="${GO_BIN:-go}"
BUILD_DIR="${SCRIPT_DIR}/.build"

usage() {
    cat <<'EOF'
Usage:
  ./cleanup.sh --state=.logs/<run-id>/state.json --dry-run
  ./cleanup.sh --state=.logs/<run-id>/state.json --apply
  ./cleanup.sh --state=.logs/<run-id>/state.json --apply --delete-service
  ./cleanup.sh --delete-service --dry-run
  ./cleanup.sh --delete-service --apply

Without --delete-service, cleanup only stops state-owned processes. With
--delete-service, it also removes state services/Skills and historical empty
go-agent-e2e-* services owned by this example. --delete-services is an alias.
The command previews by default; --apply or --force is required to delete.
EOF
}

for arg in "$@"; do
    case "${arg}" in
        -h|--help) usage; exit 0 ;;
    esac
done

export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/go-agent-naming-e2e-cache}"
mkdir -p "${BUILD_DIR}"
cd "${ROOT_DIR}"
"${GO_BIN}" build -race -o "${BUILD_DIR}/cleanup" ./examples/ai-registry/e2e

cd "${SCRIPT_DIR}"
"${BUILD_DIR}/cleanup" -mode=cleanup "$@"
