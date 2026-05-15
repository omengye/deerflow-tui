#!/usr/bin/env bash
set -euo pipefail

OUT_DIR="${OUT_DIR:-dist-sea}"
BIN_NAME="${BIN_NAME:-deerflow-tui}"

NODE_PATH="${NODE_BIN:-$(command -v node)}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_DIR="${ROOT_DIR}/${OUT_DIR}"
TARGET_BIN="${TARGET_DIR}/${BIN_NAME}"
BLOB_PATH="${TARGET_DIR}/deerflow-tui.blob"
POSTJECT_PATH="${ROOT_DIR}/node_modules/.bin/postject"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "package-linux-sea.sh must run on Linux." >&2
  exit 1
fi

if [[ ! -f "${BLOB_PATH}" ]]; then
  echo "SEA blob not found: ${BLOB_PATH}" >&2
  exit 1
fi

if [[ ! -x "${POSTJECT_PATH}" ]]; then
  echo "postject not found. Run: pnpm install" >&2
  exit 1
fi

cp "${NODE_PATH}" "${TARGET_BIN}"

"${POSTJECT_PATH}" \
  "${TARGET_BIN}" \
  NODE_SEA_BLOB \
  "${BLOB_PATH}" \
  --sentinel-fuse NODE_SEA_FUSE_fce680ab2cc467b6e072b8b5df1996b2

chmod +x "${TARGET_BIN}"

echo "Created ${TARGET_BIN}"
