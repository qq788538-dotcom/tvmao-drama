#!/usr/bin/env bash
# 在 macOS 上交叉编译 Windows x64 版本并打成 zip（解压即用，不含安装程序）。
# 依赖：rustup target x86_64-pc-windows-msvc、cargo-xwin。
set -euo pipefail
cd "$(dirname "$0")/.."

TARGET=x86_64-pc-windows-msvc
VERSION="$(node -p "require('./package.json').version")"

scripts/build-sidecar.sh "$TARGET"
cargo tauri build --runner cargo-xwin --target "$TARGET" --no-bundle

REL="src-tauri/target/$TARGET/release"
for f in miaoju.exe sidecar.exe; do
  [ -f "$REL/$f" ] || { echo "缺少构建产物 $REL/$f" >&2; exit 1; }
done

STAGE="out/miaoju-windows-x64-$VERSION"
rm -rf "$STAGE" "$STAGE.zip"
mkdir -p "$STAGE"
cp "$REL/miaoju.exe" "$REL/sidecar.exe" "$STAGE/"
( cd out && zip -r "miaoju-windows-x64-$VERSION.zip" "miaoju-windows-x64-$VERSION" )
echo "已生成 $STAGE.zip"
