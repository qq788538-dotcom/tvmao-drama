import { useEffect, useMemo, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { useCanvasStore } from '../../store';
import { useScriptSettings } from './useScriptSettings';
import { parseTokens, LABEL_BY_KIND } from './scriptTokens';
import { ScriptPromptEditor, type ScriptAssetOption } from './ScriptPromptEditor';

// 分镜提示词大编辑器：row.prompt 的唯一编辑入口（skill 语义层）。
// 编辑体验同 AI 生视频节点的 @ 方式：token 渲染为「缩略图+资产名」chip
// （ScriptPromptEditor，复制适配自 PromptMentionEditor），输入 @ 弹资产菜单。
// 关闭路径（遮罩 mousedown/ESC/X/卸载）统一 flush 草稿再关，防丢稿根因与修法
// 见 cellEditors.tsx 的 PopoverShell 注释。
export function PromptEditorDialog({
  nodeId,
  rowId,
  onClose,
  readOnly = false,
}: {
  nodeId: string;
  rowId: string;
  onClose: () => void;
  readOnly?: boolean;
}) {
  const { settings, updateRow } = useScriptSettings(nodeId);
  const nodes = useCanvasStore((s) => s.nodes);
  const row = settings.rows.find((r) => r.id === rowId);
  const [draft, setDraft] = useState(row?.prompt ?? '');

  useEffect(() => {
    if (row) setDraft(row.prompt);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [row?.prompt]);

  const draftRef = useRef(draft);
  draftRef.current = draft;
  const rowRef = useRef(row);
  rowRef.current = row;
  const savedRef = useRef(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const flush = () => {
    if (savedRef.current) return;
    savedRef.current = true;
    const cur = rowRef.current;
    if (!cur) return;
    if (draftRef.current !== cur.prompt) updateRow(rowId, { prompt: draftRef.current });
  };
  const closeWith = () => {
    flush();
    onClose();
  };
  const closeWithRef = useRef(closeWith);
  closeWithRef.current = closeWith;

  useEffect(() => {
    const onMouseDown = (e: MouseEvent) => {
      // @ 菜单 portal 在 body 上，不在 container 内——点菜单不能算「点外部」。
      const t = e.target as HTMLElement;
      if (t.closest?.('.cv-mention-menu') || t.closest?.('.cv-mention-menu-mask')) return;
      if (!containerRef.current?.contains(t)) closeWithRef.current();
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeWithRef.current();
    };
    document.addEventListener('mousedown', onMouseDown, true);
    window.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onMouseDown, true);
      window.removeEventListener('keydown', onKeyDown);
    };
  }, []);
  useEffect(() => () => flush(), []);

  // 资产选项：三类拉平，url = linkedNode 的图（节点可能已删，现查存活）。
  const options = useMemo<ScriptAssetOption[]>(() => {
    const nodeUrl = (linkedNodeId: string) =>
      (nodes.find((n) => n.id === linkedNodeId)?.content as string | undefined) || undefined;
    return [
      ...settings.assets.characters.map((a) => ({ kind: 'character' as const, name: a.name, url: nodeUrl(a.linkedNodeId) })),
      ...settings.assets.scenes.map((a) => ({ kind: 'scene' as const, name: a.name, url: nodeUrl(a.linkedNodeId) })),
      ...settings.assets.props.map((a) => ({ kind: 'prop' as const, name: a.name, url: nodeUrl(a.linkedNodeId) })),
    ];
  }, [settings.assets, nodes]);

  const refs = useMemo(() => (row ? parseTokens(draft, settings.assets) : []), [draft, row, settings.assets]);
  const brokenRefs = useMemo(() => {
    const seen = new Set<string>();
    return refs.filter((r) => {
      if (r.asset) return false;
      const k = `${r.kind}:${r.name}`;
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
  }, [refs]);

  if (!row) return null;

  return (
    <div
      className="script-borders fixed inset-0 z-40 flex items-center justify-center bg-black/50"
      onMouseDown={(e) => e.stopPropagation()}
    >
      <div
        ref={containerRef}
        data-testid="prompt-editor-dialog"
        className="flex h-[calc(100vh-120px)] w-[min(1100px,94vw)] flex-col overflow-hidden rounded-2xl border border-white/10 bg-[#171717] shadow-2xl"
      >
        <div className="flex items-center justify-between border-b border-white/10 px-5 py-4">
          <h2 className="text-base font-medium text-foreground">第 {row.shotNumber} 镜：提示词</h2>
          <button
            type="button"
            onClick={closeWith}
            aria-label="关闭"
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground bg-transparent border-none cursor-pointer"
          >
            <X size={18} />
          </button>
        </div>

        <div className="flex min-h-0 flex-1 flex-col gap-3 px-5 py-4">
          <ScriptPromptEditor
            value={draft}
            options={options}
            readOnly={readOnly}
            placeholder={'镜头1：中景，平视，固定机位。画面中 @资产 位于……。动作与结果。台词、音效。输入 @ 引用步骤2资产。'}
            onChange={setDraft}
            onBlur={() => {
              if (rowRef.current && draft !== rowRef.current.prompt) updateRow(rowId, { prompt: draft });
            }}
            className="h-full min-h-[300px] w-full rounded-xl border border-white/10 bg-black/40 p-4 text-sm leading-6 text-foreground"
          />

          {brokenRefs.length > 0 && (
            <div className="flex flex-wrap items-center gap-2">
              {brokenRefs.map((r) => (
                <span
                  key={`${r.kind}:${r.name}`}
                  className="rounded bg-red-500/15 px-2 py-1 text-xs text-red-400"
                  title="资产表里找不到该名称——检查步骤 2 或改 token"
                >
                  {LABEL_BY_KIND[r.kind]}-{r.name}（断链）
                </span>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
