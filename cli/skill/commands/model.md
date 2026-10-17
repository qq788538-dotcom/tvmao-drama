# `miaoju model` — 模型目录与参数 schema

**创建生成节点前先看这里**:时长上限、比例、分辨率档位都在 `inputSchema` 里。
凭印象写 `--param duration=10` 而模型只支持到 5,是在运行阶段才炸的那种错。

## 子命令

| 子命令 | 作用 |
| --- | --- |
| **`miaoju model list`** | 按 `--type` / `--search` 检索模型目录 |
| **`miaoju model get <模型ID或精确展示名>`** | 单模型详情,含完整 `inputSchema` |

```sh
miaoju model list --type image
miaoju model list --type video --search Seedance
miaoju model get <模型ID或精确展示名>
```

查能力上限的惯用写法:

```sh
miaoju model get <模型ID> | jq .inputSchema
```

## 要点

- 模型可用**稳定 ID** 或**精确展示名**引用。
- 展示名匹配到多个时报 `MODEL_REF_AMBIGUOUS` 并列出候选,**不会替你挑一个**——
  替你挑的后果是节点存下了一个你没同意过的模型,而且已经按它运行。
- 写入节点与任务的权威值**始终是稳定 `modelId`**,展示名只是查询时的方便写法。
- 模型目录**不含**「预计耗时 / 支持模式 / 是否默认」,能给的能力信息全在 `inputSchema` 里。

选好模型后到 [node.md](./node.md) 建生成节点(必须显式 `--model`)。

完整选项以 `miaoju model <子命令> --help` 为准。
