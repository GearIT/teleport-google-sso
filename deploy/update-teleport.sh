#!/usr/bin/env bash
# Update Teleport linux-amd64 binaries from the latest (or a given) GitHub Release.
#
# Default install layout matches managed-updates style used on geargames auth:
#   /opt/teleport/system/bin/{teleport,tctl,tsh,tbot}
#
# Usage:
#   sudo bash deploy/update-teleport.sh
#   sudo bash deploy/update-teleport.sh --tag v19.0.0-oidc.6
#   sudo TELEPORT_BIN_DIR=/usr/local/bin bash deploy/update-teleport.sh
set -euo pipefail

REPO="${TELEPORT_REPO:-GearIT/teleport-google-sso}"
BIN_DIR="${TELEPORT_BIN_DIR:-/opt/teleport/system/bin}"
ASSET_SUFFIX="${TELEPORT_ASSET_SUFFIX:-linux-amd64-bin.tar.gz}"
SERVICE_NAME="${TELEPORT_SERVICE:-teleport}"
TAG=""

usage() {
  cat <<'EOF'
Usage: update-teleport.sh [--tag <git-tag>] [--bin-dir <dir>] [--repo <owner/name>]

  --tag       GitHub release tag (default: latest via API)
  --bin-dir   Install directory (default: /opt/teleport/system/bin)
  --repo      GitHub repo (default: GearIT/teleport-google-sso)
  -h, --help  Show this help

Env overrides: TELEPORT_REPO, TELEPORT_BIN_DIR, TELEPORT_ASSET_SUFFIX, TELEPORT_SERVICE
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      TAG="${2:?--tag requires a value}"
      shift 2
      ;;
    --bin-dir)
      BIN_DIR="${2:?--bin-dir requires a value}"
      shift 2
      ;;
    --repo)
      REPO="${2:?--repo requires a value}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ "$(id -u)" -ne 0 ]]; then
  echo "Run as root (sudo)." >&2
  exit 1
fi

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

need curl
need tar
need install

PYTHON=""
if command -v python3 >/dev/null 2>&1; then
  PYTHON=python3
elif command -v python >/dev/null 2>&1; then
  PYTHON=python
elif ! command -v jq >/dev/null 2>&1; then
  echo "Need python3/python or jq to parse GitHub API JSON." >&2
  exit 1
fi

if [[ -n "${TAG}" ]]; then
  API_URL="https://api.github.com/repos/${REPO}/releases/tags/${TAG}"
else
  API_URL="https://api.github.com/repos/${REPO}/releases/latest"
fi

WORK="$(mktemp -d -t teleport-update-XXXXXXXXXX)"
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

RELEASE_JSON_PATH="${WORK}/release.json"
echo "==> Fetching release metadata: ${API_URL}"
curl -fsSL \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  -o "${RELEASE_JSON_PATH}" \
  "${API_URL}"

parse_release() {
  local json_path="$1"
  local suffix="$2"
  if [[ -n "${PYTHON}" ]]; then
    "${PYTHON}" - "${json_path}" "${suffix}" <<'PY'
import json, sys
path, suffix = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as f:
    data = json.load(f)
tag = data.get("tag_name") or ""
url = sha_url = name = ""
for a in data.get("assets") or []:
    n = a.get("name") or ""
    if n.endswith(suffix) and not n.endswith(suffix + ".sha256"):
        url = a.get("browser_download_url") or ""
        name = n
    if n.endswith(suffix + ".sha256"):
        sha_url = a.get("browser_download_url") or ""
if not tag or not url:
    sys.stderr.write("Could not find asset ending with %r in release %r\n" % (suffix, tag))
    sys.exit(1)
print(tag)
print(url)
print(sha_url)
print(name)
PY
  else
    local tag url sha_url name
    tag="$(jq -r '.tag_name' "${json_path}")"
    name="$(jq -r --arg s "$suffix" '
      [.assets[]
        | select(.name | endswith($s))
        | select(.name | endswith($s + ".sha256") | not)
        | .name][0] // empty
    ' "${json_path}")"
    url="$(jq -r --arg n "$name" '.assets[] | select(.name == $n) | .browser_download_url' "${json_path}")"
    sha_url="$(jq -r --arg n "${name}.sha256" '(.assets[] | select(.name == $n) | .browser_download_url) // empty' "${json_path}")"
    if [[ -z "${tag}" || -z "${name}" || -z "${url}" || "${url}" == "null" ]]; then
      echo "Could not find asset ending with ${suffix} in release ${tag}" >&2
      exit 1
    fi
    printf '%s\n%s\n%s\n%s\n' "${tag}" "${url}" "${sha_url}" "${name}"
  fi
}

TAG_NAME=""
TARBALL_URL=""
SHA_URL=""
TARBALL_NAME=""
{
  IFS= read -r TAG_NAME
  IFS= read -r TARBALL_URL
  IFS= read -r SHA_URL
  IFS= read -r TARBALL_NAME
} < <(parse_release "${RELEASE_JSON_PATH}" "${ASSET_SUFFIX}")

echo "==> Release: ${TAG_NAME}"
echo "==> Asset:   ${TARBALL_NAME}"
echo "==> URL:     ${TARBALL_URL}"

TARBALL_PATH="${WORK}/${TARBALL_NAME}"
echo "==> Downloading tarball"
curl -fL --retry 5 --retry-delay 3 -o "${TARBALL_PATH}" "${TARBALL_URL}"

if [[ -n "${SHA_URL}" ]]; then
  echo "==> Verifying checksum"
  curl -fL --retry 3 -o "${TARBALL_PATH}.sha256" "${SHA_URL}"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "${WORK}" && sha256sum --status -c "${TARBALL_NAME}.sha256")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "${WORK}" && shasum -a 256 --status -c "${TARBALL_NAME}.sha256")
  else
    echo "Warning: no sha256sum/shasum; skipping checksum verification" >&2
  fi
  echo "==> Checksum OK"
else
  echo "Warning: no .sha256 asset found; skipping checksum verification" >&2
fi

echo "==> Extracting"
tar -xzf "${TARBALL_PATH}" -C "${WORK}"

for bin in teleport tctl tsh tbot; do
  if [[ ! -f "${WORK}/teleport/${bin}" ]]; then
    echo "Missing binary in tarball: teleport/${bin}" >&2
    exit 1
  fi
done

mkdir -p "${BIN_DIR}"

STOPPED=0
if command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet "${SERVICE_NAME}" 2>/dev/null; then
    echo "==> Stopping ${SERVICE_NAME}"
    systemctl stop "${SERVICE_NAME}"
    STOPPED=1
  fi
fi

echo "==> Installing binaries to ${BIN_DIR}"
install -m0755 "${WORK}/teleport/teleport" "${BIN_DIR}/teleport"
install -m0755 "${WORK}/teleport/tctl"     "${BIN_DIR}/tctl"
install -m0755 "${WORK}/teleport/tsh"      "${BIN_DIR}/tsh"
install -m0755 "${WORK}/teleport/tbot"     "${BIN_DIR}/tbot"

if command -v teleport >/dev/null 2>&1; then
  teleport version
else
  "${BIN_DIR}/teleport" version
fi

if [[ "${STOPPED}" -eq 1 ]]; then
  echo "==> Starting ${SERVICE_NAME}"
  systemctl start "${SERVICE_NAME}"
  systemctl --no-pager --full status "${SERVICE_NAME}" || true
fi

echo "==> Done (release ${TAG_NAME})"
