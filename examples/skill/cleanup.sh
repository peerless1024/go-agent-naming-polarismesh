#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../internal/scripts" && pwd)"
source "${COMMON_DIR}/cleanup_common.sh"

cleanup_init skill
CLEANUP_SKILL_OWNER="${POLARIS_EXAMPLE_SKILL_OWNER:-trpc-agent-naming-polarismesh-examples}"
cleanup_add_service "${SKILL_AGENT_SERVICE:-trpc.polaris.examples.skill-agent}"
cleanup_add_skill "${SKILL_NAME:-polaris-greeting}"

cleanup_usage() {
    cat <<'EOF'
Usage: ./examples/skill/cleanup.sh [options]
  -f, --force                 apply without prompting
  --dry-run                   query and print the planned cleanup
  --delete-service            delete the Agent service and the Skill
  --polaris-server HOST       required with --delete-service
  --polaris-http-port PORT    Console HTTP port (default 8090)
  --namespace NAME            Polaris namespace
  --service NAME              Agent service name
  --skill NAME                Skill name
  --owner NAME                expected Skill owner
  --polaris-token TOKEN       Console token
EOF
}

cleanup_parse_args "$@"
cleanup_run
