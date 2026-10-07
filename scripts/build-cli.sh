#!/usr/bin/env bash
# 编译命令行工具到 src-tauri/binaries/miaoju-cli-<target-triple>（Tauri externalBin 命名约定），
# 并把 AI 助手说明复制到 src-tauri/resources/cli-skill/。
# 包内叫 miaoju-cli：主程序 mainBinaryName 已是 miaoju，同名会互相覆盖；安装到终端时再命名为 miaoju。
# 用法：bash scripts/build-cli.sh [target-triple]
set -euo pipefail
cd "$(dirname "$0")/.."

HOST="$(rustc -vV | sed -n 's/^host: //p')"
TARGET="${1:-$HOST}"
case "$TARGET" in
  aarch64-apple-darwin)   GOOS=darwin  GOARCH=arm64 EXT="" ;;
  x86_64-apple-darwin)    GOOS=darwin  GOARCH=amd64 EXT="" ;;
  x86_64-pc-windows-msvc) GOOS=windows GOARCH=amd64 EXT=".exe" ;;
  *) echo "不支持的 target：$TARGET" >&2; exit 1 ;;
esac
VERSION="$(node -p "require('./package.json').version")"
OUT="src-tauri/binaries/miaoju-cli-$TARGET$EXT"

mkdir -p src-tauri/binaries
( cd cli && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "../$OUT" ./cmd/miaoju )

rm -rf src-tauri/resources/cli-skill
mkdir -p src-tauri/resources
cp -R cli/skill src-tauri/resources/cli-skill

# 自检：本机平台的产物必须报出 package.json 的版本号（ldflags 没注入时是 dev）
if [ "$TARGET" = "$HOST" ]; then
  got="$("$OUT" version)"
  case "$got" in
    *"$VERSION"*) ;;
    *) echo "CLI 版本自检失败：输出 $got，期望包含 $VERSION" >&2; exit 1 ;;
  esac
fi
echo "CLI → ${OUT}（${VERSION}）"
