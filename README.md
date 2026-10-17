<p align="center">
  <img src="web/public/logo.png" alt="喵剧AI" width="96" />
</p>

<h1 align="center">喵剧AI</h1>

<p align="center">
  <b>开源的 LibTV 平替。</b>本地优先的 AI 短剧创作画布：在一张无限画布上把剧本、图片、视频、配音串成完整的创作流程。
</p>

<p align="center">
  <a href="https://www.tvmao.net/"><img src="https://img.shields.io/badge/%E7%BA%BF%E4%B8%8A%E4%BD%93%E9%AA%8C-www.tvmao.net-ff4f8b?style=for-the-badge" alt="线上体验 www.tvmao.net" /></a>
</p>

<p align="center">
  <a href="https://www.tvmao.net/">线上体验</a> ·
  <a href="#下载安装">下载安装</a> ·
  <a href="docs/tutorial.md">上手教程</a> ·
  <a href="docs/user-guide.md">用户手册</a> ·
  <a href="docs/development.md">开发指南</a> ·
  <a href="docs/ai-coding-tools.md">接入 Claude Code / Codex</a> ·
  <a href="cli/skill/SKILL.md">命令行工具</a> ·
  <a href="README.en.md">English</a>
</p>

<p align="center">
  <img src="docs/screenshots/canvas.jpg" alt="喵剧AI 画布：剧本、AI 绘图、AI 视频节点连成一条创作流程" width="100%" />
</p>

---

## 这是什么

喵剧AI 是一个运行在 macOS 和 Windows 上的桌面应用。你在画布上放节点、拉连线：上传的图片、视频、音频和文字作为输入，接到 AI 绘图、AI 视频、AI 配音等生成节点上，结果直接出现在画布里，可以继续剪辑、裁剪、续写、超分，或者再接到下一个节点。

- **数据都在你自己电脑上。** 项目、素材、生成结果保存在本机数据目录，不需要注册账号，也没有云端同步。
- **模型自己接。** 应用不提供模型服务，你填写一个兼容 [New API](https://github.com/QuantumNous/new-api) 协议的网关地址和 API Key 即可使用全部内置模型，也可以给单个模型配置独立的上游。
- **AI 助手可以直接操作画布。** 内置 Agent 对话，能理解画布内容、调用技能、帮你建节点和跑生成；另附命令行工具，让 Claude Code、Codex 等编程助手从终端操作画布。

## 和 LibTV 有什么不同

喵剧AI 对标 LibTV 这类在线 AI 视频创作画布，做的是同一类事：在无限画布上用节点和连线串起剧本、分镜图、视频和配音，一路做出短剧。不同的是它完全开源、跑在你自己电脑上：

| | LibTV 等在线平台 | 喵剧AI |
|---|---|---|
| 运行方式 | 网页在线服务，需要注册登录 | 桌面应用，打开即用，不需要账号 |
| 素材和作品 | 存在平台云端 | 只存在本机数据目录 |
| 模型和费用 | 用平台提供的模型，按平台价格付费 | 接自己的 New API 兼容网关，模型和费用自己说了算 |
| 源代码 | 闭源 | MIT 开源，可以自己改代码、加模型、写 Skill |

喵剧AI 是独立的开源项目，与 LibTV 及其运营方没有关联。

## 界面预览

| 画布上的图片工具 | 内置 Agent |
|---|---|
| ![选中图片节点后的工具栏和提示词面板](docs/screenshots/image-tools.jpg) | ![Agent 对话面板读取画布内容给出分镜建议](docs/screenshots/agent.jpg) |
| **故事板视图** | **3D 导演台** |
| ![按文本、图片、视频分栏的故事板视图](docs/screenshots/storyboard.jpg) | ![3D 导演台里摆放人偶和机位](docs/screenshots/director-desk.jpg) |

截图里的示例画面是为演示画的插画，不是模型生成结果。模型设置界面见 [设置截图](docs/screenshots/settings.jpg)。

## 主要功能

| 模块 | 能做什么 |
|---|---|
| 无限画布 | 连线、缩放平移、撤销重做、小地图、右键菜单、自动整理、故事板视图 |
| 输入节点 | 上传图片、视频、音频，文字节点（支持 Markdown） |
| 生成节点 | AI 绘图、AI 视频、AI 音频、保存到本地 |
| 图片工具 | 宫格切分、标注、旋转镜像、打光、多角度、全景、放大查看 |
| 视频工具 | 剪辑、裁剪、智能续写、片段重拍、转高清、白模、去字幕、音频分离、抽帧 |
| 音频工具 | 截取、变速、波形预览 |
| 3D 导演台 | 在 3D 场景里摆放人偶和镜头，截图作为分镜参考 |
| 脚本节点 | 输入剧本或小说，按风格模板拆成分镜，再批量生成视频 |
| Agent 对话 | 理解画布、调用技能和子 Agent，直接在画布上建节点、改参数 |
| 生成历史 | 全局历史记录，支持批量 ZIP 下载和回填到画布 |
| 素材库 | 文件夹管理常用素材，一键应用到画布 |
| AI 角色库 | 浏览角色形象并引用到 Seedance 生成（开源版不附带角色数据） |
| 命令行工具 | `miaoju` CLI，让终端和 AI 编程助手操作画布 |

### 内置模型

开箱内置 20 个常用模型（以应用内「设置 → 模型中心」为准），包括：

- **图片**：Nano-Banana-2、Nano-Banana-Pro、gpt-image-2、豆包 Seedream 5.0、z-image
- **视频**：豆包 Seedance 2.0 / 2.5 系列（含视频延长、视频编辑）、MiniMax-H3、grok-video-3
- **音频**：豆包音频生成、微软 TTS
- **文本**：用于 Agent 和剧本拆分的大语言模型

模型能否调用取决于你接入的网关是否提供对应渠道。

## 下载安装

到 [Releases](../../releases) 页面下载最新版本：

| 系统 | 安装包 | 要求 |
|---|---|---|
| macOS（Apple 芯片） | `喵剧AI_<版本>_aarch64.dmg` | macOS 14 及以上 |
| macOS（Intel） | `喵剧AI_<版本>_x64.dmg` | macOS 14 及以上 |
| Windows | `miaoju-windows-x64-<版本>.zip` | Windows 10 / 11 64 位 |

安装包没有购买代码签名证书，第一次打开时系统会拦截，按 [macOS 首次打开说明](packaging/首次打开说明-macOS.txt) 或 [Windows 首次打开说明](packaging/首次打开说明-Windows.txt) 放行一次即可。

## 快速开始

1. 打开喵剧AI，应用会自动创建一个「未命名项目」并进入画布。
2. 点右上角「设置 → 模型中心」，在「默认上游」里填写网关地址（如 `https://example.com`，不用带 `/v1`）和 API Key，保存。
3. 回到画布，右键空白处选「添加节点 → AI 绘图」，写一句提示词，点生成。
4. 把生成的图片拖一根线接到「AI 视频」节点上，就能图生视频。

第一次用可以跟着 [上手教程](docs/tutorial.md) 从零做一条短片；想让 Claude Code、Codex 直接操作画布，见 [接入 Claude Code / Codex](docs/ai-coding-tools.md)。更多用法见 [用户手册](docs/user-guide.md)。

## 数据存放在哪里

| 系统 | 默认数据目录 |
|---|---|
| macOS | `~/Library/Application Support/com.miaoju.desktop` |
| Windows | `%APPDATA%\com.miaoju.desktop` |

数据目录可以在「设置 → 配置」里更改，应用会自动迁移现有数据。API Key 以明文保存在数据目录的 `data.db` 中，请不要把数据目录分享给他人。

## 技术架构

```
┌─────────────────────────── 喵剧AI（Tauri 2 桌面壳，Rust）───────────────────────────┐
│                                                                                    │
│   WebView：React 18 + TypeScript + Vite + Ant Design                               │
│        │  REST /api/v1 + WebSocket（仅 127.0.0.1，随机端口 + 一次性令牌）            │
│        ▼                                                                           │
│   sidecar：Go 本地服务（chi、SQLite、Eino Agent、生成队列、媒体存储）                  │
│        │                                                                           │
└────────┼───────────────────────────────────────────────────────────────────────────┘
         ▼
   你配置的模型网关（New API 兼容）            miaoju CLI（Go）──► 同一个 sidecar
```

| 目录 | 内容 |
|---|---|
| `web/` | 前端：画布编辑器、节点、设置页 |
| `server/` | Go sidecar：HTTP/WS 接口、SQLite、生成管线、Agent、Skill 运行时 |
| `src-tauri/` | Tauri 桌面壳：启动和守护 sidecar、数据目录迁移、保存文件、安装 CLI |
| `cli/` | `miaoju` 命令行工具和配套的 AI 助手 Skill |
| `seed/` | 首次安装导入的内置模型与配置 |
| `scripts/` | 构建、打包、发版脚本 |
| `docs/` | 用户手册、开发指南和设计文档 |

## 从源码构建

需要 Node.js 22、pnpm 10、Go 1.26、Rust stable 和 `tauri-cli` 2.12+。

```sh
pnpm install
pnpm dev          # 编译 sidecar 和 CLI，启动开发版应用
```

运行测试：

```sh
pnpm test:server && pnpm test:web && pnpm test:tauri && pnpm test:scripts
```

完整的环境准备、目录说明和打包流程见 [开发指南](docs/development.md)。

## 参与贡献

欢迎提交 Issue 和 Pull Request，请先阅读 [贡献指南](CONTRIBUTING.md)。发现安全问题请按 [安全策略](SECURITY.md) 私下报告，不要公开提 Issue。

## 许可证

本项目以 [MIT 许可证](LICENSE) 开源。内置的第三方资源（如 3D 人偶模型）遵循各自的许可，见对应目录下的 license 文件。

## 免责声明

喵剧AI 只是创作工具，本身不提供任何模型服务。使用第三方模型生成内容时，请遵守相应服务商的使用条款和所在地的法律法规，不要生成侵犯他人肖像权、著作权或其他合法权益的内容。
