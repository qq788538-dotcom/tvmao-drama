# .ratchet/

棘轮 Ratchet 的工作目录。

| 路径 | 内容 | 入库 | 谁能改 |
|---|---|---|---|
| `policy/` | G1–G6 关卡、批准记录、预算 | 是 | 只有 James（闸拦截 agent） |
| `templates/` | 任务书、回传书模板 | 是 | 自改进 PR，James 合并 |
| `briefs/` | 任务书 | 是 | 主轴 |
| `reports/` | 回传书 | 是 | worker |
| `state/` | notes.md、decisions.tsv、events.jsonl | 否 | 主轴、闸、脚本 |
| `evidence/<任务号>/` | 验收证据 | 否 | worker、verifier |
