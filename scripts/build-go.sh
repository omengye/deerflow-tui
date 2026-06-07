#!/usr/bin/env bash
set -euo pipefail

# Go + Bubble Tea 版本构建脚本
# 用法: ./scripts/build-go.sh [version] [os] [arch]
# 示例: ./scripts/build-go.sh 0.1.0 linux amd64

VERSION="${1:-dev}"
GOOS="${2:-$(go env GOOS)}"
GOARCH="${3:-$(go env GOARCH)}"

BUILD_DIR="dist-go"
BINARY_NAME="deerflow-tui"
LDFLAGS="-ldflags=-X main.Version=${VERSION}"

echo "Building deerflow-tui (Go + Bubble Tea)..."
echo "  Version: ${VERSION}"
echo "  OS:      ${GOOS}"
echo "  Arch:    ${GOARCH}"

mkdir -p "${BUILD_DIR}"

OUTPUT="${BUILD_DIR}/${BINARY_NAME}"
if [[ "${GOOS}" == "windows" ]]; then
    OUTPUT="${OUTPUT}.exe"
fi

echo "Compiling..."
GOOS="${GOOS}" GOARCH="${GOARCH}" go build ${LDFLAGS} -o "${OUTPUT}" ./cmd/deerflow-tui

if [[ $? -ne 0 ]]; then
    echo "Build failed!"
    exit 1
fi

echo "Built: ${OUTPUT}"
ls -lh "${OUTPUT}"

# 如果是发布版本，创建校验和
if [[ "${VERSION}" != "dev" ]]; then
    (cd "${BUILD_DIR}" && sha256sum "$(basename ${OUTPUT})" > "$(basename ${OUTPUT}).sha256")
    echo "Checksum created: ${BUILD_DIR}/$(basename ${OUTPUT}).sha256"
fi