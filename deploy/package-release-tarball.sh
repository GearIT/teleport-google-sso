#!/usr/bin/env bash
# Packages Teleport linux amd64 release assets the same way CI publishes them.
# Used by:
#   - .github/workflows/release.yml (real binaries)
#   - deploy/validate-release-tarball.sh (dummy binaries, fast gate)
#
# Required layout for node-join install.sh (tarball path):
#   teleport/
#     teleport, tctl, tsh, tbot
#     examples/systemd/teleport.service
set -euo pipefail

ROOT_DIR="${ROOT_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
BUILD_DIR="${BUILD_DIR:-${ROOT_DIR}/build}"
OUT_DIR="${OUT_DIR:-${ROOT_DIR}}"
VERSION="${VERSION:?VERSION is required (e.g. 19.0.0-prealpha.2)}"

TARBALL="teleport-v${VERSION}-linux-amd64-bin.tar.gz"
STAGE="$(mktemp -d)"
cleanup() { rm -rf "${STAGE}"; }
trap cleanup EXIT

mkdir -p "${STAGE}/teleport/examples/systemd"

need_bin() {
  local name="$1"
  local src="${BUILD_DIR}/${name}"
  if [[ ! -f "${src}" ]]; then
    echo "missing binary: ${src}" >&2
    exit 1
  fi
  install -m0755 "${src}" "${STAGE}/teleport/${name}"
}

need_bin teleport
need_bin tctl
need_bin tsh
need_bin tbot

UNIT_SRC="${ROOT_DIR}/examples/systemd/teleport.service"
if [[ ! -f "${UNIT_SRC}" ]]; then
  echo "missing systemd unit: ${UNIT_SRC}" >&2
  exit 1
fi
install -m0644 "${UNIT_SRC}" "${STAGE}/teleport/examples/systemd/teleport.service"

# Optional helpers shipped by upstream linux packages (harmless if present).
for optional in post-install before-remove; do
  if [[ -f "${ROOT_DIR}/examples/systemd/${optional}" ]]; then
    install -m0755 "${ROOT_DIR}/examples/systemd/${optional}" \
      "${STAGE}/teleport/examples/systemd/${optional}"
  fi
done

tar -C "${STAGE}" -czf "${OUT_DIR}/${TARBALL}" teleport

# Standalone binaries for GitHub Release convenience downloads.
install -m0755 "${BUILD_DIR}/teleport" "${OUT_DIR}/teleport-v${VERSION}-linux-amd64"
install -m0755 "${BUILD_DIR}/tctl" "${OUT_DIR}/tctl-v${VERSION}-linux-amd64"

# Install scripts fetch "${TARBALL}.sha256" (URL + ".sha256").
(
  cd "${OUT_DIR}"
  sha256sum "${TARBALL}" > "${TARBALL}.sha256"
  sha256sum "${TARBALL}" "teleport-v${VERSION}-linux-amd64" "tctl-v${VERSION}-linux-amd64" \
    > "teleport-v${VERSION}-linux-amd64.sha256"
)

echo "Wrote ${OUT_DIR}/${TARBALL}"
echo "Wrote ${OUT_DIR}/${TARBALL}.sha256"
