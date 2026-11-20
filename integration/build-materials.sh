#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DIST_DIR="${SCRIPT_DIR}/dist"
BUNDLE_NAME="go-agent-naming-integration"
OUTPUT="${DIST_DIR}/${BUNDLE_NAME}.zip"
ZIP="${ZIP:-zip}"
UNZIP="${UNZIP:-unzip}"
GO_BIN="${GO_BIN:-go}"
VENDOR=false
STAGE_DIR=""

usage() {
    cat <<'EOF'
Usage: ./integration/build-materials.sh [options]

Options:
  -o, --output PATH  Output zip path
      --vendor      Include Go module dependencies under vendor/
      --clean       Remove integration/dist and exit
  -h, --help        Show this help
EOF
}

die() {
    printf '[FAIL] %s\n' "$*" >&2
    exit 1
}

cleanup() {
    if [[ -n "${STAGE_DIR}" && -d "${STAGE_DIR}" ]]; then
        rm -rf "${STAGE_DIR}"
    fi
}
trap cleanup EXIT

require_bin() {
    command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

while (($# > 0)); do
    case "$1" in
        -o|--output)
            (($# >= 2)) || die "$1 requires a path"
            OUTPUT="$2"
            shift 2
            ;;
        --vendor)
            VENDOR=true
            shift
            ;;
        --clean)
            rm -rf "${DIST_DIR}"
            printf '[package] removed %s\n' "${DIST_DIR}"
            exit 0
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown option: $1"
            ;;
    esac
done

require_bin "${ZIP}"
require_bin "${UNZIP}"
[[ -f "${REPO_ROOT}/go.mod" ]] || die "missing go.mod"
[[ -f "${REPO_ROOT}/integration/verify_all.sh" ]] || die "missing integration/verify_all.sh"
[[ -f "${REPO_ROOT}/integration/run_retained.sh" ]] || die "missing integration/run_retained.sh"
[[ -f "${REPO_ROOT}/integration/cleanup.sh" ]] || die "missing integration/cleanup.sh"

output_dir="$(dirname "${OUTPUT}")"
mkdir -p "${output_dir}"
output_dir="$(cd "${output_dir}" && pwd)"
OUTPUT="${output_dir}/$(basename "${OUTPUT}")"
[[ "${OUTPUT}" == *.zip ]] || die "output path must end with .zip"
rm -f "${OUTPUT}"

STAGE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/go-agent-naming-integration.XXXXXX")"
bundle_dir="${STAGE_DIR}/${BUNDLE_NAME}"
mkdir -p "${bundle_dir}"

while IFS= read -r -d '' file; do
    relative="${file#"${REPO_ROOT}"/}"
    mkdir -p "${bundle_dir}/$(dirname "${relative}")"
    cp -p "${file}" "${bundle_dir}/${relative}"
done < <(
    find "${REPO_ROOT}" -type f \
        ! -path "${REPO_ROOT}/.git/*" \
        ! -path "${REPO_ROOT}/.agents/*" \
        ! -path "${REPO_ROOT}/.idea/*" \
        ! -path "${REPO_ROOT}/.tcodex/*" \
        ! -path "${REPO_ROOT}/specs/*" \
        ! -path "${REPO_ROOT}/integration/dist/*" \
        ! -path "${REPO_ROOT}/integration/.retained/*" \
        ! -path '*/.build/*' \
        ! -path '*/.logs/*' \
        ! -path '*/.cloud-runs/*' \
        ! -path '*/.skill-cache/*' \
        ! -path '*/.polaris-cache/*' \
        ! -path '*/polaris/*' \
        ! -name '*.log' \
        ! -name '.*-pids' \
        ! -name '.DS_Store' \
        -print0
)

if [[ "${VENDOR}" == "true" ]]; then
    command -v "${GO_BIN}" >/dev/null 2>&1 || die "Go binary not found: ${GO_BIN}"
    (
        cd "${REPO_ROOT}"
        GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/go-agent-naming-package-cache}" \
            "${GO_BIN}" mod vendor -o "${bundle_dir}/vendor"
    )
fi

find "${bundle_dir}" -type f -name '*.sh' -exec chmod +x {} +

git_branch="unknown"
git_commit="unknown"
git_dirty="unknown"
if git -C "${REPO_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git_branch="$(git -C "${REPO_ROOT}" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
    git_commit="$(git -C "${REPO_ROOT}" rev-parse --short=12 HEAD 2>/dev/null || true)"
    if [[ -n "$(git -C "${REPO_ROOT}" status --porcelain --untracked-files=normal 2>/dev/null)" ]]; then
        git_dirty="yes"
    else
        git_dirty="no"
    fi
fi

{
    printf 'format=1\n'
    printf 'built_at=%s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')"
    printf 'git_branch=%s\n' "${git_branch}"
    printf 'git_commit=%s\n' "${git_commit}"
    printf 'git_dirty=%s\n' "${git_dirty}"
    printf 'vendored=%s\n' "${VENDOR}"
    printf 'entrypoint=integration/verify_all.sh\n'
} > "${bundle_dir}/PACKAGE-INFO.txt"

required_files=(
    "go.mod"
    "go.sum"
    "integration/verify_all.sh"
    "integration/run_retained.sh"
    "integration/cleanup.sh"
    "integration/pack-logs.sh"
    "integration/README.md"
    "integration/README.zh_CN.md"
    "examples/a2a-agentcard/provider/main.go"
)
for relative in "${required_files[@]}"; do
    [[ -f "${bundle_dir}/${relative}" ]] || die "package is missing ${relative}"
done

(
    cd "${STAGE_DIR}"
    COPYFILE_DISABLE=1 "${ZIP}" -rq "${OUTPUT}" "${BUNDLE_NAME}"
)
"${UNZIP}" -tq "${OUTPUT}" >/dev/null || die "zip verification failed"

file_count="$(find "${bundle_dir}" -type f | wc -l | tr -d ' ')"
size="$(du -h "${OUTPUT}" | awk '{print $1}')"
checksum="$(sha256 "${OUTPUT}" || true)"

printf '[package] output: %s\n' "${OUTPUT}"
printf '[package] size: %s, files: %s\n' "${size}" "${file_count}"
if [[ -n "${checksum}" ]]; then
    printf '[package] sha256: %s\n' "${checksum}"
fi
printf '[package] unpack on the cloud machine, run the suites, then use integration/pack-logs.sh\n'
