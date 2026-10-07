# 端到端最小工作流:建项目 → 上传参考图 → 建生成节点 → 跑 → 取结果

**覆盖范围**:一条龙走完一次生成所需的全部命令,以及其中两处必须做的检查
(先查模型能力、运行前反查 `derivedInputs`)。适合直接抄成脚本。

**先确认喵剧桌面端已打开。**

**前置条件**:

- 本机有 `jq`(下面的命令块靠它抠 ID)。
- 本地有一张参考图 `./ref.png`。

```sh
PID=$(miaoju project create --name "自动化画布" | jq -r .id)
miaoju project use "$PID"

# 先看模型能力,别猜时长与比例上限
miaoju model list --type video
miaoju model get <模型ID> | jq .inputSchema

REF=$(miaoju asset upload ./ref.png --create-node | jq -r .node.id)
GEN=$(miaoju node create --type video-generator --model <模型ID> \
        --param prompt="镜头缓慢推进" --param duration=5 \
        --left "$REF" | jq -r .node.id)

# 运行前反向检查:derivedInputs 会列出该节点实际会拿到的图/文/视频/音频及顺序
miaoju node get "$GEN" | jq '{model: .params.modelId, inputs: .derivedInputs, outputs}'

miaoju node run "$GEN" --wait
miaoju asset download --node "$GEN" --out ./out.mp4
```

用 `doubao-seedance*` 时,在 `node run` 之前插一步合规校验(先让输入素材有内容、
`verify` 提交、`status` 等到 `active`),见 [../commands/compliance.md](../commands/compliance.md)。

**创建与执行严格分离**:可以先把整张图搭完、审核一遍,再决定是否运行(运行才会调用上游模型)。
`node create` 与 `edge create` 永远不会隐式触发生成。

各步细节:[../commands/project.md](../commands/project.md)、
[../commands/model.md](../commands/model.md)、
[../commands/asset.md](../commands/asset.md)、
[../commands/node.md](../commands/node.md)、
[../commands/run.md](../commands/run.md)。
