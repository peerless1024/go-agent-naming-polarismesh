#!/bin/bash
set -euo pipefail
COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../internal" && pwd)"
source "${COMMON_DIR}/e2e_common.sh"
feature_common_init skill
SKILL_NAME="trpc-agent-naming-demo-skill"
feature_usage() {
    cat <<'EOF'
Usage: ./examples/skill/cleanup.sh [options]
  -f, --force             apply without prompting
  --dry-run               show planned cleanup
  --keep-logs             keep .build/.logs
  --delete-service        also delete the demo Skill and service
  --polaris-server HOST   required with --delete-service
EOF
}
feature_cleanup "trpc.polaris.demo.skill" "${SKILL_NAME}" "$@"
