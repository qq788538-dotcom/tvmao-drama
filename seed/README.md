# 内置种子数据

首次安装时导入 SQLite 的内置模型、配置与 Skill。
格式：每张表一个 JSONL 文件，每行一条记录，字段名与数据库列名一致。

| 文件 | 行数 | 说明 |
|---|---|---|
| `model_configs.jsonl` | 27 | 模型中心的内置模型（text 2 / image 7 / video 16 / audio 2）。`id` 被 `agent_tools.source_model_id` 引用，不要改动。`seedance-asset` 是隐藏模型，只用于合规校验 |
| `settings.jsonl` | 6 | 只含 `base_domain`、`canvas_light_control`、`canvas_multi_angle`、`canvas_postprocess_config`、`canvas_script_config`、`gen_image_prompt_presets` |
| `agent_backbone_models.jsonl` | 4 | Agent 使用的大模型（planner / utility / subagent_audio / subagent_video）。副本由 `scripts/sync-seed.sh` 同步到 `server/internal/agent/agentseed/data/` |
| `agent_tools.jsonl` | 4 | Agent 工具目录 |
| `skills.jsonl` | 10 | 官方 Skill（全部 `is_official=1`，其中 2 个 `is_published=0`） |
| `skill_categories.jsonl` | 4 | Skill 分类 |

未导出（有意排除）：
- 密钥类 settings：`upstream_api_key`、`upstream_access_token`、`drama_seedance_sd_key`
- 与画布无关的 settings：`drama_*`、`team_*`、`cli_release`、`flow_cli_release`、余额和配额类
- 所有用户数据表

重新导出时沿用同一范围；导出后须再次扫描确认不含密钥。

## 内置到 sidecar

`model_configs.jsonl`、`settings.jsonl` 由 sidecar 通过 `go:embed` 内置（`server/internal/builtin/seeddata/` 是它们的逐字节副本）。修改或重新导出后运行 `scripts/sync-seed.sh`；`go test ./internal/builtin/` 会检查副本与本目录一致。

## AI 角色库数据包（不随开源仓库分发）

AI 角色库的角色数据 `seedance_characters.jsonl` 和形象图（512px WebP，约 27MB）没有公开分发的授权，开源仓库里不包含，`.gitignore` 也已排除。没有数据包时 sidecar 照常构建运行，AI 角色库为空。

私有构建需要角色库时：
1. 把 `seedance_characters.jsonl` 放到本目录，把 `<asset_id>.webp` 放到 `server/internal/aicharacters/images/`。
2. 运行 `scripts/sync-seed.sh`，再构建。`go test ./internal/aicharacters/` 会检查每个角色都有对应的图。

`seedance_characters.jsonl` 由 `server/internal/aicharacters`（`seeddata/` 副本，同样由 `scripts/sync-seed.sh` 同步）在启动时导入 `seedance_characters` 表：哈希（含 `seedFormatVersion`）记在 `builtin_seed_state`（dataset=`ai_characters`），没变则跳过；变了按 `asset_id` upsert，种子里已删除的角色从表中删除。JSONL 里的 `created_at`/`updated_at` 不导入。
