#!/usr/bin/env bash
# 打包前准备：编译对应平台的 sidecar，再按文件名顺序执行 scripts/prebuild.d/*.sh <target-triple>
# （其他阶段的资源准备钩子，例如 P6 的 Python 运行时）。
# 用法：bash scripts/prebuild.sh <target-triple>
set -euo pipefail
cd "$(dirname "$0")/.."

TARGET="${1:?用法: bash scripts/prebuild.sh <target-triple>}"
bash scripts/build-sidecar.sh "$TARGET"

shopt -s nullglob
for hook in scripts/prebuild.d/*.sh; do
  echo "==> $hook $TARGET"
  bash "$hook" "$TARGET"
done
