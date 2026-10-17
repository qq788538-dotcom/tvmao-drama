import { nanoid } from 'nanoid';
import {
  createScriptRow,
  renumberRows,
  MIN_SHOT_SECONDS,
  DEFAULT_DURATION_SEC,
  type ScriptAsset,
  type ScriptAssets,
  type ScriptRow,
  type ScriptSettings,
} from './scriptData';

// skill 产物 candidate.json → 脚本节点 settings 的导入映射（唯一一份）：
// - 顶层 character/scene/item → 步骤 2 资产：prompt → description，按 name 匹配已有
//   资产（保留 linkedNodeId——重新生成不该把用户配好的图打回原形），没有才新建。
// - storyboard[] → rows：name/duration/prompt 原样，token 保持 @[类型-名称] 不翻译。
// - storyboard[].character/scene/item 数组丢弃：prompt 内 token 即真源，存两份必然漂移。

interface CandidateNamedPrompt {
  name?: unknown;
  prompt?: unknown;
}

interface CandidateStoryboard extends CandidateNamedPrompt {
  duration?: unknown;
}

interface CandidateDoc {
  character?: CandidateNamedPrompt[];
  scene?: CandidateNamedPrompt[];
  item?: CandidateNamedPrompt[];
  storyboard?: CandidateStoryboard[];
}

function mergeAssets(existing: ScriptAsset[], incoming: CandidateNamedPrompt[] | undefined): ScriptAsset[] {
  const out = [...existing];
  for (const it of incoming ?? []) {
    const name = typeof it.name === 'string' ? it.name.trim() : '';
    if (!name) continue;
    const description = typeof it.prompt === 'string' ? it.prompt : '';
    const idx = out.findIndex((a) => a.name === name);
    if (idx >= 0) {
      // 同名资产已存在：只刷新描述，linkedNodeId（用户已配的图）原样保留。
      out[idx] = { ...out[idx], description };
    } else {
      out.push({ id: nanoid(8), name, description, linkedNodeId: '' });
    }
  }
  return out;
}

// 解析失败抛错（由调用方落到 gen.error 展示）；成功返回要 patch 进 settings 的分片。
export function importCandidate(
  content: string,
  current: ScriptSettings,
): Pick<ScriptSettings, 'rows' | 'assets'> {
  let doc: CandidateDoc;
  try {
    doc = JSON.parse(content) as CandidateDoc;
  } catch {
    throw new Error('生成结果不是合法 JSON');
  }
  const storyboard = Array.isArray(doc.storyboard) ? doc.storyboard : [];
  if (storyboard.length === 0) {
    throw new Error('生成结果缺少分镜（storyboard 为空）');
  }

  const rows: ScriptRow[] = renumberRows(
    storyboard.map((sb) => {
      const duration = typeof sb.duration === 'number' ? Math.round(sb.duration) : MIN_SHOT_SECONDS;
      return {
        ...createScriptRow(),
        name: typeof sb.name === 'string' ? sb.name : '',
        duration: Math.min(Math.max(duration, MIN_SHOT_SECONDS), DEFAULT_DURATION_SEC),
        prompt: typeof sb.prompt === 'string' ? sb.prompt : '',
      };
    }),
  );

  const assets: ScriptAssets = {
    characters: mergeAssets(current.assets.characters, doc.character),
    scenes: mergeAssets(current.assets.scenes, doc.scene),
    props: mergeAssets(current.assets.props, doc.item),
  };

  return { rows, assets };
}
