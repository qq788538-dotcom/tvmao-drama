# CLI 接入 AI 编程助手（Claude Code / Codex）

喵剧AI 附带命令行工具 `miaoju`，再配一份写给 AI 看的使用手册（Skill）。装好以后，你可以在 Claude Code、Codex 里用自然语言说需求，由 AI 助手调用 `miaoju` 去建节点、接线、上传素材、运行生成、下载结果，改动会实时出现在打开的画布上。

这份文档讲清楚三件事：它是怎么工作的、在 Claude Code 和 Codex 里分别怎么配置、以及怎么配合使用最顺手。命令本身的完整说明见 [CLI 手册](../cli/skill/SKILL.md)。

- [1. 工作原理](#1-工作原理)
- [2. 安装](#2-安装)
- [3. 在 Claude Code 中使用](#3-在-claude-code-中使用)
- [4. 在 Codex 中使用](#4-在-codex-中使用)
- [5. 其他 AI 工具和脚本](#5-其他-ai-工具和脚本)
- [6. 推荐的工作方式](#6-推荐的工作方式)
- [7. 场景示例](#7-场景示例)
- [8. 命令速查](#8-命令速查)
- [9. 排错](#9-排错)

---

## 1. 工作原理

```
Claude Code / Codex / 终端脚本
        │  执行 miaoju 命令
        ▼
miaoju CLI ──读取──► 数据目录里的 cli-endpoint.json（端口 + 一次性令牌）
        │
        │  HTTP，只连 127.0.0.1
        ▼
喵剧AI 本地服务（sidecar） ──► 画布实时刷新
        │  运行生成时
        ▼
你配置的模型网关
```

需要知道的几点：

- **喵剧AI 必须开着。** CLI 不是独立的服务，它连的是正在运行的喵剧AI。应用每次启动换一个随机端口和令牌，写进数据目录的 `cli-endpoint.json`，CLI 每次执行都会重新读取。应用没开时命令会以退出码 8 失败。
- **只走本机。** CLI 只访问 `127.0.0.1`，令牌每次启动都会更换，macOS 上地址文件权限为仅本人可读。
- **建图不花钱，运行才花钱。** `node create`、`edge create` 永远不会触发生成；只有 `node run` 会调用上游模型，费用记在你的网关账户上。
- **输出是 JSON。** 默认 JSON 输出到 stdout、进度和诊断走 stderr，AI 助手和 `jq` 都能直接解析。给人看用 `-o table`。

---

## 2. 安装

打开喵剧AI，进入「设置 → 命令行工具」，两种方式任选其一。

### 方式一：一键安装（推荐）

点「安装」。应用会：

1. 把 `miaoju` 装到 `~/.miaoju/bin/`（Windows 为 `%USERPROFILE%\.miaoju\bin\`），并加入 PATH（macOS 写入 `~/.zshrc`，Windows 写入用户 Path）。
2. 检测本机是否装了 Claude Code（`~/.claude` 存在）和 Codex（`~/.codex` 存在），把 Skill 装到对应的 `skills/miaoju-cli` 目录。没检测到的不会替你创建。

安装后重新打开终端，运行：

```sh
miaoju version
miaoju project list -o table
```

能列出项目就说明一切正常。卡片上会分别显示 Claude Code 和 Codex 的 Skill 状态：

| 状态 | 含义 |
|---|---|
| 已安装 | 正常 |
| 未检测到 Claude Code / Codex | 对应的 `~/.claude` 或 `~/.codex` 不存在。先装好并运行一次该工具，再回来点「重新安装」 |
| 已存在同名目录，未覆盖 | 你自己建过 `skills/miaoju-cli`，应用不会动它。确认不需要后删掉再重新安装 |
| 版本不一致 | 应用升级过，点「重新安装」同步 |

### 方式二：让 AI 助手自己装

切到「让 AI 助手安装」页签，复制里面那段话，发给 Claude Code 或 Codex。它会按步骤执行和一键安装相同的操作，并在最后读一遍 Skill 学会用法。因为要写入工作区以外的目录（`~/.miaoju`、`~/.zshrc`、`~/.claude` 或 `~/.codex`），AI 助手会请你批准。

### 升级与卸载

- 喵剧AI 升级后，设置页显示「版本不一致」时点「重新安装」即可。macOS 上命令和 Skill 都是指向应用内文件的链接，通常会自动跟着更新。
- 点「卸载」会移除命令、PATH 配置和应用装的 Skill，你自己的文件不受影响。

---

## 3. 在 Claude Code 中使用

### 3.1 确认 Skill 已加载

一键安装后，Skill 位于 `~/.claude/skills/miaoju-cli/`。Claude Code 会根据 Skill 的描述自动判断何时使用：只要你提到画布、节点、喵剧等相关需求，它就会先读手册再执行命令。

新开一个会话（已开着的会话不会加载新装的 Skill），可以先问一句确认：

```text
你能用 miaoju 操作喵剧画布吗？先列一下我有哪些项目。
```

也可以输入 `/miaoju-cli` 手动调用这个 Skill。

### 3.2 减少权限确认（可选）

Claude Code 默认每次执行命令都会问你。`miaoju` 的读写都只发生在本机画布，觉得每次确认太烦的话，可以在工作目录的 `.claude/settings.json`（只对这个目录生效）或 `~/.claude/settings.json`（全局）里放行：

```json
{
  "permissions": {
    "allow": ["Bash(miaoju:*)"]
  }
}
```

放行后 Claude Code 运行生成也不会再问你，而生成会产生费用。介意的话只放行只读命令，例如 `"Bash(miaoju project list:*)"`、`"Bash(miaoju canvas get:*)"`、`"Bash(miaoju node get:*)"`、`"Bash(miaoju model:*)"`。

### 3.3 沙箱

如果你开启了 Claude Code 的沙箱，并且命令以退出码 9 失败（网络被拦截），说明沙箱挡住了对 `127.0.0.1` 的访问。在沙箱的网络设置里放行本机地址，或让 Claude Code 在沙箱外执行 `miaoju`。

### 3.4 在项目目录里写一份说明（可选）

如果某个目录专门用于一部短剧，可以在目录里放一个 `CLAUDE.md`，写清约定，Claude Code 每次进来都会读：

```markdown
# 本目录
- 对应喵剧项目：《雨夜》，已用 miaoju project use 绑定。
- 剧本在 script/，角色参考图在 refs/，生成结果下载到 out/。
- 视频统一 9:16、5 秒，模型用 doubao-seedance 系列。
- 运行生成前先列出要跑的节点，等我确认。
```

---

## 4. 在 Codex 中使用

### 4.1 放行本机网络（必须）

Codex 默认禁止沙箱内联网，`miaoju` 访问 `127.0.0.1` 也会被拦，表现为退出码 9 或每次都要审批。编辑 `~/.codex/config.toml`，加入：

```toml
sandbox_mode = "workspace-write"

[sandbox_workspace_write]
network_access = true
```

保存后重新启动 Codex。这段配置在喵剧AI 的「设置 → 命令行工具 → Codex 用户须知」里也能一键复制。

### 4.2 确认 Skill 已加载

一键安装后，Skill 位于 `~/.codex/skills/miaoju-cli/`。新开一个 Codex 会话后直接说需求即可，例如：

```text
用喵剧在当前项目里建一个图片节点，并接到视频生成节点上。
```

如果 Codex 没有主动使用 Skill，可以明确告诉它：「先阅读 miaoju-cli 这个 skill，再操作喵剧画布」。

### 4.3 用 AGENTS.md 固定约定（可选）

Codex 会读取工作目录里的 `AGENTS.md`，写法和上面 Claude Code 的 `CLAUDE.md` 一样，把项目绑定、素材目录、默认参数、是否需要你确认后再运行写进去即可。

---

## 5. 其他 AI 工具和脚本

`miaoju` 是普通的命令行程序，任何能执行终端命令的 AI 工具（Cursor、Gemini CLI、Cline 等）都能用。这些工具不会自动安装 Skill，在工作目录的 `AGENTS.md` 或该工具的规则文件里加一句即可：

```markdown
操作喵剧画布时使用 miaoju 命令行工具，用法见 ~/.claude/skills/miaoju-cli/SKILL.md
（或喵剧AI 源码仓库里的 cli/skill/SKILL.md），以 miaoju --help 的输出为准。
```

不用 AI，直接写 shell 脚本也可以，完整示例见 [端到端最小工作流](../cli/skill/examples/minimal-workflow.md)。

---

## 6. 推荐的工作方式

### 一个目录对应一个项目

`miaoju project use <项目ID>` 会在**当前目录**写入 `.miaoju/project.json`，之后在这个目录里执行的命令都作用于这个项目。规则很严格：只看当前目录，不向上级目录查找，没绑定也不会猜。

所以推荐给每部短剧建一个工作文件夹，在里面绑定项目，再在这个文件夹里启动 Claude Code 或 Codex：

```sh
mkdir -p ~/短剧/雨夜 && cd ~/短剧/雨夜
miaoju project create --name "雨夜" --use
claude        # 或 codex
```

这样 AI 助手读的剧本、放的素材、下载的结果都在同一个地方，也不会改错项目。

### 先搭图，审一遍，再运行

建节点和连线不花钱，运行才花钱。和 AI 助手配合时，建议明确分两步说：

1. 「把分镜全部建成节点、连好线，**先别运行**。」
2. 到喵剧画布上看一遍，改改提示词，确认没问题后再说「运行第 1 到第 3 镜」。

需要的话把「运行前先等我确认」写进 `CLAUDE.md` / `AGENTS.md`。

### 让 AI 运行前先核对输入

生成节点实际会收到哪些图、文、视频、音频，以 `miaoju node get <节点ID>` 输出里的 `derivedInputs` 为准。Skill 里已经要求 AI 运行前检查，你也可以直接说「运行前把每个节点的 derivedInputs 列给我看」。

### 喵剧内置 Agent 和外部 AI 助手怎么选

| | 喵剧内置 Agent | Claude Code / Codex + CLI |
|---|---|---|
| 在哪用 | 应用内对话面板 | 终端 |
| 擅长 | 理解画布内容、写提示词、改剧本、单张画布上的创作对话 | 读写本地文件、批量操作、跑脚本、按规则整理输出 |
| 能读你的本地文件 | 只能读上传到画布的素材 | 能读工作目录里的剧本、表格、图片 |
| 典型用法 | 「帮我把这几个镜头的提示词改得更有电影感」 | 「读 script/第一集.md，每一镜建一个视频节点，按 3 列网格排好」 |

两者可以同时用：外部 AI 助手负责批量搭建和导出，内置 Agent 负责在画布上精修。

---

## 7. 场景示例

下面的说法可以直接发给 Claude Code 或 Codex。第一次使用某个模型前，AI 会先用 `miaoju model get` 查参数范围（比如时长上限），不需要你记。

### 场景一：从剧本文件批量建分镜

```text
读 script/第一集.md，按场景拆成镜头。每个镜头建一个 video-generator 节点，
模型用 Seedance，比例 9:16、时长 5 秒，提示词写画面和运镜。
用 node move 把节点按每行 4 个排好。先别运行，建完把镜头列表给我。
```

AI 会依次执行 `model list`、`model get`、多次 `node create`，最后用一条 `node move` 批量摆放位置。

### 场景二：用同一组参考图保持角色一致

```text
把 refs/ 下的角色图都上传成节点。然后为下面三句台词各建一个 image-generator，
都接上林晓的参考图，提示词里用 @ 引用她的图：
1. 林晓撑伞走进小巷
2. 林晓回头，表情惊恐
3. 林晓站在路灯下，背影
```

AI 会用 `asset upload --create-node` 上传，用 `--left` 接线，并在提示词里写 `@[图片:<节点ID>]` 精确引用某张图。连线增删后图片序号会自动重算，不会指错图。

### 场景三：Seedance 2.0 合规校验后再生成

```text
对所有接到 Seedance 视频节点上的参考图做合规校验，
全部通过后再运行这些视频节点，等结果出来。
```

AI 会先 `compliance verify`，用 `compliance status` 确认都是 `active`，再 `node run --wait`。重复校验是安全的，已通过的素材不会再消耗额度。

### 场景四：批量下载并按镜头命名

```text
把当前项目里所有已成功的视频节点下载到 out/，
文件名按画布上从左到右、从上到下的顺序编号（01.mp4、02.mp4…），最后列一张编号和节点 ID 的对照表。
```

### 场景五：调整已有节点

```text
第 3 镜的提示词改成夜景、加一点雨，比例改为 16:9，然后重新生成。
```

注意：`node run --param` 覆盖的参数会写回节点，不是一次性的。

### 场景六：写成可以反复跑的脚本

```text
把刚才的流程写成 build.sh：读取 shots.csv（镜头号、提示词、时长），
为每一行建视频节点并接上 refs/林晓.png。默认只建图，加 --run 参数才运行。
```

CLI 的每条写命令都自带幂等键，网络失败自动重试不会重复建任务；需要跨多次执行保持同一个幂等键时用 `--idempotency-key`。

---

## 8. 命令速查

| 想做什么 | 命令 |
|---|---|
| 列出项目 | `miaoju project list -o table` |
| 新建项目并绑定当前目录 | `miaoju project create --name "雨夜" --use` |
| 绑定已有项目 | `miaoju project use <项目ID>` |
| 看整张画布 | `miaoju canvas get` |
| 列出节点 | `miaoju node list --type video-generator` |
| 看节点详情和实际输入 | `miaoju node get <节点ID>` |
| 查模型 | `miaoju model list --type video --search Seedance` |
| 查模型参数范围 | `miaoju model get <模型ID> \| jq .inputSchema` |
| 上传素材并建节点 | `miaoju asset upload ./ref.png --create-node` |
| 建生成节点并接上游 | `miaoju node create --type image-generator --model <模型ID> --param prompt="..." --left <节点ID>` |
| 改参数、换上游 | `miaoju node update <节点ID> --param ratio=9:16 --left-rm <旧> --left <新>` |
| 批量摆位置 | `miaoju node move --set n-a=0,0 --set n-b=400,0` |
| 合规校验 | `miaoju compliance verify <节点ID>...` / `miaoju compliance status` |
| 运行并等待 | `miaoju node run <节点ID> --wait` |
| 查任务 | `miaoju run get <任务ID>` / `miaoju run wait <任务ID>` |
| 下载结果 | `miaoju asset download --node <节点ID> --out ./out/` |

可写的节点类型只有 `image-input`、`audio-input`、`video-input`、`image-generator`、`video-generator` 五种。文字节点、脚本节点、3D 导演台、后处理结果等可以读取和引用，但要在应用里创建和修改。

所有选项以 `miaoju <命令> --help` 为准。

---

## 9. 排错

| 现象 | 原因与处理 |
|---|---|
| `command not found: miaoju` | 安装后没重新打开终端；或 AI 助手的会话是安装前开的。重开终端或新开会话。仍不行就用完整路径 `~/.miaoju/bin/miaoju` |
| 退出码 8：喵剧未运行 | 打开喵剧AI，等画布加载完再重试 |
| 退出码 9：网络被沙箱拦截 | Codex 按 [4.1](#41-放行本机网络必须) 配置；Claude Code 见 [3.3](#33-沙箱) |
| 退出码 2：参数错误 | 看 stderr 的提示改命令，常见的是没绑定项目：先 `miaoju project use <项目ID>`，或加 `--project <项目ID>` |
| `MODEL_REQUIRED` | 生成节点必须显式 `--model`，CLI 不会替你挑模型 |
| `MODEL_REF_AMBIGUOUS` | 模型名匹配到多个，改用错误信息里列出的模型 ID |
| `NODE_ALREADY_RUNNING` | 节点正在生成，等它结束，用 `miaoju run get` 查看 |
| Seedance 生成失败，提示素材未通过校验 | 先 `miaoju compliance verify` 上游素材节点；状态为 `failed_permanent` 的需要换素材 |
| 画布没有变化 | CLI 的改动会在画布空闲时自动刷新，稍等片刻即可 |
| AI 助手不知道怎么用 miaoju | Skill 没装上或会话是旧的。到设置页看 Skill 状态，新开会话，或直接让它「先读 ~/.claude/skills/miaoju-cli/SKILL.md」 |
| `--wait` 时按了 Ctrl+C | 只是停止等待，任务仍在跑，用 `miaoju run get <任务ID>` 继续查。目前不支持取消任务 |

反馈问题时附上 stderr 里的 `request-id`，以及「设置 → 环境诊断」的结果。

完整退出码表见 [CLI 手册](../cli/skill/SKILL.md#退出码)。
