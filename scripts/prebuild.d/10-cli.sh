#!/usr/bin/env bash
# prebuild 钩子：编译命令行工具并复制 skill（scripts/prebuild.sh 按文件名顺序调用，参数为 target-triple）。
set -euo pipefail
exec bash "$(dirname "$0")/../build-cli.sh" "$1"
