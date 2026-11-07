---
name: ratchet-report
description: 棘轮 worker（齿）完成任务、开好 draft PR 后写回传书时使用。五项必填，证据路径必须真实存在，写完自己跑 check-report。
---

# 写回传书（ratchet-report）

回传书的文字不算验收，验收看证据。但没有回传书、或缺项的回传书，主轴直接退回。

## 步骤

1. 从模板复制：`cp .ratchet/templates/report.md .ratchet/reports/<任务号>.md`，改 front matter 的 id、pr、worker。
2. 把证据放进 `.ratchet/evidence/<任务号>/`（这个目录不入库，cleanup 时不要删）。至少三类：
   - 命令与退出码（`commands.log`，每条命令一行，后面跟 `exit=<码>`）
   - 测试输出
   - JSON 输出 / 截图 / 录屏 / 日志 之一
3. 填五项：

| 项 | 写什么 |
|---|---|
| 做了什么 | 改了哪些文件、为什么；和任务书目标逐条对应 |
| 证据路径 | 每条一行、反引号包住路径；路径必须存在 |
| 失败尝试 | 试过但放弃的做法和原因；没有写「无」 |
| 绕路 | 为了绕开问题做的非常规处理（跳过某检查、临时 mock 等）；没有写「无」 |
| 未解决风险 | 你知道但没处理的问题；觉得某条红线规则需要改，也写在这里（只写建议，不动手） |

4. 自查：`python3 scripts/ratchet/ratchet.py check-report .ratchet/reports/<任务号>.md`，不过就补齐。

## 诚实

- 失败、绕路照实写。写「无」而实际有，比写出来更糟：verifier 会读 transcript 和 diff。
- 没跑过的检查不要写"通过"；CI 没出结果写「CI 待核」。
