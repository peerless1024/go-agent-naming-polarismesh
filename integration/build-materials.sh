#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DIST_DIR="${SCRIPT_DIR}/dist"
BUNDLE_NAME="go-agent-naming-integration"
OUTPUT=""
ZIP="${ZIP:-zip}"
UNZIP="${UNZIP:-unzip}"
GO_BIN="${GO_BIN:-go}"
MAKE_BIN="${MAKE_BIN:-make}"
TARGET="${POLARIS_E2E_PACKAGE_TARGET:-linux/amd64}"
STAGE_DIR=""

usage() {
    cat <<'EOF'
Usage: ./integration/build-materials.sh [options]

Options:
  -o, --output PATH  Output zip path
      --target OS/ARCH
                    Binary target (default: linux/amd64)
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
        --target)
            (($# >= 2)) || die "$1 requires OS/ARCH"
            TARGET="$2"
            shift 2
            ;;
        --target=*)
            TARGET="${1#*=}"
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

if [[ ! "${TARGET}" =~ ^[A-Za-z0-9_]+/[A-Za-z0-9_]+$ ]]; then
    die "target must use OS/ARCH form, for example linux/amd64"
fi
target_os="${TARGET%%/*}"
target_arch="${TARGET#*/}"
[[ -n "${OUTPUT}" ]] ||
    OUTPUT="${DIST_DIR}/${BUNDLE_NAME}-${target_os}-${target_arch}-bin.zip"

require_bin "${ZIP}"
require_bin "${UNZIP}"
require_bin "${MAKE_BIN}"
command -v "${GO_BIN}" >/dev/null 2>&1 || die "Go binary not found: ${GO_BIN}"
[[ -f "${REPO_ROOT}/go.mod" ]] || die "missing go.mod"
[[ -f "${REPO_ROOT}/integration/Makefile" ]] || die "missing integration/Makefile"
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
        ! -path "${REPO_ROOT}/integration/bin/*" \
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

supported_targets="$("${GO_BIN}" tool dist list)"
if [[ $'\n'"${supported_targets}"$'\n' != *$'\n'"${TARGET}"$'\n'* ]]; then
    die "unsupported Go target: ${TARGET}"
fi
package_cache="${GOCACHE:-${TMPDIR:-/tmp}/go-agent-naming-package-cache}"
printf '[package] building integration binaries for %s\n' "${TARGET}"
"${MAKE_BIN}" -C "${REPO_ROOT}/integration" build \
    TARGET="${TARGET}" \
    GO="${GO_BIN}" \
    GOCACHE="${package_cache}"

binary_rel="integration/bin/${target_os}-${target_arch}"
binary_dir="${bundle_dir}/${binary_rel}"
mkdir -p "${binary_dir}"
cp -Rp "${REPO_ROOT}/integration/bin/${target_os}-${target_arch}/." "${binary_dir}/"

find "${bundle_dir}" -type f -name '*.sh' -exec chmod +x {} +
find "${binary_dir}" -type f -exec chmod +x {} +

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
    printf 'execution=binary\n'
    printf 'target=%s\n' "${TARGET}"
    printf 'binary_dir=%s\n' "${binary_rel}"
    printf 'entrypoint=integration/verify_all.sh\n'
} > "${bundle_dir}/PACKAGE-INFO.txt"

required_files=(
    "go.mod"
    "go.sum"
    "integration/verify_all.sh"
    "integration/run_retained.sh"
    "integration/cleanup.sh"
    "integration/pack-logs.sh"
    "integration/Makefile"
    "integration/README.md"
    "integration/README.zh_CN.md"
    "examples/a2a-agentcard/provider/main.go"
    "${binary_rel}/a2a.test"
    "${binary_rel}/mcp.test"
    "${binary_rel}/skill.test"
    "${binary_rel}/a2a-agentcard.test"
    "${binary_rel}/harness.test"
    "${binary_rel}/agentcard-provider"
    "${binary_rel}/agentcard-client"
)
for relative in "${required_files[@]}"; do
    [[ -f "${bundle_dir}/${relative}" ]] || die "package is missing ${relative}"
done

(
    cd "${STAGE_DIR}"
    COPYFILE_DISABLE=1 "${ZIP}" -rq -9 "${OUTPUT}" "${BUNDLE_NAME}"
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
