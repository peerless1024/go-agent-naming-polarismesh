#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
RUN_ID="${POLARIS_E2E_RUN_ID:-}"
SOURCE_DIR=""
OUTPUT=""
TAR="${TAR:-tar}"

usage() {
    cat <<'EOF'
Usage: ./integration/pack-logs.sh [options]

Options:
  --run-id ID       Run ID used by verify_all.sh or run_retained.sh
  --source DIR      Additional absolute log directory to include
  --output FILE     Output .tar.gz path
  -h, --help        Show this help

By default the archive includes:
  integration/.retained/<run-id>/ logs and manifest
  integration/.logs/<run-id>.log
Secret files such as retained env files and .cloud-state are excluded.
EOF
}

die() {
    printf '[FAIL] %s\n' "$*" >&2
    exit 1
}

is_sensitive_path() {
    case "$1" in
        env|*/env|.cloud-state|*/.cloud-state|.DS_Store|*/.DS_Store)
            return 0
            ;;
    esac
    return 1
}

while (($# > 0)); do
    case "$1" in
        --run-id=*) RUN_ID="${1#*=}"; shift ;;
        --run-id) (($# >= 2)) || die "$1 requires an ID"; RUN_ID="$2"; shift 2 ;;
        --source=*) SOURCE_DIR="${1#*=}"; shift ;;
        --source) (($# >= 2)) || die "$1 requires a directory"; SOURCE_DIR="$2"; shift 2 ;;
        --output=*) OUTPUT="${1#*=}"; shift ;;
        --output) (($# >= 2)) || die "$1 requires a file"; OUTPUT="$2"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
done

[[ -n "${RUN_ID}" ]] || die "--run-id or POLARIS_E2E_RUN_ID is required"
[[ "${RUN_ID}" =~ ^[A-Za-z0-9._-]+$ ]] || die "invalid run ID"
if [[ -n "${SOURCE_DIR}" ]]; then
    [[ "${SOURCE_DIR}" == /* ]] || die "--source must be absolute"
    [[ -d "${SOURCE_DIR}" ]] || die "source directory not found: ${SOURCE_DIR}"
fi
[[ -n "${OUTPUT}" ]] || OUTPUT="${SCRIPT_DIR}/dist/go-agent-naming-logs-${RUN_ID}.tar.gz"
output_dir="$(dirname "${OUTPUT}")"
mkdir -p "${output_dir}"
output_dir="$(cd "${output_dir}" && pwd)"
OUTPUT="${output_dir}/$(basename "${OUTPUT}")"
[[ "${OUTPUT}" == *.tar.gz ]] || die "output must end with .tar.gz"

command -v "${TAR}" >/dev/null 2>&1 || die "tar is required"

stage="$(mktemp -d "${TMPDIR:-/tmp}/go-agent-naming-logs.XXXXXX")"
cleanup() {
    rm -rf "${stage}"
}
trap cleanup EXIT

mkdir -p "${stage}/logs"
found=false

retained="${SCRIPT_DIR}/.retained/${RUN_ID}"
if [[ -d "${retained}" ]]; then
    mkdir -p "${stage}/logs/retained-${RUN_ID}"
    while IFS= read -r -d '' file; do
        relative="${file#"${retained}"/}"
        is_sensitive_path "${relative}" && continue
        mkdir -p "${stage}/logs/retained-${RUN_ID}/$(dirname "${relative}")"
        cp -p "${file}" "${stage}/logs/retained-${RUN_ID}/${relative}"
    done < <(find "${retained}" -type f -print0)
    found=true
fi

for candidate in "${SCRIPT_DIR}/.logs/${RUN_ID}.log" "${SCRIPT_DIR}/.logs/${RUN_ID}"*; do
    [[ -e "${candidate}" ]] || continue
    if [[ -f "${candidate}" ]]; then
        is_sensitive_path "$(basename "${candidate}")" && continue
        cp -p "${candidate}" "${stage}/logs/"
        found=true
    elif [[ -d "${candidate}" ]]; then
        destination="${stage}/logs/$(basename "${candidate}")"
        mkdir -p "${destination}"
        while IFS= read -r -d '' file; do
            relative="${file#"${candidate}"/}"
            is_sensitive_path "${relative}" && continue
            mkdir -p "${destination}/$(dirname "${relative}")"
            cp -p "${file}" "${destination}/${relative}"
        done < <(find "${candidate}" -type f -print0)
        found=true
    fi
done

if [[ -n "${SOURCE_DIR}" ]]; then
    mkdir -p "${stage}/logs/additional"
    while IFS= read -r -d '' file; do
        relative="${file#"${SOURCE_DIR}"/}"
        is_sensitive_path "${relative}" && continue
        mkdir -p "${stage}/logs/additional/$(dirname "${relative}")"
        cp -p "${file}" "${stage}/logs/additional/${relative}"
    done < <(find "${SOURCE_DIR}" -type f -print0)
    found=true
fi

[[ "${found}" == "true" ]] || die "no logs found for run ID ${RUN_ID}"

{
    printf 'run_id=%s\n' "${RUN_ID}"
    printf 'packed_at=%s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')"
    printf 'server=%s\n' "${POLARIS_SERVER:-unknown}"
    printf 'namespace=%s\n' "${POLARIS_E2E_NAMESPACE:-unknown}"
    printf 'hostname=%s\n' "$(hostname 2>/dev/null || true)"
    printf 'uname=%s\n' "$(uname -a 2>/dev/null || true)"
    printf 'go_version=%s\n' "$(go version 2>/dev/null || true)"
    if [[ -f "${REPO_ROOT}/PACKAGE-INFO.txt" ]]; then
        printf 'package_info=present\n'
        sed 's/^/package_/' "${REPO_ROOT}/PACKAGE-INFO.txt"
    fi
} > "${stage}/run-info.txt"

archive_listing="${stage}/archive.list"
(
    cd "${stage}"
    "${TAR}" -czf "${OUTPUT}" .
)
"${TAR}" -tzf "${OUTPUT}" > "${archive_listing}"
if grep -Eq '(^|/)(env|\.cloud-state)$' "${archive_listing}"; then
    rm -f "${OUTPUT}"
    die "refusing to package secret-bearing files"
fi

if command -v sha256sum >/dev/null 2>&1; then
    checksum="$(sha256sum "${OUTPUT}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
    checksum="$(shasum -a 256 "${OUTPUT}" | awk '{print $1}')"
else
    checksum=""
fi

printf '[logs] output: %s\n' "${OUTPUT}"
if [[ -n "${checksum}" ]]; then
    printf '[logs] sha256: %s\n' "${checksum}"
fi
