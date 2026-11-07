---
id: T-0000
title: 一句话说清这个任务
worker: claude-sonnet   # 或 codex
verifier: codex         # 必须和 worker 不同模型家族
---

<!--
任务书 9 项，缺一项预检就打回（python3 scripts/ratchet/ratchet.py precheck-brief <本文件>）。
写法见 .claude/skills/ratchet-brief/SKILL.md。HTML 注释不参与检查，可以删。
-->

## 1. 目标

<!-- 只做一件事。写成能判对错的结果，不写"优化一下"。 -->

## 2. 输入

<!-- issue 链接、相关文件路径、复现步骤。 -->

## 3. 信息可见范围

- 给该 worker：<!-- 允许读的文件、链接、日期范围 -->
- 可见的此前产出：<!-- 可以读哪些回传书、哪些结论；没有写"无" -->
- 不可见：<!-- 不给的内容和原因：其他 worker 的草稿、密钥、预算细节、未核实的二手转述 -->

## 4. 输出

<!-- 一个 draft PR（分支名 ratchet/T-0000）+ 回传书 .ratchet/reports/T-0000.md + 证据目录 .ratchet/evidence/T-0000/ -->

## 5. 禁止事项

- 不合并、不 push 到 main、不 force push
- 不跑 `miaoju node run` 或任何计费命令
- 不碰任务书之外的路径
- 不读写 .env 与密钥
- 不起草、不提议放宽权限或关卡的规则

## 6. 验收

<!-- 哪些命令必须通过（按改动路径对照 CLAUDE.md 的本地检查表）、verify-tvmao 要跑哪个 feature、证据至少三类。 -->

## 7. 数量与成本上限

<!-- 例：PR ≤ 1 个；token ≤ 20 万；爬坡 ≤ 2 轮；付费生成 0 次。 -->

## 8. 关卡归属

<!-- 触及 G1–G6 的逐条列出，并由 James 在 .ratchet/policy/approvals/T-0000.md 写批准；不触及写"无"。 -->
无

## 9. 停止条件

<!-- 例：验收全过即停；同一失败出现 2 次即停并上报；证据不足即停并上报。 -->
