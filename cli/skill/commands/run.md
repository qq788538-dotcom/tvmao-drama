# `miaoju node run` / `miaoju run` — 运行与取结果

**运行会调用上游模型,费用由用户在上游账户承担。** 建图阶段([node.md](./node.md))
不会触发生成,所以可以先把整张图搭完、审一遍,再决定跑哪些节点。

前置:节点已建好、上游连对(跑之前用 `miaoju node get` 看 `derivedInputs`,
见 [canvas.md](./canvas.md));`doubao-seedance*` 还要先过合规校验,见
[compliance.md](./compliance.md)。

## 子命令

| 子命令 | 作用 |
| --- | --- |
| **`miaoju node run <节点ID>`** | 提交生成,返回 `taskId` |
| **`miaoju run get <任务ID>`** | 查任务当前状态 |
| **`miaoju run wait <任务ID>`** | 阻塞等到终态 |

```sh
miaoju node run n-abc12345                 # 提交后立即返回 taskId
miaoju node run n-abc12345 --wait          # 等到终态
miaoju node run n-abc12345 --param ratio=9:16 --model <模型ID>
miaoju run get 1234
miaoju run wait 1234
miaoju asset download --node n-abc12345 --out ./out.png
```

结果取回本地见 [asset.md](./asset.md)。

## 要点

- `--param` 覆盖的参数会**合并进节点并落盘**,下次不带也生效。也就是说 `run --param`
  不是「这一次临时用一下」,而是**改了节点**——不想留就跑完自己改回去。
- 节点已在生成中时命令会被拒绝(`NODE_ALREADY_RUNNING`),不会重复提交。
- 节点没保存模型时报 `MODEL_REQUIRED`,需要显式 `--model`。
- 提示词里的 `@[图片:节点ID]` 标记在提交时被翻译成「图片N」(规则见 [node.md](./node.md) 要点)。
- 每条写命令默认自动带幂等键,所以网络失败后的自动重试**不会产生第二个任务**。
  需要跨进程固定时用 `--idempotency-key`。
- `--wait` 期间 **Ctrl+C 只是停止等待**,任务仍在服务端跑,可用 `miaoju run get` 继续查。
  当前**不支持任务取消**——Ctrl+C 不会取消任务,也不会因此回滚。

出错时按 `error.code` 分支,退出码含义见 [../SKILL.md](../SKILL.md#退出码)。

完整选项以 `miaoju node run --help` / `miaoju run <子命令> --help` 为准。
