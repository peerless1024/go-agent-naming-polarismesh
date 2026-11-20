#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../internal/scripts" && pwd)"
source "${COMMON_DIR}/cleanup_common.sh"

cleanup_init mcp
cleanup_add_service "${MCP_SERVICE:-trpc.polaris.examples.mcp}" true
cleanup_add_service "${MCP_AGENT_SERVICE:-trpc.polaris.examples.mcp-agent}"

cleanup_usage() {
    cat <<'EOF'
Usage: ./examples/mcp/cleanup.sh [options]
  -f, --force                 apply without prompting
  --dry-run                   query and print the planned cleanup
  --delete-service            delete both MCP and Agent services
  --polaris-server HOST       required with --delete-service
  --polaris-http-port PORT    Console HTTP port (default 8090)
  --namespace NAME            Polaris namespace
  --contract-service NAME     additional service whose contracts to delete
  --service NAME              additional service to delete
  --polaris-token TOKEN       Console token

The default cleanup removes the MCP service, its Service Contract, and the
Agent service.
EOF
}

cleanup_parse_args "$@"
cleanup_run
