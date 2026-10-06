---
name: real-person-short-drama-v1
title: 真人短剧v1-1.8倍
description: Use when converting a Chinese short-drama episode script into AI video storyboard JSON, including character, scene, and item image prompts, fixed-duration shot groups, dialogue-preserving cNN prompts, or pipeline-ready asset references.
output_artifact: candidate.json
---

# 真人短剧风格 v1

## 目标

把全剧设定（可选）与单集剧本转换为一个可由 AI 出图、出片管线直接消费的 JSON 对象。严格保留原台词，生成去重资产提示词，并让分镜时长、cNN 子镜头和 `@[类型-名称]` 引用闭环。

## 开始前

确认输入同时包含：

- 单集剧本正文；
- 分镜主切割时长 `DURATION_SEC`（整数秒，至少 4 秒）。

若缺少剧本正文，请求用户补充。若缺少 `DURATION_SEC`，只询问该参数；不要猜测。输入齐全后再执行工作流。

## 工作流

必须按编号顺序完整读取并执行阶段 01–07。不要只依据本文件的摘要生成结果。

必须先按原剧本顺序建立内部**语音账本**，然后才能执行资产提取和分镜切分。账本覆盖角色对白、OS／内心独白、具名与匿名 VO／旁白、画外音、系统／电子语音，以及广播、电话、录音等带明确文本的声音。`【音效】`、环境声和其他无台词声音不进入语音账本。

1. 完整读取 [输入归一化](references/01-input-normalization.md)，识别场景头、动作、对白、OS、OV、系统音和快闪。
2. 完整读取 [资产提取](references/02-asset-extraction.md)，建立去重的角色、场景、物品清单及生图提示词。
3. 完整读取 [时长核算与分镜切分](references/03-duration-and-storyboard-splitting.md)，先做总时长闸门，再切功能节拍、分镜和 cNN 子镜头。
4. 完整读取 [镜头编写与导演控制](references/04-shot-writing-and-directing.md)，写空间、姿态、景别、机位、运镜和可见动作。
5. 完整读取 [台词与声音处理](references/05-dialogue-and-audio.md)，逐字保留台词并处理 OS、OV、VO、画外音和系统音。
6. 完整读取 [标签与风格控制](references/06-tags-and-style-control.md)，应用固定风格串、背景虚焦、无文字字幕和配额。
7. 完整读取 [JSON 组装与质量闸门](references/07-json-assembly-and-quality-gates.md)，按精确 schema 组装并完成语义自检和脚本校验。

输出候选 JSON 前，先用语音账本执行**正向对账**与**反向对账**：账本中每项必须按原顺序恰好映射到输出，输出中每段有文本语音也必须能回溯到账本。账本编号仅用于内部推理，不得写入最终 JSON。

仅在输入格式复杂、切分边界不明确或需要比对成品时读取 [输入输出示例](references/08-examples.md)。示例不覆盖阶段 01–07 的规则。

## 校验循环

1. 将候选 JSON 写入当前 Skill Run 工作目录下的相对路径 `candidate.json`。
   - 必须使用相对路径 `candidate.json`；
   - 禁止使用 `/tmp`；
   - 禁止使用任何绝对路径；
   - 禁止使用 `../` 路径。
2. 从本 skill 目录运行：

```bash
python3 scripts/validate_storyboard.py candidate.json --duration-sec <DURATION_SEC>
```

3. 根据全部 `ERROR:` 修正 JSON，再次运行校验器。
4. 重复直到命令退出码为 0 且输出 `OK: storyboard JSON is valid`。

脚本只验证确定性结构约束，不能替代台词零改写、剧情顺序、导演细节、节奏与风格配额的人工语义复核。运行脚本前先逐项完成阶段 07 的质量闸门。

## 最终输出

校验通过后，调用 `submit_artifact` 工具提交精确相对路径 `candidate.json`；最终产物只通过该工具交付：

- 不要在回复文本中复制 JSON 本体或其任何片段；
- 不加解释、校验日志或临时文件路径。

不得提交尚未通过校验的候选 JSON。
