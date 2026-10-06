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

# 内置 Python 与 sidecar 同目标（脚本节点 X6）。已就绪时 fetch-python.sh 自己会跳过。
# - MIAOJU_SKIP_PYTHON=1：完全跳过（离线开发、CI 只编 sidecar）。
# - 下载/校验失败：只警告，sidecar 照常可用；但打包脚本会用 scripts/lib/require-python.sh 拦住没有内置 Python 的情况。
if [ "${MIAOJU_SKIP_PYTHON:-}" = "1" ]; then
  echo "MIAOJU_SKIP_PYTHON=1：跳过内置 Python 准备（打包前必须先运行 scripts/fetch-python.sh ${TARGET}）"
elif ! bash scripts/fetch-python.sh "$TARGET"; then
  echo "警告：内置 Python 准备失败，已继续（sidecar 已生成）。" >&2
  echo "警告：开发运行时脚本节点会找不到内置 Python；打包前必须先成功运行 scripts/fetch-python.sh ${TARGET}，否则打包脚本会报错退出。" >&2
fi
