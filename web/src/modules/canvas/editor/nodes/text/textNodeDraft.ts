// 文本节点内容的持久化三件套收口。updateNodeSettings 只改内存（画布没有全量
// 自动保存），必须「本地更新 + debounce 落库 + 失焦冲刷」三步齐全——TextNode
// 曾经漏掉后两步，打完字一刷新全丢，上过生产的真实回归（见
// textNodePersistence.test.tsx）。收成独立函数是为了让 Tiptap 的 onUpdate/onBlur
// 和展开编辑弹层共用同一条落库路径，也让回归测试不必穿透 ProseMirror 模拟打字。
import { useCanvasStore } from '../../store';
import { flushNodePatch, scheduleNodePatch } from '../../persistence/canvasMutations';

export function commitTextDraft(nodeId: string, markdown: string) {
  useCanvasStore.getState().updateNodeSettings(nodeId, { text: markdown });
  scheduleNodePatch(nodeId, { settings: { text: markdown } });
}

export function flushTextDraft(nodeId: string): Promise<void> {
  return flushNodePatch(nodeId);
}
