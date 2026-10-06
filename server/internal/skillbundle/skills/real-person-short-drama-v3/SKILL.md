---
name: real-person-short-drama-v3
title: 真人短剧v3
description: Use when converting a Chinese short-drama episode script into project-compatible AI video storyboard JSON, including character, scene, and item image prompts, fixed-duration shot groups, dialogue-preserving Seedance 2.0 镜头N prompts, professional shot-scale and camera decisions, blocking, composition, lighting continuity, a coherent directorial spine, or splice-ready continuity between independently generated video segments.
output_artifact: candidate.json
---

# 真人短剧风格 v3

## 目标

把全剧设定（可选）与单集剧本转换为一个可由当前项目 AI 出图、出片管线直接消费的 JSON 对象。严格保留原台词，生成去重资产提示词；内部用 cNN 账本完成时长、意图、景别、运镜起落点，并维护人物动线、构图、光线与氛围的场级连续状态及风险—约束账本。对跨多个独立片段或空间关系脆弱的同场戏，额外生成兼容现有 `scene` schema 的 `空间母版-场景名` 资产，把座位、轴线、门窗、关键道具归属和光向固化为一张参考图。最终将每个 `storyboard[].prompt` 编译为 Seedance 2.0 友好的“空间层 + 时间层”顺序分镜：每条 `镜头N` 先形成一幅可见画面，再描述动作、运镜结果与声音，同时让 `@[类型-名称]` 引用闭环。

## 开始前

确认输入同时包含：

- 单集剧本正文；
- 分镜主切割时长 `DURATION_SEC`（整数秒，至少 4 秒）。

若缺少剧本正文，请求用户补充。若缺少 `DURATION_SEC`，只询问该参数；不要猜测。输入齐全后再执行工作流。

## 工作流

必须按编号顺序完整读取并执行阶段 01–07。不要只依据本文件的摘要生成结果。

必须先按原剧本顺序建立内部**语音账本**，然后才能执行资产提取和分镜切分。账本覆盖角色对白、OS／内心独白、具名与匿名 VO／旁白、画外音、系统／电子语音，以及广播、电话、录音等带明确文本的声音。`【音效】`、环境声和其他无台词声音不进入语音账本。

切分完成后、编写模型 prompt 前，必须建立内部**镜头衔接账本**：先锁定整集唯一导演声部，并为每个场建立空间轴线、人物动线、构图重心、物理主光与氛围的**场级视觉基线**；为每个 storyboard 标记弧线位置、景别趋势、运镜趋势和唯一叙事任务；为每个内部 cNN 记录一个可见意图、起幅、落幅、完整摄影机物理契约、主表演者起点—路径—终点与稳定锚点，同时记录可能破坏本镜意图或连续性的风险及优先正向表达的局部约束；再为相邻 storyboard 编号 `B001、B002…`，记录出点、入点、切点类型、轴线、人物世界锚点、按需画面坐标、视线、道具状态、光线相位与环境声底。账本仅用于规划和对账，不另增 JSON 字段；`Bnnn` 编号不发送给 Seedance，最终 prompt 只使用阶段 04 的自然剪辑短语。

1. 完整读取 [输入归一化](references/01-input-normalization.md)，识别场景头、动作、对白、OS、OV、系统音和快闪。
2. 完整读取 [资产提取](references/02-asset-extraction.md)，建立去重的角色、场景、物品清单及生图提示词；需要空间母版时仍把它作为普通 `scene` 资产输出，不新增项目 schema 字段。
3. 完整读取 [时长核算与分镜切分](references/03-duration-and-storyboard-splitting.md)，先做总时长闸门，再切功能节拍、独立生成片段和 cNN 子镜头，并建立镜头衔接账本。
4. 先完整读取 [景别与运镜导演知识库](references/04a-shot-scale-and-camera-library.md)，按戏剧功能建立整集镜头弧线；再完整读取 [调度、构图与光线连续性](references/04b-blocking-composition-lighting.md)，为每条镜头建立不可省略的空间层画面基底，并维护场级视觉基线和稀疏变化量；完整读取 [约束与负面提示词编译](references/04c-constraint-and-negative-prompt-compiler.md)，先用正向状态锁解决具体风险，只在已完成动作或未来事件需要排除时保留短局部约束；最后完整读取 [镜头编写与 Seedance 2.0 提示词编译](references/04-shot-writing-and-directing.md)，把“一镜一意图、一起幅、一落幅、一个主运镜”的内部决策编译为模型友好的 `镜头N：景别，机位，标准运镜短语。画面中……。主体动作与结果。[必要的一条视觉变化量或局部约束。]声音。`，并让相邻独立生成片段形成可剪辑切点。
5. 完整读取 [台词与声音处理](references/05-dialogue-and-audio.md)，逐字保留台词并处理 OS、OV、VO、画外音和系统音。
6. 完整读取 [标签与风格控制](references/06-tags-and-style-control.md)，应用固定风格串、叙事性焦点、无文字字幕和安全头尾；不把背景虚焦或特殊手法当作硬配额。
7. 完整读取 [JSON 组装与质量闸门](references/07-json-assembly-and-quality-gates.md)，按精确 schema 组装并完成语义自检和脚本校验。

输出候选 JSON 前，先用语音账本执行**正向对账**与**反向对账**：账本中每项必须按原顺序恰好映射到输出，输出中每段有文本语音也必须能回溯到账本。账本编号仅用于内部推理，不得写入最终 JSON。

随后用镜头衔接账本执行**导演弧线对账**、**空间层对账**、**时间层对账**、**视觉连续对账**、**镜头契约对账**、**约束预算对账**与**边界对账**：内部每条 cNN 必须有非空意图、起幅、落幅、唯一主运镜、运镜时机、主表演者动线和稳定锚点；最终每条 `镜头N` 必须写出可见主体、`@[场景-名称]` 所对应的具体可见区域，以及人物／物件相对入口、窗、桌轴、座位、门框、彼此或前后景的空间关系，不能只剩动作。**世界坐标强锁，画面坐标按镜选用**：入口侧／窗侧、座位、道具归属和光源方向跨镜继承；`画左／画右` 只在当前构图、横向运动、遮挡揭示或匹配剪辑确实需要时使用，不得把某角色“永远固定在画左”当作跨反打连续性。参考资产承载身份细节和场景全貌，不替代本镜构图。同一逻辑场跨两个以上 storyboard 时，每段必须引用同一个 `@[场景-空间母版-场景名]`，不再同时发送该场空景图；母版已锁定的易漂移道具只有在物件特写确需额外身份图时才单独引用。每个 storyboard 首镜还要建立物理光源与氛围基线，后续镜头只写继承或变化。优先描述画面应保持的正向状态，不输出普通负面缺陷词清单或独立 `negativePrompt` 字段；不得泄漏 `c01,3s`、`(意图:)`、`主运镜:` 或 `B001` 等内部分析语法。相邻 storyboard 的自然切入／切出类型必须一致，不得把独立生成伪装成无缝长镜头。

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

脚本只验证确定性结构约束，不能替代台词零改写、剧情顺序、导演细节、节奏与风格适用性的人工语义复核。运行脚本前先逐项完成阶段 07 的质量闸门。

## 最终输出

校验通过后，调用 `submit_artifact` 工具提交精确相对路径 `candidate.json`；最终产物只通过该工具交付：

- 不要在回复文本中复制 JSON 本体或其任何片段；
- 不加解释、校验日志或临时文件路径。

不得提交尚未通过校验的候选 JSON。
