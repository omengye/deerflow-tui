#!/usr/bin/env bash
set -euo pipefail

# Go + Bubble Tea build script.
# Usage: ./scripts/build-go.sh [version] [os] [arch]
# Example: ./scripts/build-go.sh v1.0.0 linux amd64

VERSION="${1:-dev}"
GOOS="${2:-$(go env GOOS)}"
GOARCH="${3:-$(go env GOARCH)}"

BUILD_DIR="dist-go"
BINARY_NAME="deerflow-tui"
LDFLAGS="-s -w -X main.Version=${VERSION}"

printf 'Building deerflow-tui (Go + Bubble Tea)...\n'
printf '  Version: %s\n' "${VERSION}"
printf '  OS:      %s\n' "${GOOS}"
printf '  Arch:    %s\n' "${GOARCH}"

mkdir -p "${BUILD_DIR}"

OUTPUT="${BUILD_DIR}/${BINARY_NAME}"
if [[ "${GOOS}" == "windows" ]]; then
    OUTPUT="${OUTPUT}.exe"
fi

printf 'Compiling...\n'
CGO_ENABLED="${CGO_ENABLED:-0}" GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${OUTPUT}" ./cmd/deerflow-tui

printf 'Built: %s\n' "${OUTPUT}"
ls -lh "${OUTPUT}"

if [[ "${VERSION}" != "dev" ]]; then
    output_base="$(basename "${OUTPUT}")"
    (cd "${BUILD_DIR}" && sha256sum "${output_base}" > "${output_base}.sha256")
    printf 'Checksum created: %s/%s.sha256\n' "${BUILD_DIR}" "${output_base}"
fi
