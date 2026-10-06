# 内置种子数据

来源：参考仓生产库（`tapnow`，生产实例 `tapnow-api-blue`），2026-09-30 只读导出。
格式：每张表一个 JSONL 文件，每行一条记录，字段名与生产库列名一致。

| 文件 | 行数 | 说明 |
|---|---|---|
| `model_configs.jsonl` | 28 | 模型中心（text 4 / image 6 / video 16 / audio 2；2026-10-06 删除 19 个：gpt-image-2.1（id 29）、工作流专用与停用的 id 2、3、5、6、7、8、14、40、45，以及 cm-seedance 系列 id 21–26、32–34）。保留原始 `id`，`agent_tools.source_model_id` 引用它 |
| `settings.jsonl` | 6 | 只含 `base_domain`、`canvas_light_control`、`canvas_multi_angle`、`canvas_postprocess_config`、`canvas_script_config`、`gen_image_prompt_presets` |
| `agent_backbone_models.jsonl` | 4 | Agent 使用的大模型（planner / utility / subagent_audio / subagent_video） |
| `agent_tools.jsonl` | 4 | Agent 工具目录 |
| `skills.jsonl` | 10 | 官方 Skill（全部 `is_official=1`，其中 2 个 `is_published=0`） |
| `skill_categories.jsonl` | 4 | Skill 分类 |
| `seedance_characters.jsonl` | 1729 | AI 角色库；`image_url` 是 `media://tapnow/sd-assets/…`，图片在生产 TOS 上 |

未导出（有意排除）：
- 密钥类 settings：`upstream_api_key`、`upstream_access_token`、`drama_seedance_sd_key`
- 与画布无关的 settings：`drama_*`、`team_*`、`cli_release`、`flow_cli_release`、余额和配额类
- `director_library_categories`、`director_library_models`：生产库为空
- 所有用户数据表

重新导出时沿用同一范围；导出后须再次扫描确认不含密钥。

## 内置到 sidecar

`model_configs.jsonl`、`settings.jsonl` 由 sidecar 通过 `go:embed` 内置（`server/internal/builtin/seeddata/` 是它们的逐字节副本）。修改或重新导出后运行 `scripts/sync-seed.sh`；`go test ./internal/builtin/` 会检查副本与本目录一致。

`seedance_characters.jsonl` 由 `server/internal/aicharacters`（`seeddata/` 副本，同样由 `scripts/sync-seed.sh` 同步）在启动时导入 `seedance_characters` 表：哈希（含 `seedFormatVersion`）记在 `builtin_seed_state`（dataset=`ai_characters`），没变则跳过；变了按 `asset_id` upsert，种子里已删除的角色从表中删除。JSONL 里的 `created_at`/`updated_at` 不导入。
