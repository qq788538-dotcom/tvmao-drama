#!/usr/bin/env bash
# verify-tvmao F2：脚本拆分镜（部分计费，本脚本只跑零成本部分）。
#
# 前提：另一个终端已在跑 `node scripts/ratchet/browser-mode.mjs`（临时 sidecar + Vite）。
# 做什么：读风格列表 → 确认 CLI 建不了脚本节点 → 抢画布编辑权 → 用画布 REST 建一个脚本节点、
#        写两行分镜 → `canvas get` 读回断言 → 浏览器打开该画布截图。
# 不做什么：不调 `POST /api/v1/canvas-script-tasks`（提交就要跑上游模型，计费，关卡 G5），
#          不跑 `miaoju run` / `miaoju node run`，不点界面上的任何生成按钮。全程计费调用 0 次。
#
# 用法：scripts/ratchet/verify-f2.sh T-0201
# 环境：MIAOJU_BIN 指定 miaoju 可执行文件（默认先找 PATH，再从 cli/ 构建到证据目录）。
#      playwright 不在仓库依赖里；装在别处时用 NODE_PATH 指过去，例如
#      NODE_PATH=/tmp/t2-pw/node_modules scripts/ratchet/verify-f2.sh T-0201
# 证据：.ratchet/evidence/<任务号>/f2/ 下的 commands.log（命令与退出码）、*.json、smoke.png、console.log。
# 退出码：0 = F2 零成本部分全部断言通过；1 = 失败（看 commands.log 最后几行）；2 = 用法错误。
set -euo pipefail

TASK="${1:-}"
if [[ ! "$TASK" =~ ^T-[0-9]{4,}$ ]]; then
  echo "用法：$0 T-0201" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
STATE="$ROOT/.ratchet/state/browser-mode.json"
OUT="$ROOT/.ratchet/evidence/$TASK/f2"
mkdir -p "$OUT"
LOG="$OUT/commands.log"
: > "$LOG"

if [[ ! -f "$STATE" ]]; then
  echo "找不到 ${STATE}：先跑 node scripts/ratchet/browser-mode.mjs" >&2
  exit 1
fi

json() { python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2], {'d': d}))" "$@"; }

STATE_PID="$(json "$STATE" "d['pid']")"
if ! kill -0 "$STATE_PID" 2>/dev/null; then
  echo "$STATE 指向的 browser-mode（pid ${STATE_PID}）已经不在了：重新跑 node scripts/ratchet/browser-mode.mjs，等它打印 ready 再来" >&2
  exit 1
fi
ENDPOINT_DIR="$(json "$STATE" "d['endpointDir']")"
export MIAOJU_ENDPOINT_DIR="$ENDPOINT_DIR"

if [[ -n "${MIAOJU_BIN:-}" ]]; then
  M="$MIAOJU_BIN"
elif command -v miaoju >/dev/null 2>&1; then
  M="$(command -v miaoju)"
else
  M="$OUT/miaoju"
  (cd "$ROOT/cli" && go build -o "$M" ./cmd/miaoju)
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/ratchet-f2-XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

# run <证据文件名> <命令…>：记录命令、退出码，stdout 存成证据文件；非零退出码即失败
run() {
  local name="$1"; shift
  local code=0
  "$@" > "$OUT/$name" 2> "$OUT/$name.stderr" || code=$?
  printf '%s exit=%d\n' "$(printf '%q ' "$@" | sed "s#$M#miaoju#")" "$code" >> "$LOG"
  if [[ $code -ne 0 ]]; then
    echo "失败：$* （退出码 ${code}，见 ${OUT}/${name}.stderr）" | tee -a "$LOG" >&2
    exit 1
  fi
}

# run_fail <证据文件名> <预期退出码> <命令…>：预期失败的命令（被测行为），退出码不符才算失败
run_fail() {
  local name="$1" want="$2"; shift 2
  local code=0
  "$@" > "$OUT/$name" 2> "$OUT/$name.stderr" || code=$?
  printf '%s exit=%d\n' "$(printf '%q ' "$@" | sed "s#$M#miaoju#")" "$code" >> "$LOG"
  if [[ $code -ne $want ]]; then
    echo "失败：预期退出码 ${want}，实际 ${code}（见 ${OUT}/${name}.stderr）" | tee -a "$LOG" >&2
    exit 1
  fi
}

check() {
  local desc="$1" expr="$2" file="$3"
  if [[ "$(json "$OUT/$file" "$expr")" == "True" ]]; then
    echo "断言通过：$desc" >> "$LOG"
  else
    echo "断言失败：${desc}（${file}）" | tee -a "$LOG" >&2
    exit 1
  fi
}

# api <证据文件名> <METHOD> <路径> [JSON 请求体文件]
# 打 sidecar 的用户侧接口：端口和 token 从 state 文件的 url 里取，token 不打印、不写进证据。
# 非 2xx 即失败。这里只用于零成本接口（画布读、画布写、编辑权）；生成接口一律不碰。
api() {
  local name="$1" method="$2" path="$3" bodyfile="${4:-}"
  local code=0
  F2_STATE="$STATE" F2_OUT="$OUT/$name.json" F2_METHOD="$method" F2_PATH="$path" F2_BODY="$bodyfile" \
    python3 - <<'PY' || code=$?
import json, os, sys, urllib.request, urllib.error
from urllib.parse import urlparse, parse_qs

st = json.load(open(os.environ['F2_STATE']))
q = parse_qs(urlparse(st['url']).query)
base = 'http://127.0.0.1:%s/api/v1' % q['ratchet_port'][0]
body = None
if os.environ.get('F2_BODY'):
    body = open(os.environ['F2_BODY'], 'rb').read()
req = urllib.request.Request(
    base + os.environ['F2_PATH'],
    data=body,
    method=os.environ['F2_METHOD'],
    headers={'Authorization': 'Bearer ' + q['ratchet_token'][0], 'Content-Type': 'application/json'},
)
try:
    with urllib.request.urlopen(req, timeout=15) as r:
        text = r.read().decode()
except urllib.error.HTTPError as e:
    text = e.read().decode()
    open(os.environ['F2_OUT'], 'w').write(text)
    sys.stderr.write('HTTP %d %s %s\n' % (e.code, os.environ['F2_METHOD'], os.environ['F2_PATH']))
    sys.exit(1)
open(os.environ['F2_OUT'], 'w').write(text)
PY
  printf 'sidecar %s %s exit=%d\n' "$method" "$path" "$code" >> "$LOG"
  if [[ $code -ne 0 ]]; then
    echo "失败：${method} ${path}（退出码 ${code}，见 ${OUT}/${name}.json）" | tee -a "$LOG" >&2
    exit 1
  fi
}

# 1. 建项目并绑定当前目录
run project.json "$M" project create --name "F2 验证 $TASK"
PID="$(json "$OUT/project.json" "d['id']")"
run use.json "$M" project use "$PID"

# 2. 读风格列表（只读，零成本）并断言字段齐全
api styles GET /canvas-script/styles
check "风格列表非空" "len(d['styles']) >= 1" styles.json
check "每个风格都有非空的 name（version 可为空）" "all(isinstance(s.get('name'), str) and s['name'] != '' for s in d['styles'])" styles.json
check "候选模型非空且 modelId/displayName/credits 齐全" "len(d['modelCandidates']) >= 1 and all(set(['modelId','displayName','credits']) <= set(c.keys()) for c in d['modelCandidates'])" styles.json

# 3. CLI 建不了脚本节点：这是被测行为，预期退出码 2 + INVALID_NODE_TYPE
run_fail node-create-script.out 2 "$M" node create --type script
if grep -q INVALID_NODE_TYPE "$OUT/node-create-script.out.stderr"; then
  echo "断言通过：CLI 不能建脚本节点（miaoju node create --type script 退出码 2，INVALID_NODE_TYPE）" >> "$LOG"
else
  echo "断言失败：CLI 建脚本节点的报错里没有 INVALID_NODE_TYPE（见 ${OUT}/node-create-script.out.stderr）" | tee -a "$LOG" >&2
  exit 1
fi

# 4. 抢画布编辑权。必须在打开浏览器之前——页面一打开就会自己去 claim。
printf '{}\n' > claim-body.json
api claim POST "/projects/$PID/canvas/owner/claim" claim-body.json
OT="$(json "$OUT/claim.json" "d['ownerToken']")"
if [[ -z "$OT" ]]; then
  echo "断言失败：claim 没返回 ownerToken（见 ${OUT}/claim.json）" | tee -a "$LOG" >&2
  exit 1
fi
echo "断言通过：抢到画布编辑权（ownerToken 已脱敏，不写进证据）" >> "$LOG"
printf '{"ownerToken":"<redacted>","expiresAt":"%s"}\n' "$(json "$OUT/claim.json" "d['expiresAt']")" > "$OUT/claim.json"

# 5. 建一个脚本节点（`script` 类型只在前端注册，CLI 不可写；画布 REST 不校验类型）
SNODE="f2-script-1"
printf '{"ownerToken":"%s","node":{"id":"%s","type":"script","x":120,"y":120,"width":500,"height":280,"settings":{"scriptPrompt":"F2 验证：雨夜街口","durationSec":15,"rows":[],"assets":{"characters":[],"scenes":[],"props":[]},"globalStyle":"","gen":{"taskId":0,"style":"","model":"","durationSec":15,"error":""}}}}\n' "$OT" "$SNODE" > node-body.json
api node POST "/projects/$PID/canvas/nodes" node-body.json

# 6. 写两行分镜（REST 可写；CLI 视角这节点是只读的）
printf '{"ownerToken":"%s","patch":{"settings":{"rows":[{"id":"r1","shotNumber":1,"name":"测试镜","duration":4,"prompt":"镜头1：雨夜街口","colorMark":"red"},{"id":"r2","shotNumber":2,"name":"第二镜","duration":6,"prompt":"镜头2：推门","colorMark":""}]}}}\n' "$OT" > rows-body.json
api rows PATCH "/projects/$PID/canvas/nodes/$SNODE" rows-body.json

# 7. 用 CLI 读回画布并断言
run canvas.json "$M" canvas get
run node.json "$M" node get "$SNODE"
check "画布恰好 1 个节点" "len(d['nodes']) == 1" canvas.json
check "节点类型是 script" "d['nodes'][0]['type'] == 'script'" canvas.json
check "CLI 视角脚本节点只读（writable 为 false）" "d['nodes'][0]['writable'] is False" canvas.json
check "分镜行读回 2 行" "len(d['nodes'][0]['params']['rows']) == 2" canvas.json
check "第一行是刚写的红色标记" "d['nodes'][0]['params']['rows'][0]['colorMark'] == 'red' and d['nodes'][0]['params']['rows'][1]['name'] == '第二镜'" canvas.json
check "node get 也读到同一个 id" "d['id'] == 'f2-script-1'" node.json

# 8. 浏览器里打开这张画布，截图、记 console
smoke_code=0
node "$ROOT/scripts/ratchet/browser-smoke.mjs" --task "$TASK" --sub f2 --path "/canvas/$PID" --wait-selector '[data-node-id]' --expect-count 1 > "$OUT/smoke-result.json" || smoke_code=$?
printf 'node scripts/ratchet/browser-smoke.mjs --task %s --sub f2 --path /canvas/%s --wait-selector [data-node-id] --expect-count 1 exit=%d\n' "$TASK" "$PID" "$smoke_code" >> "$LOG"
check "浏览器里页面连上 sidecar、画出 1 个节点、无页面错误" "d['ok'] is True and d['waitFor']['count'] == 1" smoke.json

# 不走管道：管道里展开出错时，退出码可能被右边的 tee 盖住（macOS bash 3.2 上见过 EXIT=0 却没写这行）
DONE_MSG="F2 通过（零成本部分）：项目 ${PID}，脚本节点 ${SNODE}，计费调用 0 次。证据：${OUT}"
printf '%s\n' "$DONE_MSG" >> "$LOG"
printf '%s\n' "$DONE_MSG"
