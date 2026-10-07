# `miaoju node` / `miaoju edge` — 建图

创建节点、连线、改参数。**建图不会触发生成**:`node create` 与 `edge create` 永远不会
隐式触发生成,也不会调用上游模型。想跑要显式走 [run.md](./run.md)。

前置:已按 [project.md](./project.md) 绑定项目;建生成节点前先按 [model.md](./model.md)
确认模型 ID 与 `inputSchema`。

## 子命令

| 子命令 | 作用 |
| --- | --- |
| **`miaoju node create`** | 建节点;`--left` 可同时接上游 |
| **`miaoju node update <节点ID>`** | 改参数 / 换上游 |
| **`miaoju node move`** | **批量**摆位置 / 改尺寸,多个节点一次请求 |
| **`miaoju node delete <节点ID>`** | 删节点(需 `--yes`) |
| **`miaoju edge create`** | 建一条连线 |

```sh
# 建生成节点,同时接上游(--left 可重复,顺序即输入顺序)
miaoju node create \
  --type image-generator \
  --model doubao-image \
  --param prompt="雨后城市街道,霓虹灯,电影感" \
  --param ratio=16:9 \
  --left n-ref00001 \
  --left n-text0002

miaoju edge create --from n-aaa --to n-bbb
miaoju node update n-bbb --param prompt="换个说法"

# 提示词里 @ 指定某张图(必须是已用 --left 连上的上游节点 ID)
miaoju node update n-bbb --param prompt="@[图片:n-ref00001] 中的角色走进雨夜街道"

# 换掉某个上游:一条命令内先删后加,顺序可预期
miaoju node update n-bbb --left-rm n-old --left n-new

# 批量摆位:多个节点一条命令、一次请求、版本只 +1、整批原子
miaoju node move --set n-aaa=0,500 --set n-bbb=400,500 --set n-ccc=,3400   # 留空分量不动
miaoju node move --file layout.json     # [{"id":"n-aaa","x":0,"y":500}, ...],- 读 stdin

miaoju node delete n-bbb --yes
```

**布局多个节点一律用 `node move`,不要逐个 `node update --x --y`。** 逐个更新会让你
每挪一个节点就停下来思考一轮、还得反复 `canvas get` 追版本号——十几个节点要花几分钟,
其中网络只占十几秒。`node move` 不看可写类型(坐标不是内容,网页上任何节点都能拖),
响应带回新 `version`。

输入节点通常不用手写 `node create`——直接 `miaoju asset upload --create-node`
上传即建,见 [asset.md](./asset.md)。

## 可写节点类型只有 5 种

| 类型 | 用途 |
| --- | --- |
| `image-input` | 图片输入 |
| `audio-input` | 音频输入 |
| `video-input` | 视频输入 |
| `image-generator` | 生图 |
| `video-generator` | 生视频 |

其余类型(`preview`、`text-node`、Agent 产物、后处理节点)**只读**,可以引用但不能创建或修改;
在 [`canvas`](./canvas.md) 的输出里它们标 `writable=false`。

## 要点

- **生成节点必须显式 `--model`**。服务端不会替你挑——历史上大量节点没保存模型,
  那些结果已无法追溯用了哪个模型。
- `--param` 用可重复的数组参数,**值里的逗号不会被拆**(中文提示词几乎必然含逗号)。
  数字/布尔按字面推断类型;`prompt` 恒为字符串;需要强制字符串用 `--param-string`。
- **多张参考图的顺序 = `--left` 的传入顺序**,不需要额外的顺序字段。
- **提示词里精确指定某张图,用 `@[图片:<图片节点ID>]`**(视频/音频同理:`@[视频:<节点ID>]`
  / `@[音频:<节点ID>]`)。只能引用已用 `--left` 连上的上游节点;提交生成时服务端把标记按
  当前连线里同类型的位置翻译成模型认识的「图片1」「图片2」纯文本,与网页端提交完全一致。
  引用未连线/已断线节点的标记会被直接剔除;`image-generator` 只认图片引用。按节点 ID 引用
  而不是手写「图片1」的好处:连线增删时位置自动重算,不会指错图。
- `--left` 是「确保这条边存在」,`--left-rm` 是「确保这条边不存在」;两者都幂等,
  本来就没有的边执行 `--left-rm` 视为成功。
- 建节点与建连线**都不会**提交生成(不产生上游费用)。
- **`--snapshot-version` 是可选的,别每条命令都带。** 服务端本来就靠行锁串行化写入,
  局部更新只改你传的字段,不带版本号连续写也不会互相覆盖。只有「先读画布、据此决定
  怎么改」的写入才需要它(比如看到某节点没上游才去连边);盲写(坐标/参数是你自己算的,
  与别人改了什么无关)不带,省掉每次写前的 `canvas get`。

建完图,运行前先 `miaoju node get <节点ID>` 看 `derivedInputs`
(见 [canvas.md](./canvas.md)),再走 [run.md](./run.md)。

完整选项以 `miaoju node <子命令> --help` / `miaoju edge <子命令> --help` 为准。
