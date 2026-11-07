#!/usr/bin/env bash
# verify-tvmao F1：建图与连线（零成本）。
#
# 前提：另一个终端已在跑 `node scripts/ratchet/browser-mode.mjs`（临时 sidecar + Vite）。
# 做什么：在临时 sidecar 上新建项目 → 上传一张参考图（自动建 image-input 节点）→
#        建 image-generator 节点并连上 → 摆位 → 读回画布并断言 → 浏览器打开该画布截图。
# 不做什么：不跑 `node run`，不调用任何上游模型，不计费。
#
# 用法：scripts/ratchet/verify-f1.sh T-0001
# 环境：MIAOJU_BIN 指定 miaoju 可执行文件（默认先找 PATH，再从 cli/ 构建到证据目录）。
# 证据：.ratchet/evidence/<任务号>/f1/ 下的 commands.log（命令与退出码）、*.json、smoke.png、console.log。
# 退出码：0 = F1 全部断言通过；1 = 失败（看 commands.log 最后几行）。
set -euo pipefail

TASK="${1:-}"
if [[ ! "$TASK" =~ ^T-[0-9]{4,}$ ]]; then
  echo "用法：$0 T-0001" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
STATE="$ROOT/.ratchet/state/browser-mode.json"
OUT="$ROOT/.ratchet/evidence/$TASK/f1"
mkdir -p "$OUT"
LOG="$OUT/commands.log"
: > "$LOG"

if [[ ! -f "$STATE" ]]; then
  echo "找不到 $STATE：先跑 node scripts/ratchet/browser-mode.mjs" >&2
  exit 1
fi

json() { python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2], {'d': d}))" "$@"; }

STATE_PID="$(json "$STATE" "d['pid']")"
if ! kill -0 "$STATE_PID" 2>/dev/null; then
  echo "$STATE 指向的 browser-mode（pid $STATE_PID）已经不在了：重新跑 node scripts/ratchet/browser-mode.mjs，等它打印 ready 再来" >&2
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

WORK="$(mktemp -d "${TMPDIR:-/tmp}/ratchet-f1-XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

# run <证据文件名> <命令…>：记录命令、退出码，stdout 存成证据文件；非零退出码即失败
run() {
  local name="$1"; shift
  local code=0
  "$@" > "$OUT/$name" 2> "$OUT/$name.stderr" || code=$?
  printf '%s exit=%d\n' "$(printf '%q ' "$@" | sed "s#$M#miaoju#")" "$code" >> "$LOG"
  if [[ $code -ne 0 ]]; then
    echo "失败：$* （退出码 $code，见 $OUT/$name.stderr）" | tee -a "$LOG" >&2
    exit 1
  fi
}

check() {
  local desc="$1" expr="$2" file="$3"
  if [[ "$(json "$OUT/$file" "$expr")" == "True" ]]; then
    echo "断言通过：$desc" >> "$LOG"
  else
    echo "断言失败：$desc（$file）" | tee -a "$LOG" >&2
    exit 1
  fi
}

# 1. 一张 64×64 的纯色 PNG 当参考图（不需要任何外部文件）
python3 - <<'PY'
import struct, zlib
w = h = 64
raw = b''.join(b'\x00' + bytes([200, 80, 80]) * w for _ in range(h))
def chunk(t, d):
    return struct.pack('>I', len(d)) + t + d + struct.pack('>I', zlib.crc32(t + d) & 0xffffffff)
open('ref.png', 'wb').write(b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 2, 0, 0, 0))
                           + chunk(b'IDAT', zlib.compress(raw)) + chunk(b'IEND', b''))
PY

# 2. 建项目并绑定当前目录
run project.json "$M" project create --name "F1 验证 $TASK"
PID="$(json "$OUT/project.json" "d['id']")"
run use.json "$M" project use "$PID"

# 3. 选一个图片模型（只读目录，不运行）
run models.json "$M" model list
MODEL="$(json "$OUT/models.json" "next(m['modelId'] for m in (d if isinstance(d, list) else d.get('items', d.get('models', []))) if m.get('kind') == 'image' and m.get('available'))")"

# 4. 上传参考图并建 image-input 节点
run upload.json "$M" asset upload ./ref.png --create-node
IMG="$(json "$OUT/upload.json" "d['node']['id']")"

# 5. 建 image-generator 节点并连上参考图（建图不触发生成）
run generator.json "$M" node create --type image-generator --model "$MODEL" --param prompt="F1 验证：雨后城市街道" --left "$IMG"
GEN="$(json "$OUT/generator.json" "d['node']['id']")"

# 6. 摆位，避免两个节点叠在一起，截图看得清
run move.json "$M" node move --set "$IMG=0,0" --set "$GEN=420,0"

# 7. 读回画布与生成节点的实际输入并断言
run canvas.json "$M" canvas get
run gen-node.json "$M" node get "$GEN"
check "画布恰好 2 个节点" "len(d['nodes']) == 2" canvas.json
check "画布恰好 1 条连线" "len(d['edges']) == 1" canvas.json
check "连线方向是 参考图 → 生成节点" "d['edges'][0]['fromNodeId'] == '$IMG' and d['edges'][0]['toNodeId'] == '$GEN'" canvas.json
ASSET_PATH="$(json "$OUT/upload.json" "d['asset']['url'].split('?')[0].split('/media/')[1]")"
check "生成节点实际会拿到 1 张图，且就是上传的参考图（derivedInputs）" "[u.split('?')[0].split('/media/')[1] for u in d['derivedInputs']['images']] == ['$ASSET_PATH']" gen-node.json

# 8. 浏览器里打开这张画布，截图、记 console
smoke_code=0
node "$ROOT/scripts/ratchet/browser-smoke.mjs" --task "$TASK" --sub f1 --path "/canvas/$PID" > "$OUT/smoke-result.json" || smoke_code=$?
printf 'node scripts/ratchet/browser-smoke.mjs --task %s --sub f1 --path /canvas/%s exit=%d\n' "$TASK" "$PID" "$smoke_code" >> "$LOG"
check "浏览器里页面连上 sidecar 且无页面错误" "d['ok'] is True" smoke.json

echo "F1 通过：项目 $PID，节点 $IMG → $GEN，模型 $MODEL。证据：$OUT" | tee -a "$LOG"
