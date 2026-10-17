import type { CanvasNode } from '../types';
import type { NodeSummary } from './types';

// buildCanvasSummary 只服务 @ mention 的候选列表（MentionPicker），不进 agent
// 的 prompt——传输层只发用户显式 @ 的 mention（见 agentClient 测试里
// 「never includes canvas_summary」的约束），所以这里没有 context 预算问题，
// 不做数量截断：截断会让老节点永远 @ 不到。返回顺序倒过来（最新的在前）——
// 画布数组是按创建顺序 append 的，不反转的话候选列表会从最早的节点开始列，
// 而用户十有八九要引用的是刚生成的那几个。
export function buildCanvasSummary(nodes: CanvasNode[]): NodeSummary[] {
  // 把用户上传的输入（input-image / video-input / input-audio）跟生成的输出
  // 一起纳入——@ mention 应该能引用到任意媒体节点。文字节点（text-node）
  // 不在此表里，下面按 settings.text 非空单独纳入（kind='text'，无 URL）。
  const MEDIA_TYPES = new Set<string>([
    'preview',
    'gen-image',
    'gen-video',
    'input-image',
    'video-input',
    'input-audio',
  ]);
  const candidates = nodes
    .filter((n) => {
      // 文字节点没有 content（正文存 settings.text）——非空才有引用价值。
      // @ 它的语义是把正文内联进消息文本（没有 URL，不进 mentions[]）。
      if (n.type === 'text-node') {
        const t = (n.settings as { text?: unknown } | undefined)?.text;
        return typeof t === 'string' && t.length > 0;
      }
      return typeof n.content === 'string' && n.content.length > 0 && MEDIA_TYPES.has(n.type);
    })
    .map<NodeSummary>((n) => {
      const settings = (typeof n.settings === 'object' && n.settings !== null
        ? (n.settings as { name?: unknown; kind?: unknown; text?: unknown })
        : {}) as { name?: unknown; kind?: unknown; text?: unknown };
      if (n.type === 'text-node') {
        const text = typeof settings.text === 'string' ? settings.text : '';
        // 名字优先 settings.name；没有就取正文前 20 字当标签——候选列表和
        // chip 都需要一个人能认出来的名字，纯 id 没法选。
        const name =
          typeof settings.name === 'string' && settings.name
            ? settings.name
            : text.slice(0, 20);
        return { id: n.id, type: n.type, name, url: '', kind: 'text', text };
      }
      // 判定媒体 kind：
      //   - input-image / gen-image → image
      //   - video-input / gen-video → video
      //   - input-audio             → audio
      //   - preview                 → 看 settings.kind（image/video）
      let kind: 'image' | 'video' | 'audio';
      if (n.type === 'video-input' || n.type === 'gen-video') {
        kind = 'video';
      } else if (n.type === 'input-audio') {
        kind = 'audio';
      } else if (n.type === 'preview' && settings.kind === 'video') {
        kind = 'video';
      } else if (n.type === 'preview' && settings.kind === 'audio') {
        kind = 'audio';
      } else {
        kind = 'image';
      }
      return {
        id: n.id,
        type: n.type,
        name: typeof settings.name === 'string' ? settings.name : '',
        url: typeof n.content === 'string' ? n.content : '',
        kind,
      };
    });
  return candidates.reverse();
}
