#!/usr/bin/env bash
# Cross-compiles the subscription-service binary for linux/amd64 and
# linux/arm64 into dist/. Run from anywhere; paths are resolved relative to
# the repo root.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT_DIR="$ROOT_DIR/dist"
LDFLAGS="-s -w -X main.version=${VERSION}"

mkdir -p "$OUT_DIR"

for target in "linux/amd64" "linux/arm64"; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  OUT="$OUT_DIR/subscription-service-${GOOS}-${GOARCH}"

  echo "==> building ${GOOS}/${GOARCH} -> ${OUT}"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
    -trimpath \
    -ldflags "$LDFLAGS" \
    -o "$OUT" \
    ./cmd/subscription-service
done

echo "==> done. binaries in $OUT_DIR"
