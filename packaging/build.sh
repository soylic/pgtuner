#!/bin/bash
# Build pgtuner packages with nfpm.
#   ./packaging/build.sh <version> <arch> [formats]
#   formats: space/comma list of deb,rpm (default: "deb rpm")
set -euo pipefail

VERSION="${1:?usage: build.sh <version> <arch> [formats]}"
ARCH="${2:-amd64}"
FORMATS="${3:-deb rpm}"
PKG_NAME="pgtuner"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"

cd "$REPO_DIR"

echo "Building ${PKG_NAME} ${VERSION} (${ARCH}) for: ${FORMATS}"
CGO_ENABLED=0 GOARCH="${ARCH}" go build \
    -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o "${PKG_NAME}" ./cmd/pgtuner
echo "Binary built: $(ls -lh ${PKG_NAME} | awk '{print $5}')"

export PKG_VERSION="${VERSION}"
export PKG_ARCH="${ARCH}"

mkdir -p dist
for fmt in ${FORMATS//,/ }; do
    nfpm package --packager "${fmt}" --config packaging/nfpm.yaml --target dist/
    echo "Built ${fmt}: $(ls -t dist/*.${fmt} | head -1)"
done
