#!/bin/sh
set -e

REPO="lucinate-ai/lucinate"
BINARY="lucinate"
INSTALL_DIR="/usr/local/bin"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

EXT="tar.gz"
if [ "$OS" = "windows" ]; then
  EXT="zip"
fi

TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
VERSION="${TAG#v}"

ASSET="${BINARY}_${VERSION}_${OS}_${ARCH}.${EXT}"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"

TMPDIR_PATH=$(mktemp -d)
trap 'rm -rf "$TMPDIR_PATH"' EXIT

echo "Downloading ${BINARY} ${VERSION} for ${OS}/${ARCH}..."
curl -fsSL -o "${TMPDIR_PATH}/${ASSET}" "$URL"
tar -xzf "${TMPDIR_PATH}/${ASSET}" -C "$TMPDIR_PATH"

if [ -w "${INSTALL_DIR}" ]; then
  install -m 755 "${TMPDIR_PATH}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo install -m 755 "${TMPDIR_PATH}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
fi

echo "${BINARY} ${VERSION} installed to ${INSTALL_DIR}/${BINARY}"
