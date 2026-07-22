#!/usr/bin/env bash
# Fast gate: package a dummy release tarball and assert the layout install.sh needs.
# Runs in seconds — no buildbox / make full required.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

VERSION="0.0.0-test"
BUILD_DIR="${WORK}/build"
OUT_DIR="${WORK}/out"
mkdir -p "${BUILD_DIR}" "${OUT_DIR}"

for bin in teleport tctl tsh tbot; do
  printf '#!/bin/sh\necho dummy-%s\n' "${bin}" > "${BUILD_DIR}/${bin}"
  chmod +x "${BUILD_DIR}/${bin}"
done

ROOT_DIR="${ROOT_DIR}" BUILD_DIR="${BUILD_DIR}" OUT_DIR="${OUT_DIR}" VERSION="${VERSION}" \
  bash "${ROOT_DIR}/deploy/package-release-tarball.sh"

TARBALL="${OUT_DIR}/teleport-v${VERSION}-linux-amd64-bin.tar.gz"
test -f "${TARBALL}"
test -f "${TARBALL}.sha256"
test -f "${OUT_DIR}/teleport-v${VERSION}-linux-amd64.sha256"

EXTRACT="${WORK}/extract"
mkdir -p "${EXTRACT}"
tar -xzf "${TARBALL}" -C "${EXTRACT}"

REQUIRED=(
  "teleport/teleport"
  "teleport/tctl"
  "teleport/tsh"
  "teleport/tbot"
  "teleport/examples/systemd/teleport.service"
)

for path in "${REQUIRED[@]}"; do
  if [[ ! -e "${EXTRACT}/${path}" ]]; then
    echo "FAIL: tarball missing required path: ${path}" >&2
    echo "Tarball contents:" >&2
    tar -tzf "${TARBALL}" >&2
    exit 1
  fi
done

# Checksum file must name the tarball so `sha256sum -c` works after download.
if ! grep -q "teleport-v${VERSION}-linux-amd64-bin.tar.gz$" "${TARBALL}.sha256"; then
  echo "FAIL: ${TARBALL}.sha256 does not reference the tarball filename" >&2
  cat "${TARBALL}.sha256" >&2
  exit 1
fi

# Simulate install.sh checksum step (cwd = download dir).
(
  cd "${OUT_DIR}"
  sha256sum --status -c "${TARBALL}.sha256"
)

echo "OK: release tarball layout + checksum validated"
