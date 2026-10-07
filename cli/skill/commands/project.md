# `miaoju project` — 项目(画布)

自由画布里一个「项目」就是一张画布。本命令负责增删改查,以及**把当前工作目录绑定到某个项目**——
绑定之后 [`node`](./node.md) / [`canvas`](./canvas.md) / [`asset`](./asset.md) /
[`compliance`](./compliance.md) 这些命令都不用再重复传 `--project`。

## 子命令

| 子命令 | 作用 |
| --- | --- |
| **`miaoju project list`** | 列出本机喵剧里的项目 |
| **`miaoju project create`** | 新建项目;`--use` 顺带绑定当前目录 |
| **`miaoju project use <项目ID>`** | 在当前目录写 `.miaoju/project.json` |
| **`miaoju project get [项目ID]`** | 项目详情;省略 ID 时用目录绑定 |
| **`miaoju project update [项目ID]`** | 改名称 / 简介 |
| **`miaoju project delete <项目ID>`** | 删除项目(需 `--yes`) |
| **`miaoju project unuse`** | 解除当前目录的绑定 |

```sh
miaoju project list
miaoju project create --name "我的画布" --use     # --use 顺带绑定当前目录
miaoju project use 42                            # 在当前目录写 .miaoju/project.json
miaoju project get                               # 省略 ID 时用绑定
miaoju project update --description "新简介"
miaoju project delete 42 --yes
miaoju project unuse
```

## 绑定的两条规则

- **只看当前目录,不向父目录查找**。换句话说 `cd` 到子目录后绑定就不生效了——
  这是刻意的:向上查找会让「我以为在改 A 项目,其实在改父目录绑的 B 项目」变成
  一类查不出来的事故。

未绑定又没传 `--project` 时**直接报错**,不会猜最近用过的项目。

完整选项以 `miaoju project <子命令> --help` 为准。
