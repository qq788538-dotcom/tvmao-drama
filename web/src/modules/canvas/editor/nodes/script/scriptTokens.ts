import type { ScriptAsset, ScriptAssets } from './scriptData';

// skill 语义 token 工具：@[角色-张三] / @[场景-修理铺] / @[物品-信封]。
// 这是脚本节点 prompt 的原生格式（Drama Skill candidate.json 同款）——注意与画布
// 节点语义的 @[图片:<nodeId>]（utils/mentions.ts）严格分层：脚本行里永远存
// 类型-名称，只有点「批量生视频」翻译成节点格式的那一刻两层才接触。

export type TokenKind = 'character' | 'scene' | 'prop';

// 中文标签 ↔ ScriptAssets 字段的对照。skill 用「物品」，我们内部字段沿用 props
// （改字段名要动快照数据形状，纯展示层面的对齐不值得破坏 key）。
const KIND_BY_LABEL: Record<string, TokenKind> = { 角色: 'character', 场景: 'scene', 物品: 'prop' };
export const LABEL_BY_KIND: Record<TokenKind, string> = { character: '角色', scene: '场景', prop: '物品' };
const SECTION_BY_KIND: Record<TokenKind, keyof ScriptAssets> = {
  character: 'characters',
  scene: 'scenes',
  prop: 'props',
};

// 名称禁 ]（token 边界符）。skill 侧同约束。
export const SCRIPT_TOKEN_RE = /@\[(角色|场景|物品)-([^\]]+)\]/g;

export interface ScriptTokenRef {
  kind: TokenKind;
  name: string;
  /** 名称+类型能在资产表里找到则为该资产，否则 undefined（断链） */
  asset?: ScriptAsset;
}

export function makeToken(kind: TokenKind, name: string): string {
  return `@[${LABEL_BY_KIND[kind]}-${name}]`;
}

function findAsset(assets: ScriptAssets, kind: TokenKind, name: string): ScriptAsset | undefined {
  return assets[SECTION_BY_KIND[kind]].find((a) => a.name === name);
}

/** 解析 prompt 里的全部 token（按出现顺序，同一 token 多次出现会重复出现在结果里）。 */
export function parseTokens(prompt: string, assets: ScriptAssets): ScriptTokenRef[] {
  const out: ScriptTokenRef[] = [];
  for (const m of prompt.matchAll(SCRIPT_TOKEN_RE)) {
    const kind = KIND_BY_LABEL[m[1]];
    out.push({ kind, name: m[2], asset: findAsset(assets, kind, m[2]) });
  }
  return out;
}

/** 资产改名时的全表联动：把所有行 prompt 里的 @[类型-旧名] 换成 @[类型-新名]。
 *  名称是 token 的外键，改名不联动 = 全部引用当场断链。 */
export function renameTokenInPrompt(prompt: string, kind: TokenKind, oldName: string, newName: string): string {
  return prompt.split(makeToken(kind, oldName)).join(makeToken(kind, newName));
}

export interface TranslatedPrompt {
  /** 翻译后的文本：可解析 token → @[图片:<linkedNodeId>]；断链/缺图 token → 纯名字 */
  text: string;
  /** 需要连线到 gen-video 的图片节点 id（去重，按首次出现顺序） */
  refNodeIds: string[];
  /** 缺图（无 linkedNodeId 或名称找不到资产）的 token 名单（去重） */
  missing: { kind: TokenKind; name: string }[];
}

/** 批量生视频那一刻的单向翻译：skill 语义 → 画布节点语义。
 *  aliveNodeId：调用方传入「节点仍存在」的判定（资产的 linkedNodeId 可能指向已删节点）。
 *  缺图 token 退化为纯名字文本——绝不把 @[类型-名称] 这种 skill 语法漏给视频模型。 */
export function translatePromptForVideo(
  prompt: string,
  assets: ScriptAssets,
  aliveNodeId: (nodeId: string) => boolean,
): TranslatedPrompt {
  const refNodeIds: string[] = [];
  const missing: TranslatedPrompt['missing'] = [];
  const missingKey = new Set<string>();
  const text = prompt.replace(SCRIPT_TOKEN_RE, (_full, label: string, name: string) => {
    const kind = KIND_BY_LABEL[label];
    const asset = findAsset(assets, kind, name);
    const nodeId = asset?.linkedNodeId;
    if (asset && nodeId && aliveNodeId(nodeId)) {
      if (!refNodeIds.includes(nodeId)) refNodeIds.push(nodeId);
      return `@[图片:${nodeId}]`;
    }
    const key = `${kind}:${name}`;
    if (!missingKey.has(key)) {
      missingKey.add(key);
      missing.push({ kind, name });
    }
    return name;
  });
  return { text, refNodeIds, missing };
}
