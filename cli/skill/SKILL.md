---
name: miaoju-cli
description: >-
  操作本机喵剧桌面端自由画布的命令行工具(miaoju)。
  凡是和画布/项目/节点/模型/素材相关的操作,一律通过 miaoju CLI 完成,
  不要自己捏造 HTTP 请求或改去应用里手动点。本 skill 内即包含完整的 CLI 命令
  操作手册;端到端场景见 examples/。
---

# 喵剧AI CLI(`miaoju`)

## 使用前提

1. 喵剧桌面端必须**已经打开**。命令行工具只连本机正在运行的喵剧,应用没开时所有命令都会失败。
2. 遇到以下退出码时,按表处理,**不要**原样重试:

| 退出码 | 含义 | 处理 |
|---|---|---|
| 8 | 喵剧未运行 | 请用户打开喵剧桌面端,打开后再执行同一条命令 |
| 9 | 网络被沙箱拦截 | 本工具需要访问本机 `127.0.0.1`。Codex:申请提升权限(或请用户在 `~/.codex/config.toml` 设置 `sandbox_mode = "workspace-write"`,并在 `[sandbox_workspace_write]` 下设置 `network_access = true`);Claude Code:请用户放行本机地址。获得权限后重试同一条命令 |

## 以 `--help` 为准

本 skill 是 `miaoju` 的使用手册,子命令与选项的权威文案以 `miaoju --help` 与
`miaoju <子命令> --help` 为准;当文档与 `--help` 不一致时,以 CLI 实际输出为准。

**输出约定**:默认输出 JSON 到 stdout,进度与诊断走 stderr——可直接 `| jq`,
不需要从日志里抠 ID。`-o table`(给人看)/`-o ndjson`(逐行管道)/`--quiet`。

```sh
miaoju project list -o json      # 默认
miaoju project list -o table     # 给人看
miaoju project list -o ndjson    # 逐行,便于管道
miaoju project list --quiet      # 不打进度(警告与错误仍然输出)
```

## 文档地图

| 主题 | 文件 |
| --- | --- |
| 项目(画布)与目录绑定 | [commands/project.md](./commands/project.md) |
| 查看画布(节点/连线/derivedInputs) | [commands/canvas.md](./commands/canvas.md) |
| 模型目录与参数 schema(创建生成节点前必看) | [commands/model.md](./commands/model.md) |
| 建图(节点/连线/参数,5 种可写类型) | [commands/node.md](./commands/node.md) |
| 素材上传 / 结果下载 | [commands/asset.md](./commands/asset.md) |
| 运行与取结果(幂等/等待/落盘规则) | [commands/run.md](./commands/run.md) |
| Seedance 2.0 合规校验 | [commands/compliance.md](./commands/compliance.md) |
| 端到端最小工作流 | [examples/minimal-workflow.md](./examples/minimal-workflow.md) |

## 退出码

| 退出码 | 含义 |
|---:|---|
| 0 | 成功 |
| 2 | 参数或本地配置错误(改命令再来) |
| 3 | 鉴权失败(一般不会出现,出现时按 8 处理) |
| 4 | 资源不存在 |
| 5 | 冲突:进行中、版本不符、幂等键复用、模型名歧义 |
| 6 | 服务端或上游失败 |
| 7 | 网络超时、限流或用户取消 |
| 8 | 喵剧未运行(见「使用前提」) |
| 9 | 网络被沙箱拦截(见「使用前提」) |

脚本应按**错误码**(`error.code`)而不是中文 message 分支——message 文案会变,
按它分支的脚本会在某次文案调整后静默走错分支。出错时 stderr 会带 `request-id`,
反馈问题时附上即可。

## 已知边界

先知道什么做不到,才不会在这些地方反复试:

- CLI 修改画布后,用户已打开的画布会在空闲时自动刷新。
- 不支持任务取消。
- 上传的素材保存在本机喵剧数据目录。
- 没有批量原子 mutation:一组节点/连线是逐条写入的,中途失败会留下部分结果(命令会如实报告建到了哪一步)。
- 合规校验只覆盖画布节点上的素材;素材库里没挂到节点上的东西还是得回到喵剧桌面端,在画布上手动提交。
- 模型目录不含「预计耗时 / 支持模式 / 是否默认」——后端 `model_configs` 表本就没有这几列,
  能给的能力信息全在 `inputSchema` 里。
