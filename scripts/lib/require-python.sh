#!/usr/bin/env bash
# 打包前检查：src-tauri/resources/python 里必须是 <target> 对应的内置 Python（fetch-python.sh 生成）。
# build-sidecar.sh 在下载失败或 MIAOJU_SKIP_PYTHON=1 时只警告不中断，所以打包脚本必须在这里拦住，
# 否则会打出一个没有 Python、脚本节点跑不起来的包。
# 用法：bash scripts/lib/require-python.sh <target-triple>
set -euo pipefail
cd "$(dirname "$0")/../.."

TARGET="${1:?用法: bash scripts/lib/require-python.sh <target-triple>}"
DEST=src-tauri/resources/python
case "$TARGET" in
  *windows*) EXE="$DEST/python.exe" ;;
  *)         EXE="$DEST/bin/python3" ;;
esac

fail() {
  echo "错误：$1" >&2
  echo "打包需要内置 Python，请先在联网环境运行：bash scripts/fetch-python.sh $TARGET" >&2
  exit 1
}

[ -f "$DEST/.stamp" ] || fail "未检测到内置 Python（缺少 $DEST/.stamp）"
STAMP="$(cat "$DEST/.stamp")"
case "$STAMP" in
  *"-$TARGET-"*) ;;
  *) fail "内置 Python 的目标平台不对：.stamp 是 ${STAMP}，需要 ${TARGET}" ;;
esac
[ -f "$EXE" ] || fail "内置 Python 不完整：缺少 $EXE"
echo "内置 Python 就绪：${STAMP}"
