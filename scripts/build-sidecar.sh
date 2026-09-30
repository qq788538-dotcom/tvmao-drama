#!/usr/bin/env bash
# 把 Go sidecar 编译到 src-tauri/binaries/sidecar-<target-triple>[.exe]。
# Tauri 的 externalBin 要求文件名带 target triple 后缀。
set -euo pipefail
cd "$(dirname "$0")/.."

TARGET="${1:-$(rustc -vV | sed -n 's/^host: //p')}"
case "$TARGET" in
  aarch64-apple-darwin)   GOOS=darwin  GOARCH=arm64 EXT="" ;;
  x86_64-apple-darwin)    GOOS=darwin  GOARCH=amd64 EXT="" ;;
  x86_64-pc-windows-msvc) GOOS=windows GOARCH=amd64 EXT=".exe" ;;
  *) echo "不支持的目标平台: $TARGET" >&2; exit 1 ;;
esac

VERSION="$(node -p "require('./package.json').version")"
mkdir -p src-tauri/binaries
( cd server && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "../src-tauri/binaries/sidecar-$TARGET$EXT" ./cmd/sidecar )
echo "已生成 src-tauri/binaries/sidecar-$TARGET$EXT"
