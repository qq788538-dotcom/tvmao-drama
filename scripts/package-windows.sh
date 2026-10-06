#!/usr/bin/env bash
# 在 macOS 上交叉编译 Windows x64 版本并打成 zip（解压即用，不含安装程序，spec E4）。
# 依赖：rustup target x86_64-pc-windows-msvc、cargo-xwin、node、python3。
set -euo pipefail
cd "$(dirname "$0")/.."

TARGET=x86_64-pc-windows-msvc
bash scripts/version.sh check
VERSION="$(node -p "require('./package.json').version")"

bash scripts/prebuild.sh "$TARGET"
cargo tauri build --runner cargo-xwin --target "$TARGET" --no-bundle

REL="src-tauri/target/$TARGET/release"
for f in miaoju.exe sidecar.exe; do
  [ -f "$REL/$f" ] || { echo "缺少构建产物 $REL/$f" >&2; exit 1; }
done

NAME="miaoju-windows-x64-$VERSION"
STAGE="out/$NAME"
ZIP="out/$NAME.zip"
rm -rf "$STAGE" "$ZIP"
mkdir -p "$STAGE"
cp "$REL/miaoju.exe" "$REL/sidecar.exe" "$STAGE/"

# --no-bundle 不处理 bundle.resources：按 {源: 目标} 映射复制到 exe 同目录
while IFS=$'\t' read -r src dest; do
  [ -n "$src" ] || continue
  if [ -d "src-tauri/$src" ]; then
    mkdir -p "$STAGE/$dest"
    cp -R "src-tauri/$src/." "$STAGE/$dest/"
  else
    mkdir -p "$(dirname "$STAGE/$dest")"
    cp "src-tauri/$src" "$STAGE/$dest"
  fi
done < <(node scripts/lib/list-resources.mjs src-tauri/tauri.conf.json)

# 说明文件：UTF-8 BOM + CRLF，老版本记事本打开也不乱码
{ printf '\xEF\xBB\xBF'; sed 's/$/\r/' "packaging/首次打开说明-Windows.txt"; } > "$STAGE/首次打开说明.txt"

python3 scripts/lib/make_zip.py "$STAGE" "$ZIP"
echo "已生成 ${ZIP}（$(du -h "$ZIP" | cut -f1)）"
