# `miaoju asset` — 素材上传与结果下载

把本地文件送进画布,把生成结果取回本地。上传和下载是画布与本地文件系统之间唯一的两个口子。

前置:已按 [project.md](./project.md) 绑定项目。

## 子命令

| 子命令 | 作用 |
| --- | --- |
| **`miaoju asset upload <文件路径>`** | 上传参考图/音频/视频;`--create-node` 顺带建输入节点 |
| **`miaoju asset download`** | 按 `--node` 或 `--asset` 取结果到本地 |

## 上传

```sh
# 上传参考图/音频/视频并直接建成输入节点(按 MIME 与扩展名自动判类型)
miaoju asset upload ./ref.png --create-node
miaoju asset upload ./clip.mp4 --create-node          # 建 video-input 节点
miaoju asset upload ./x.bin --create-node --node-type video-input   # 判断不了时显式指定
```

- `--create-node` 按 **MIME 与扩展名**自动判类型,落到 `image-input` / `audio-input` /
  `video-input` 三者之一(见 [node.md](./node.md) 的可写类型表)。
- 判断不了(比如 `.bin` 这种没信息量的扩展名)时用 `--node-type` 显式指定,
  CLI 不会瞎猜一个类型建出来。
- 大文件走 API 转发上传,尚未做对象存储直传。

拿节点 ID 接着建图的惯用写法:

```sh
REF=$(miaoju asset upload ./ref.png --create-node | jq -r .node.id)
```

## 下载

```sh
miaoju asset download --node n-abc12345 --out ./out.png
miaoju asset download --asset 567 --out ./downloads/
```

`--out` 给文件路径就落成那个文件,给目录就落进目录。

跑生成拿结果的完整顺序见 [run.md](./run.md);端到端示例见
[../examples/minimal-workflow.md](../examples/minimal-workflow.md)。

完整选项以 `miaoju asset <子命令> --help` 为准。
