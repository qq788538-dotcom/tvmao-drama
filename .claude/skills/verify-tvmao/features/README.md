# 喵剧验证功能图

驱动应用前先读这份索引，然后按对应的功能文件操作。格式沿用 pstack `create-verification-skill` 的功能图约定，用 `/pstack:maintain-verification-skill` 维护。

## 基础前提

- 只在 SKILL.md 第 1 节起的临时验证实例上操作，不碰用户正在用的喵剧。
- `MIAOJU_ENDPOINT_DIR` 必须指向临时实例；不设时 `miaoju` 连的是用户的真实数据。
- 每次驱动前先跑 SKILL.md 第 2 节的 doctor。

## 驱动约定

- 优先用 `miaoju` CLI（输出 JSON，最稳定），CLI 覆盖不到的用浏览器验证模式加 Playwright。
- 命令照抄，引号里的名字和参数不要改。
- 任何会提交生成的操作（`miaoju run <节点ID>`、`miaoju node run`、直接 POST sidecar 生成接口、界面上的生成按钮）都不做。需要付费才能验证的功能，标成 `verified-unreachable`，写明缺的前提是 G5 批准。

## 证据与跳过的写法

- 证据放 `.ratchet/evidence/<任务号>/<功能子目录>/`，至少三类（命令与退出码、结构化输出、截图或日志）。
- 断言写进 `commands.log`（“断言通过：…”），不要只放一张截图让人猜。
- 到不了的入口写清尝试了什么、缺什么前提，不要用别的入口顶替说“已验证”。

## 平台

功能要在 Linux 容器和 macOS 上都跑通，才算已验证。状态列写清是哪个平台、哪个任务号。只在一个平台上跑过的，另一个平台写「待复验」。

## 功能条目的格式

每个功能文件以一个 H1 标题和一段用户视角的描述开头，然后按顺序正好四个 H2：`Sub-features`、`How to get to it (user POV)`、`Driving it with <harness>`（以 `Preconditions:` 开头）、`Gotchas`。

## 功能

| 编号 | 功能 | 文件 | 成本 | 状态 |
|---|---|---|---|---|
| F1 | 建图与连线 | [canvas-graph.md](./canvas-graph.md) | 零成本 | 已验证：Linux（T-0007）、macOS bash 3.2（T-0104） |
| F2 | 脚本拆分镜 | [script-storyboard.md](./script-storyboard.md) | 部分计费（仅「生成分镜脚本」提交计费，G5；其余项零成本） | 已验证：macOS bash 3.2（T-0201）；Linux 待复验 |
| F3 | 模型配置 | 待补 | 零成本 | 待补 |
| F4 | 素材库 | 待补 | 零成本 | 待补 |
| F5 | Agent 面板 | 待补 | 可能计费 | 待补 |
