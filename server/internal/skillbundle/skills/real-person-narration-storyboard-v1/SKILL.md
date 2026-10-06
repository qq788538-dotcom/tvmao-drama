---
name: real-person-narration-storyboard-v1
description: Use when converting a Chinese narration script, ordinary screenplay, short story, or novel prose into project-compatible live-action Seedance performance-ad storyboard JSON, with fast-recap narration timing, first-three-second hooks, small-screen visual clarity, source-traceable narration, v3 camera and spatial continuity, and splice-ready independently generated clips.
output_artifact: candidate.json
---

# 真人短篇解说分镜 v1

## 目标

把解说剧本、普通剧本或小说原文转换为当前项目可直接消费的 `candidate.json`，用于小说前贴与信息流投放素材。以连续快速解说和少量保留对白构成语音时间轴；每个语音单元绑定原文来源、可见证据、剧透边界和精确时长，2～3 秒画面节拍覆盖在连续声音上。继承成熟真人短剧 v3 的资产、空间母版、人物动线、构图、物理光线、景别、单一主运镜和独立片段剪辑咬合规则，同时强化前三秒钩子、小屏主体识别和平台压缩后的暗部可读性。

本 skill 必须完全自包含。项目运行时不得调用、读取或依赖其他 skill；本目录内的阶段、参考资料、脚本和测试是唯一运行依赖。

## 开始前

确认输入包含：

- 解说剧本、普通剧本或小说正文；
- `DURATION_SEC`，整数且至少 4，表示单个 storyboard 的生成时长上限，不表示整篇总时长。

优先读取 Skill Run 根目录的 `source.txt` 作为机器原文锚点；没有该文件时才使用消息正文。`INPUT_MODE` 可为 `auto|narration_script|screenplay|short_story`，缺省为 `auto`。本 skill 固定使用 `NARRATION_MODE=fast_recap`；Seedance 2.0 基础口播规划值固定为每秒 3 个语义字符，`PLAYBACK_RATE` 只使用 `1.5|1.6|1.7|1.8`，缺省 `1.6`。该字段只用于解说时长预算和片段装载，不是 Seedance 请求参数，也不得写成最终提示词里的“1.6倍速”。已有解说剧本缺省 `FIDELITY_MODE=faithful`，逐字保留原语音；普通剧本和小说缺省 `FIDELITY_MODE=condensed`，按电影解说逻辑压缩重复信息但保留完整因果链、冲突、反转、证据和结果。`GENRE_PROFILE` 可显式提供；缺省从内容确定为 `general_drama|modern_romance|costume_intrigue|suspense|comeback` 之一。当前项目 schema 一次只交付一个 `candidate.json`；需要多集时由上游按明确原文范围分别启动 Run，本 skill 不伪造未定义的集字段。

## 必须执行的工作流

### 阶段 01：输入路由与解说计划

完整读取 [输入路由与解说计划](references/01-input-routing-and-narration-plan.md)。识别输入类型，建立 `sourceUnits`、解说／对白语音单元、场景、视觉证据和剧透边界，写入当前 Run 根目录的 `narration_plan.json`。

- 已有解说剧本：所有解说和对白逐字、逐项、按原顺序进入计划；只切自然意群，不重写。
- 普通剧本：保留少量决定性对白；其余动作、心理、背景和场间因果改写为高信息密度解说，不新增剧情事实。
- 小说：所有原文单元至少映射一次；相邻重复描写可合并为因果清晰的快速解说，但不得遗漏身份、动机、冲突、反转、证据和结果。只保留真正值得原声落地的短对白。
- `N001` 必须承担前三秒主钩子；它的声音与可见证据从第一个模型镜头同时开始，不用空景、走路或慢铺垫开场。

运行：

```bash
python3 scripts/validate_narration_plan.py narration_plan.json --source source.txt
```

若 Run 中没有 `source.txt`，省略 `--source`。未输出 `OK: narration plan is valid` 前不得进入分镜阶段。

### 阶段 02：资产与空间母版

完整读取 [资产提取](references/02-asset-extraction.md)。从解说计划建立角色、场景、物品和空间母版。匿名“解说／旁白”不创建角色资产；只有实际在画面或具名发声的人物才创建角色资产。

### 阶段 03：语音计时、片段切分与音画映射

完整读取 [解说时长与分镜切分](references/03-duration-and-storyboard-splitting.md)，再读取 [解说与声音处理](references/05-dialogue-and-audio.md)。解说按 `3 × PLAYBACK_RATE` 个语义字符／秒建立规划时钟，非解说声音按基础 3 字／秒；各语音单元保留 0.1 秒精度，先连续累计，整个 storyboard 最后只向上取整一次。`duration` 由连续语音、显式剧情停顿和安全头尾计算，不能为每个短意群或画面切镜重复加时。优先把同场连续语音装载到 12～15 秒附近，长段集中约 45～60 个有声语义字符；普通信息不断句，只有反转、重大结果、对白交接或时空切换可停 0.5 秒。内部画面以 2～3 秒推进，解说不因画面切换暂停或重起；每个解说单元开始时必须已有可见证据，画面不得提前揭示尚未说出的反转。

切分后建立内部**镜头衔接账本**：为每个 storyboard 写唯一叙事任务、弧线位置、景别趋势和运镜趋势；为每条 cNN 写可见意图、起幅、落幅、主表演者起点—路径—终点和唯一主运镜；为相邻片段记录自然切点、轴线、人物世界锚点、视线、道具状态、光线相位和环境声底。账本只用于规划与边界对账，不增加最终 JSON 字段，不向模型发送内部编号。

### 阶段 04：导演与模型提示词编译

依次完整读取：

1. [景别与运镜导演知识库](references/04a-shot-scale-and-camera-library.md)
2. [调度、构图与光线连续性](references/04b-blocking-composition-lighting.md)
3. [约束与负面提示词编译](references/04c-constraint-and-negative-prompt-compiler.md)
4. [投放素材竖屏视觉与光影](references/09-performance-ad-visual-style.md)
5. [镜头编写与 Seedance 提示词编译](references/04-shot-writing-and-directing.md)

一镜只允许一个可见意图、一个主表演动作、一个主运镜和一个明确终点。先按 `visualMode` 分配景别功能，再把整集组织成钩子—因果推进—证据／反应—反转／回报的导演弧线。投放风格只保留一组题材色彩对比、一个可解释主光来源、主体与背景的可读分离和压缩安全曝光；轮廓光只在窗、路灯、雪地反射、门缝或聚光灯等物理来源成立时使用。连续性执行“世界坐标强锁，画面坐标按镜选用”：入口、窗、座位、路线、道具归属和光源方向跨镜继承，画左／画右只在当前运动或构图需要时使用。最终 prompt 只输出自然语言 `镜头N：`，不得泄漏内部时长、映射编号、导演解释、营销结论或未来片段。

### 阶段 05：风格、JSON 与质量闸门

完整读取 [标签与风格控制](references/06-tags-and-style-control.md)和 [JSON 组装与质量闸门](references/07-json-assembly-and-quality-gates.md)。复杂输入或首次执行时读取 [输入输出示例](references/08-examples.md)。

输出前执行四次对账：

1. **来源对账**：每个 `sourceUnit` 至少关联一个语音单元，关联双向一致。
2. **语音对账**：先做正向对账，确认 `narration_plan.json` 中每条解说／对白按顺序恰好出现一次；再做反向对账，确认 prompt 中每段有文本声音都能回溯到计划；不得新增、遗漏、重排或换人。
3. **视觉对账**：每条语音的画面与 `visualEvidence` 同步，不能只写走路、转头、握拳等无事实证据的泛动作，也不能在声音前剧透。
4. **导演与边界对账**：长序列必须形成空间建立、关系／动作、证据／反应和释放的景别弧线；摄影机运动、机位变化或有意静止必须来自统一导演声部。相邻片段自然切点、世界状态和声底闭环，不能让镜头语言塌缩成单一安全模板。

## 校验循环

把候选 JSON 写入当前 Run 根目录的相对路径 `candidate.json`，然后运行：

```bash
python3 scripts/validate_storyboard.py candidate.json --duration-sec <DURATION_SEC> --narration-plan narration_plan.json --source source.txt
```

若 Run 中没有 `source.txt`，才省略 `--source`。修复全部 `ERROR:`，重复运行，直到输出 `OK: storyboard JSON is valid`。确定性脚本不能替代画面是否真正承载语义、导演意图是否一致和空间是否合理的人工语义复核。

## 最终输出

校验通过后调用 `submit_artifact`，提交精确相对路径 `candidate.json`。

- 不在回复中重发 JSON；
- 不提交 `narration_plan.json`；
- 不输出解释、日志、Markdown 代码块或绝对路径；
- 不得提交未通过校验的产物。
