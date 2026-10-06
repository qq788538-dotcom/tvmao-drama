import { useCallback } from 'react';
import { useCanvasStore } from '../../store';
import { scheduleNodePatch } from '../../persistence/canvasMutations';
import {
  createScriptRow,
  readScriptSettings,
  renumberRows,
  type ScriptRow,
  type ScriptSettings,
} from './scriptData';

// 脚本面板内所有写操作的唯一收口。任何组件想改 settings，必须经这里的方法，
// 不许直接调 store.updateNodeSettings ——统一写路径才能保证「内存即时 + 落库
// 防抖」两条腿一起动，不会有组件漏了落库那一半。
//
// 写路径：内存走 updateNodeSettings（zustand set，UI 立即响应），落库走
// scheduleNodePatch（1.5s 防抖，整包 settings 一起送，不是逐字段增量——
// 后端 patch 语义是浅合并，整包送比拼字段 diff 简单且不会漏字段）。
export function useScriptSettings(nodeId: string): {
  settings: ScriptSettings;
  patch: (p: Partial<ScriptSettings>) => void;
  updateRow: (rowId: string, p: Partial<ScriptRow>) => void;
  addRow: () => void;
  removeRow: (rowId: string) => void;
  moveRow: (rowId: string, toIndex: number) => void;
  replaceRows: (rows: ScriptRow[]) => void;
} {
  const rawSettings = useCanvasStore((s) => s.nodes.find((n) => n.id === nodeId)?.settings);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const settings = readScriptSettings(rawSettings);

  // 读当前最新 settings（而不是闭包捕获的渲染期 settings）：同一批同步调用
  // （比如一个 act() 里连打两次 addRow）之间 React 不会重渲染，闭包里的
  // settings 还是旧值。改从 store.getState() 现读，保证连续调用彼此看得见
  // 对方刚写入的结果，不会互相覆盖。
  const currentSettings = useCallback((): ScriptSettings => {
    const node = useCanvasStore.getState().nodes.find((n) => n.id === nodeId);
    return readScriptSettings(node?.settings);
  }, [nodeId]);

  // 统一写出口：先落内存（即时反映到 UI），再排队落库（防抖，避免逐字敲键盘
  // 就打一次网络请求）。两步顺序固定——内存先行是为了让本次 render 立刻拿到
  // 新值，落库是异步的旁路，不影响这次交互的即时性。
  const write = useCallback((next: ScriptSettings) => {
    updateNodeSettings(nodeId, next as unknown as Record<string, unknown>);
    scheduleNodePatch(nodeId, { settings: next as unknown as Record<string, unknown> });
  }, [nodeId, updateNodeSettings]);

  const patch = useCallback((p: Partial<ScriptSettings>) => {
    write({ ...currentSettings(), ...p });
  }, [currentSettings, write]);

  // skill 对齐版没有提示词状态机（prompt 即唯一真源，没有"从素材拼接"这一步），
  // 行编辑就是纯 merge + 镜号重排。
  const updateRow = useCallback((rowId: string, p: Partial<ScriptRow>) => {
    const cur = currentSettings();
    const rows = renumberRows(cur.rows.map((r) => (r.id === rowId ? { ...r, ...p } : r)));
    write({ ...cur, rows });
  }, [currentSettings, write]);

  const addRow = useCallback(() => {
    const cur = currentSettings();
    const rows = renumberRows([...cur.rows, createScriptRow()]);
    write({ ...cur, rows });
  }, [currentSettings, write]);

  const removeRow = useCallback((rowId: string) => {
    const cur = currentSettings();
    const rows = renumberRows(cur.rows.filter((r) => r.id !== rowId));
    write({ ...cur, rows });
  }, [currentSettings, write]);

  const moveRow = useCallback((rowId: string, toIndex: number) => {
    const cur = currentSettings();
    const fromIndex = cur.rows.findIndex((r) => r.id === rowId);
    if (fromIndex === -1 || toIndex < 0 || toIndex >= cur.rows.length) return;
    const next = [...cur.rows];
    const [moved] = next.splice(fromIndex, 1);
    next.splice(toIndex, 0, moved);
    write({ ...cur, rows: renumberRows(next) });
  }, [currentSettings, write]);

  const replaceRows = useCallback((rows: ScriptRow[]) => {
    write({ ...currentSettings(), rows: renumberRows(rows) });
  }, [currentSettings, write]);

  return { settings, patch, updateRow, addRow, removeRow, moveRow, replaceRows };
}
