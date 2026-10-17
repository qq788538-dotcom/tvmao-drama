#!/usr/bin/env bash
# 把脚本节点用到的 Skill 从 HUABU_SKILLS_DIR 复制到 server/internal/skillbundle/skills（go:embed 编进 sidecar）。
# 名单与 seed/settings.jsonl 中 canvas_script_config.styles[].skillName 一致；风格配置变了就重跑并提交。
set -euo pipefail
cd "$(dirname "$0")/.."

SRC="${HUABU_SKILLS_DIR:?请用 HUABU_SKILLS_DIR 指定 Skill 源目录}"
DEST=server/internal/skillbundle/skills
SKILLS=(
  real-person-short-drama-v3
  real-person-narration-storyboard-v1
  real-person-short-drama-v2
  real-person-short-drama-v1
  3d-animation-style-v2
  3d-animation-style-v1
  novel-real-person-short-drama-v1
  novel-3d-animation-style-v1
)

rm -rf "$DEST"
mkdir -p "$DEST"
for s in "${SKILLS[@]}"; do
  [ -f "$SRC/$s/SKILL.md" ] || { echo "缺少 $SRC/$s/SKILL.md" >&2; exit 1; }
  rsync -a --exclude 'tests/' --exclude 'agents/' --exclude '.DS_Store' \
        --exclude '__pycache__/' --exclude '*.pyc' "$SRC/$s/" "$DEST/$s/"
done
echo "已同步 ${#SKILLS[@]} 个 Skill 到 $DEST"
