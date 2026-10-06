import type { UISchemaField } from '@/shared/api';
import { useCanvasStore, DEFAULT_SIZES } from '../../store';
import { persistNodeById } from '../../persistence/canvasMutations';
import { translatePromptForVideo, type TranslatedPrompt } from './scriptTokens';
import type { ScriptAssets, ScriptRow } from './scriptData';

// 批量生视频（skill 对齐版）：点击那一刻做「skill 语义 → 画布节点语义」的单向翻译
// （@[类型-名称] → @[图片:<linkedNodeId>]，见 scriptTokens.translatePromptForVideo），
// 然后只铺 gen-video 节点不提交生成（铁律：生成必须用户逐节点手动点）。

export interface BatchVideoItem {
  rowId: string;
  /** uiSchema duration 档位值；模型无 duration 字段时为 undefined */
  duration?: string | number;
}

/** 弹层默认档位：离该行 duration 最近的合法档位（skill 的 duration 是 4~N 整秒，
 *  模型档位未必逐秒，就近取）。 */
export function pickClosestDurationOption(field: UISchemaField, seconds: number): string | number | undefined {
  const options = field.options ?? [];
  if (options.length === 0) return field.default;
  let best = options[0].value;
  let bestDiff = Number.POSITIVE_INFINITY;
  for (const o of options) {
    const n = Number(o.value);
    const diff = Number.isFinite(n) ? Math.abs(n - seconds) : Number.POSITIVE_INFINITY;
    if (diff < bestDiff) {
      bestDiff = diff;
      best = o.value;
    }
  }
  return best;
}

/** 行的翻译预览（弹层用）：缺图警告名单 + 参考图节点。存活判定读当前 store。 */
export function translateRow(row: ScriptRow, assets: ScriptAssets): TranslatedPrompt {
  const nodes = useCanvasStore.getState().nodes;
  return translatePromptForVideo(row.prompt, assets, (id) => nodes.some((n) => n.id === id));
}

/** 画布上已为该分镜行铺过的 gen-video 节点数（防误翻倍的提示用）。 */
export function spawnedCountForRow(rowId: string): number {
  return useCanvasStore.getState().nodes.filter(
    (n) => n.type === 'gen-video' && n.settings?.scriptRowId === rowId,
  ).length;
}

/** 铺节点：每镜一个 gen-video，写翻译后的 videoPrompt + modelId + duration 真字段 +
 *  scriptRowId 反向锚；连线=每个被引用图片节点→gen-video、脚本节点→gen-video。
 *  不提交任何生成任务。返回新节点 id 数组。 */
export function spawnVideoNodes(
  scriptNodeId: string,
  items: BatchVideoItem[],
  modelId: string,
  extraSettings?: Record<string, unknown>,
): string[] {
  const store = useCanvasStore.getState();
  const script = store.nodes.find((n) => n.id === scriptNodeId);
  if (!script) return [];
  const rowById = new Map(
    (Array.isArray(script.settings?.rows) ? (script.settings!.rows as ScriptRow[]) : []).map((r) => [r.id, r]),
  );
  const assets = (script.settings?.assets ?? { characters: [], scenes: [], props: [] }) as ScriptAssets;
  const size = DEFAULT_SIZES['gen-video'];
  const created: string[] = [];

  items.forEach((item, i) => {
    const row = rowById.get(item.rowId);
    if (!row) return;
    const { text, refNodeIds } = translateRow(row, assets);
    // addNode 把传入坐标当中心点（store.addNode 内部 -width/2），这里补偿成
    // 「左上角落在脚本节点右侧 120px、纵向按序错开」的直觉布局。
    const anchor = {
      x: script.x + script.width + 120 + size.width / 2,
      y: script.y + i * (size.height + 40) + size.height / 2,
    };
    const newId = store.addNode('gen-video', anchor);
    if (!newId) return;
    store.updateNodeSettings(newId, {
      videoPrompt: text,
      modelId,
      scriptRowId: row.id,
      // 节点标题用分镜简述（NodeShell 的 settings.name 自定义名机制）——一排
      // 「AI 视频」认不出哪个是哪镜。简述为空时不写,回退默认类型标签。
      ...(row.name.trim() !== '' ? { name: row.name.trim() } : {}),
      // 弹框底栏选的 uiSchema 参数（比例/分辨率等）逐节点落库——用户在节点上点
      // 生成时 buildDynamicInputs 按字段名读到它们。
      ...(extraSettings ?? {}),
      ...(item.duration !== undefined ? { duration: item.duration } : {}),
    });
    persistNodeById(newId);
    // 只连资产参考图；脚本节点→视频节点的派生连线用户明确不要（画布上一坨扇形
    // 连线噪音大，scriptRowId 反向锚已经足够表达归属）。
    for (const refId of refNodeIds) store.addConnection(refId, newId);
    created.push(newId);
  });
  return created;
}
