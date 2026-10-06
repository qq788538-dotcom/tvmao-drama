#!/usr/bin/env bash
# 从 1024×1024 设计稿生成 Tauri 全套图标（src-tauri/icons/）。
# 用法：bash scripts/gen-icons.sh [源图，默认 packaging/icon-1024.png]
set -euo pipefail
cd "$(dirname "$0")/.."

SRC="${1:-packaging/icon-1024.png}"
[ -f "$SRC" ] || { echo "找不到图标源文件：$SRC" >&2; exit 1; }
W="$(sips -g pixelWidth "$SRC" | awk '/pixelWidth/{print $2}')"
H="$(sips -g pixelHeight "$SRC" | awk '/pixelHeight/{print $2}')"
[ "$W" = 1024 ] && [ "$H" = 1024 ] || { echo "图标源必须是 1024×1024，实际 ${W}×${H}" >&2; exit 1; }

cargo tauri icon "$SRC" -o src-tauri/icons
echo "图标已生成到 src-tauri/icons/"
