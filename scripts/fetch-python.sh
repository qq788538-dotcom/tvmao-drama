#!/usr/bin/env bash
# 下载 python-build-standalone，校验 sha256，裁剪后放到 src-tauri/resources/python/。
# Tauri 把该目录作为资源打进 .app（Contents/Resources/python）；Windows zip 由 package-windows.sh 拷贝。
# 用法：scripts/fetch-python.sh [target-triple]，缺省为本机 triple。
set -euo pipefail
cd "$(dirname "$0")/.."

TARGET="${1:-$(rustc -vV | sed -n 's/^host: //p')}"
PBS_RELEASE=20261003
PY_VERSION=3.12.15
case "$TARGET" in
  aarch64-apple-darwin)   SHA=ad8d0c637c0a36b967b310e2c07254f4d2ca8cabaa7699e55ed6290aceb481a2 ;;
  x86_64-apple-darwin)    SHA=562c30864ece2cb1d3e0ad66a1acd498611a47e5a10ce81b99158bef1ccbd355 ;;
  x86_64-pc-windows-msvc) SHA=6fba7f2ae506facf41d457ea8293c7497910a675c69a4e954875169410a50402 ;;
  *) echo "不支持的目标平台: $TARGET" >&2; exit 1 ;;
esac

NAME="cpython-${PY_VERSION}+${PBS_RELEASE}-${TARGET}-install_only_stripped.tar.gz"
URL="https://github.com/astral-sh/python-build-standalone/releases/download/${PBS_RELEASE}/${NAME//+/%2B}"
DEST=src-tauri/resources/python
STAMP="$DEST/.stamp"

if [ -f "$STAMP" ] && [ "$(cat "$STAMP")" = "$NAME" ]; then
  echo "内置 Python 已是 ${NAME}，跳过"
  exit 0
fi

CACHE=out/cache/python
mkdir -p "$CACHE"
if [ ! -f "$CACHE/$NAME" ]; then
  curl -fL --retry 3 -o "$CACHE/$NAME.part" "$URL"
  mv "$CACHE/$NAME.part" "$CACHE/$NAME"
fi
GOT="$(shasum -a 256 "$CACHE/$NAME" | awk '{print $1}')"
if [ "$GOT" != "$SHA" ]; then
  echo "sha256 不匹配：期望 $SHA，实际 $GOT（已删除缓存，请重试）" >&2
  rm -f "$CACHE/$NAME"
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
tar -xzf "$CACHE/$NAME" -C "$TMP"
P="$TMP/python"

# 裁剪：Skill 校验脚本只用标准库（argparse/json/re/math/decimal/pathlib/typing），不需要 pip、GUI、测试集。
case "$TARGET" in
  *windows*)
    LIB="$P/Lib"
    rm -rf "$P/include" "$P/libs" "$P/Scripts" "$P/tcl" \
           "$P/DLLs/_tkinter.pyd" "$P/DLLs/tcl86t.dll" "$P/DLLs/tk86t.dll"
    ;;
  *)
    LIB="$P/lib/python3.12"
    rm -rf "$P/include" "$P/share" "$P"/lib/tcl8* "$P"/lib/tk8* "$P"/lib/itcl* "$P"/lib/thread* \
           "$P/lib/pkgconfig" "$P"/bin/idle3* "$P"/bin/pydoc3* "$P"/bin/pip* "$P"/bin/2to3*
    # 符号链接换成实体文件：bin/python3 -> python3.12。资源拷贝和 zip 不保证保留符号链接。
    REAL="$(readlink "$P/bin/python3")"
    rm -f "$P/bin/python3"
    mv "$P/bin/$REAL" "$P/bin/python3"
    find "$P/bin" -type l -delete
    ;;
esac
rm -rf "$LIB"/test "$LIB"/idlelib "$LIB"/tkinter "$LIB"/turtledemo "$LIB"/ensurepip \
       "$LIB"/lib2to3 "$LIB"/pydoc_data "$LIB"/site-packages/pip*

# macOS：每个 Mach-O 补一个 ad-hoc 签名（E5）。arm64 上未签名的可执行文件根本无法运行，
# 外层 .app 的签名也会因内部有未签名代码而失败。
case "$TARGET" in
  *apple-darwin)
    find "$P" -type f \( -perm -u+x -o -name '*.so' -o -name '*.dylib' \) -print0 |
      while IFS= read -r -d '' f; do
        if file -b "$f" | grep -q 'Mach-O'; then codesign --force --sign - "$f" >/dev/null; fi
      done
    ;;
esac

# 本机能跑的目标顺手冒烟：确认校验脚本用到的标准库都在。
HOST="$(rustc -vV | sed -n 's/^host: //p')"
if [ "$TARGET" = "$HOST" ]; then
  "$P/bin/python3" -E -s -B -X utf8 -c 'import argparse, json, re, math, decimal, pathlib, typing; print("内置 Python 冒烟通过")'
fi

mkdir -p "$DEST"
find "$DEST" -mindepth 1 -maxdepth 1 ! -name README.md -exec rm -rf {} +
cp -RL "$P"/. "$DEST"/
echo "$NAME" > "$STAMP"
echo "已准备内置 Python：${DEST}（$(du -sh "$DEST" | cut -f1)）"
