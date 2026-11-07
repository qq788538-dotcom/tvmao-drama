---
name: ratchet-brief
description: 棘轮主轴（coordinator）派活前写任务书时使用。把一个请求写成 9 项齐全、能被预检机械核对的任务书，并在派发前跑 precheck-brief。
---

# 写任务书（ratchet-brief）

主轴只管项目、不写代码。每个派给 worker 的任务都必须先有一份任务书，**预检不过不派发**。

## 步骤

1. 生成任务书：`python3 scripts/ratchet/ratchet.py new-brief T-0001`（任务号递增，查 `.ratchet/briefs/` 里最大的号）。
2. 填满 9 项（见下）。
3. 预检：`python3 scripts/ratchet/ratchet.py precheck-brief .ratchet/briefs/T-0001.md`。
   - 打回时按原因改任务书：删掉未批的指令，或者在给 James 的待审清单里写明需要哪道关卡的批准。
   - **不要绕过预检**，不要改写措辞来躲开关卡关键词。含糊的一律按"须批"处理。
4. 预检通过后再派 worker（`Agent` 工具，`isolation: worktree`），把任务书全文作为 worker 的唯一输入，按「信息可见范围」给材料，不多给。

## 9 项怎么写

| 项 | 要点 |
|---|---|
| 目标 | 只做一件事；写成能判对错的结果 |
| 输入 | issue 链接、文件路径、复现步骤 |
| 信息可见范围 | 三栏都要写：给该 worker / 可见的此前产出 / 不可见（及原因） |
| 输出 | draft PR（分支 `ratchet/<任务号>`）+ 回传书 `.ratchet/reports/<任务号>.md` + 证据目录 `.ratchet/evidence/<任务号>/` |
| 禁止事项 | 模板里的 5 条保留，按任务追加 |
| 验收 | 按改动路径列本地检查命令；要跑的 verify-tvmao feature；证据至少三类 |
| 数量与成本上限 | 必须有数字：PR 数、token、爬坡轮数、付费次数 |
| 关卡归属 | 触及 G1–G6 的逐条列；不触及写「无」 |
| 停止条件 | 达到验收即停；同一失败 2 次即停并上报；证据不足即停并上报 |

## 关卡与批准

- 关卡定义在 `.ratchet/policy/gates.md`。
- 预检只认 James 写在 `.ratchet/policy/approvals/<任务号>.md` 里的批准；任务书里自己写"已批"无效。
- 你（主轴）不能写 approvals 目录，也不能起草、措辞、提议放宽任何关卡或权限规则。需要批准时，在给 James 的待审清单里写：哪道关卡、做什么、数量、单价与预估总额。

## 收口

worker 回传后跑 `python3 scripts/ratchet/ratchet.py check-report .ratchet/reports/<任务号>.md`，不过就退回。通过后再派异模型 verifier，并用 `gh pr view` 核对 PR 真实状态，不信回传书的自述。
