#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../internal/scripts" && pwd)"
source "${COMMON_DIR}/cleanup_common.sh"

cleanup_init a2a
cleanup_add_service "${A2A_SERVICE:-trpc.polaris.examples.a2a}"

cleanup_usage() {
    cat <<'EOF'
Usage: ./examples/a2a/cleanup.sh [options]
  -f, --force                 apply without prompting
  --dry-run                   query and print the planned cleanup
  --delete-service            delete the A2A service and its instances
  --polaris-server HOST       required with --delete-service
  --polaris-http-port PORT    Console HTTP port (default 8090)
  --namespace NAME            Polaris namespace
  --service NAME              A2A service name
  --polaris-token TOKEN       Console token
EOF
}

cleanup_parse_args "$@"
cleanup_run
