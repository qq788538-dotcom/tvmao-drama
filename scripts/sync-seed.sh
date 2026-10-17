#!/usr/bin/env bash
# 把仓库根 seed/ 里由 sidecar 内置的种子文件复制到 go:embed 目录。seed/ 是唯一真源。
# P2b 增加内置文件时把文件名加进下面的列表。
set -euo pipefail
cd "$(dirname "$0")/.."
for f in model_configs.jsonl settings.jsonl; do
  cp "seed/$f" "server/internal/builtin/seeddata/$f"
done
cp seed/agent_backbone_models.jsonl server/internal/agent/agentseed/data/agent_backbone_models.jsonl
# AI 角色库数据包可选，不随开源仓库分发。
if [ -f seed/seedance_characters.jsonl ]; then
  cp seed/seedance_characters.jsonl server/internal/aicharacters/seeddata/seedance_characters.jsonl
fi
echo "已同步内置种子到 server/internal/builtin/seeddata/"
