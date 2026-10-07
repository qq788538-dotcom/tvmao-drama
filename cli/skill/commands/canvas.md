# `miaoju canvas` / `node list` / `edge list` — 查看画布

只读地看清一张画布现在长什么样:有哪些节点、怎么连的、某个生成节点**实际会拿到什么输入**。
建图前用它认路,运行前用它审计。

前置:已按 [project.md](./project.md) 绑定项目(或每条命令显式传 `--project`)。

## 子命令

| 子命令 | 作用 |
| --- | --- |
| **`miaoju canvas get`** | 规范化节点 + 连线 + 快照版本 |
| **`miaoju node list`** | 列出节点,可按 `--type` 过滤 |
| **`miaoju node get <节点ID>`** | 单节点详情,含 `derivedInputs` 与 `outputs` |
| **`miaoju edge list`** | 列出连线,可按 `--to` / `--from` 过滤 |

```sh
miaoju canvas get                       # 规范化节点 + 连线 + 快照版本
miaoju node list --type image-generator
miaoju node get n-abc12345              # 含 derivedInputs 与 outputs,供运行前审计
miaoju edge list --to n-abc12345        # 该节点的入边,顺序即输入顺序
```

## 要点

- 节点 DTO 已归一化:媒体 URL 已签名、`history` 三种内部形态统一成一种结构、
  内部管理键不外泄。所以不要自己去拼对象存储地址,拿到什么就是什么。
- CLI 不认识的新节点类型会**原样透传并标 `writable=false`**,不会报错。
  节点类型清单还在增长,遇到 `writable=false` 的节点意味着「可以引用,不能创建或修改」,
  不是「CLI 坏了」。可写的 5 种类型见 [node.md](./node.md)。
- **`derivedInputs` 是运行前的唯一可信来源**:它列出该节点实际会拿到的图/文/视频/音频
  及其顺序。连线建对了不等于输入对了,跑之前用 `node get` 反查一遍比看边便宜得多。

完整选项以 `miaoju canvas --help` / `miaoju node --help` / `miaoju edge --help` 为准。
