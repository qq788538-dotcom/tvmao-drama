import { useState } from 'react';
import { Check, Copy, FileJson, Loader2, Redo2, Save, Trash2, Undo2, Workflow, ZoomIn, ZoomOut } from 'lucide-react';
import { useCanvasStore } from './store';
import { flushCanvasMutations } from './persistence/canvasMutations';
import { saveLocalView } from './persistence/localView';
import { useIsSuperAdmin } from '@/shared/permissions';
import { MAX_ZOOM, MIN_ZOOM } from './utils/viewGestures';
import { WorkflowExportModal } from './WorkflowExportModal';
import { WorkflowImportModal } from './WorkflowImportModal';

interface ToolbarProps {
  projectId: number;
  onSaved?: (ts: string) => void;
  onError?: (err: unknown) => void;
}

export function Toolbar({ projectId, onSaved, onError }: ToolbarProps) {
  const undo = useCanvasStore((s) => s.undo);
  const redo = useCanvasStore((s) => s.redo);
  const deleteNodes = useCanvasStore((s) => s.deleteNodes);
  const duplicateNodes = useCanvasStore((s) => s.duplicateNodes);
  const view = useCanvasStore((s) => s.view);
  const setView = useCanvasStore((s) => s.setView);

  const [saveState, setSaveState] = useState<'idle' | 'saving' | 'done'>('idle');
  const isSuperAdmin = useIsSuperAdmin();
  const [exportOpen, setExportOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);

  const zoomAt = (factor: number) => {
    const nextZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, view.zoom * factor));
    const nextView = { ...view, zoom: nextZoom };
    setView(nextView);
    if (projectId) saveLocalView(projectId, nextView);
  };

  // 「立即保存」flush 局部 mutation 队列，不再提交整张快照。
  const handleSave = async () => {
    if (saveState === 'saving') return;
    setSaveState('saving');
    try {
      await flushCanvasMutations();
      onSaved?.(new Date().toISOString());
      setSaveState('done');
      setTimeout(() => setSaveState('idle'), 1500);
    } catch (err) {
      onError?.(err);
      setSaveState('idle');
    }
  };

  return (
    <div className="fixed left-3 top-[68px] bg-card border border-input rounded-lg p-1.5 flex flex-col gap-1 z-30 shadow-lg">
      <Btn
        title={saveState === 'done' ? '已保存' : '立即保存 (Ctrl+S)'}
        onClick={handleSave}
        disabled={saveState === 'saving'}
      >
        {saveState === 'saving' ? (
          <Loader2 size={16} className="animate-spin" />
        ) : saveState === 'done' ? (
          <Check size={16} className="text-emerald-400" />
        ) : (
          <Save size={16} />
        )}
      </Btn>
      <div className="h-px bg-accent my-0.5" />
      <Btn title="撤销 (Ctrl+Z)" onClick={undo}>
        <Undo2 size={16} />
      </Btn>
      <Btn title="重做 (Ctrl+Shift+Z)" onClick={redo}>
        <Redo2 size={16} />
      </Btn>
      <div className="h-px bg-accent my-0.5" />
      <Btn
        title="拷贝选中 (Ctrl+D)"
        onClick={() => duplicateNodes(Array.from(useCanvasStore.getState().selectedIds))}
      >
        <Copy size={16} />
      </Btn>
      <Btn
        title="删除选中 (Del)"
        onClick={() => deleteNodes(Array.from(useCanvasStore.getState().selectedIds))}
      >
        <Trash2 size={16} />
      </Btn>
      <div className="h-px bg-accent my-0.5" />
      <Btn title="放大" onClick={() => zoomAt(1.2)}>
        <ZoomIn size={16} />
      </Btn>
      <Btn title="缩小" onClick={() => zoomAt(1 / 1.2)}>
        <ZoomOut size={16} />
      </Btn>
      <div className="text-[10px] text-muted-foreground text-center select-none">
        {Math.round(view.zoom * 100)}%
      </div>

      {/* 超级管理员运维工具——对其他人不可见。把 canvas 编译成 Skill JSON
          （导出），或从粘贴的 JSON 重建 canvas（导入）。不涉及后端 API。 */}
      {isSuperAdmin && (
        <>
          <div className="h-px bg-accent my-0.5" />
          <Btn title="导出画布为 Skill JSON" onClick={() => setExportOpen(true)}>
            <Workflow size={16} />
          </Btn>
          <Btn title="从 Skill JSON 导入到画布" onClick={() => setImportOpen(true)}>
            <FileJson size={16} />
          </Btn>
          <WorkflowExportModal open={exportOpen} onClose={() => setExportOpen(false)} />
          <WorkflowImportModal open={importOpen} onClose={() => setImportOpen(false)} />
        </>
      )}
    </div>
  );
}

function Btn({
  title,
  onClick,
  children,
  disabled,
}: {
  title: string;
  onClick: () => void;
  children: React.ReactNode;
  disabled?: boolean;
}) {
  return (
    <button
      title={title}
      onClick={onClick}
      disabled={disabled}
      className="w-8 h-8 rounded flex items-center justify-center bg-transparent border-none text-sidebar-foreground hover:bg-popover hover:text-foreground transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
    >
      {children}
    </button>
  );
}
