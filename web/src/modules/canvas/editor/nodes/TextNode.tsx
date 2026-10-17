// 文本节点——LibTV 式四态交互：
//   菜单态（新建空节点，「尝试: 自己编写内容」）→ 编辑态（Tiptap + 浮动工具栏）
//   → 失焦后按内容分流：有内容 = 只读 markdown 渲染态；无内容 = 「请编写内容」占位态。
// 存储始终是 settings.text 里的 markdown 字符串（纯文本是合法 markdown，存量零迁移）。
import { useEffect, useMemo, useRef, useState } from 'react';
import { App as AntApp } from 'antd';
import { FileText, Loader2 } from 'lucide-react';
import { EditorContent } from '@tiptap/react';
import { useCanvasStore } from '../store';
import { NodeShell } from '../NodeShell';
import { persistNodeById } from '../persistence/canvasMutations';
import { pollCanvasTaskUntilDone } from '@/shared/api';
import { readMirror } from '../utils/nodeClipboard';
import { MAX_TEXT_LEN } from './text/useMarkdownEditor';
import { useReconcileCanvasTask } from './useReconcileCanvasTask';
import { NodeContextMenu, type NodeMenuItem } from './NodeContextMenu';
import { isMacLike, standardNodeMenuItems } from './standardNodeMenuItems';
import { useMarkdownEditor } from './text/useMarkdownEditor';
import { TextToolbar } from './text/TextToolbar';
import { TextExpandDialog } from './text/TextExpandDialog';
import { TextGenPanel } from './text/TextGenPanel';
import { CharCount } from './text/CharCount';
import { commitTextDraft, flushTextDraft } from './text/textNodeDraft';
import { renderTextMarkdown } from './text/renderTextMarkdown';
import { interceptMarkdownLink } from '../utils/markdownLinks';

// LibTV 空态那组「横线」示意图形。用 div 画而不是找图标：lucide 没有对应形状。
function LinesGlyph() {
  return (
    <div className="flex flex-col items-center gap-1.5" aria-hidden>
      {[44, 44, 44, 28].map((w, i) => (
        <div key={i} className="h-1.5 rounded-sm bg-muted-foreground/30" style={{ width: w }} />
      ))}
    </div>
  );
}

// 内联编辑器单独成组件：进入编辑态才挂载 Tiptap（菜单/只读态不付初始化成本），
// 退出即卸载，重进时从 store 取最新 markdown 重建——编辑器因此可以不受控。
function InlineEditor({
  nodeId,
  initialMarkdown,
  onExit,
  onExpand,
}: {
  nodeId: string;
  initialMarkdown: string;
  onExit: () => void;
  onExpand: () => void;
}) {
  const editor = useMarkdownEditor({
    value: initialMarkdown,
    onChange: (md) => commitTextDraft(nodeId, md),
    // 失焦 = 退出编辑态并立即冲刷（不等 debounce 到点）。工具栏按钮在 mousedown
    // 上 preventDefault 抢回了焦点，不会误触这里。
    onBlur: () => {
      void flushTextDraft(nodeId);
      onExit();
    },
    autoFocus: true,
  });

  return (
    <>
      {/* 浮动工具栏：借 NodeShell body 的 overflow:visible 溢出到节点上方，
          水平居中；scale(1/zoom) 抵消画布缩放让它在屏幕上恒定大小（同
          GenImageNode 浮动 prompt 面板的定位方案，anchor 换成节点顶边——
          transformOrigin 取底边中线，缩放时 12px 间隙按屏幕像素恒定）。 */}
      <div
        className="absolute z-30"
        style={{
          bottom: '100%',
          left: '50%',
          transform: 'scale(var(--inv-zoom, 1)) translate(-50%, -12px)',
          transformOrigin: '0 100%',
        }}
        onMouseDown={(e) => e.stopPropagation()}
      >
        {editor && <TextToolbar editor={editor} onExpand={onExpand} />}
      </div>
      <div
        className="flex-1 min-h-0 overflow-y-auto custom-scrollbar px-4 py-3 cursor-text"
        onMouseDown={(e) => e.stopPropagation()}
        // 点内容区空白处也把光标送进编辑器，别让用户以为「点了没反应」。
        onClick={(e) => {
          if (e.target === e.currentTarget) editor?.chain().focus('end').run();
        }}
      >
        <EditorContent editor={editor} />
      </div>
      <div className="px-3 pb-2 text-right shrink-0">
        {editor && <CharCount editor={editor} limit={MAX_TEXT_LEN} />}
      </div>
    </>
  );
}

export function TextNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodes.find((n) => n.id === id));
  const readOnly = useCanvasStore((s) => s.readOnly);
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const isSelected = useCanvasStore((s) => s.selectedIds.has(id));
  const deleteNodes = useCanvasStore((s) => s.deleteNodes);
  const { message } = AntApp.useApp();
  const [menuPos, setMenuPos] = useState<{ x: number; y: number } | null>(null);
  // 兜底对账:节点卡在 generating 且丢 taskId 时按 nodeId 找回任务号(useReconcileCanvasTask 顶部注释)。
  useReconcileCanvasTask(id);
  const [editing, setEditing] = useState(false);
  // 「进入过编辑又清空」与「从未编辑」的空节点长得不一样（LibTV 同款）：
  // 前者显示「请编写内容」占位，后者显示「尝试:」菜单。只是会话内状态，
  // 刷新后空节点回到菜单态——与 LibTV 行为一致，不值得为此加持久化字段。
  const [touched, setTouched] = useState(false);
  const [expandOpen, setExpandOpen] = useState(false);

  // AI 生成的轮询：只要节点持有进行中的任务就恢复/驱动（刷新页面也能续上）。
  // 轮询只为即时展示——settings.text 由后端 sink 权威写回快照，关页面不丢。
  useEffect(() => {
    if (node?.status === 'generating' && node.taskId) {
      const ctrl = new AbortController();
      let cancelled = false;
      pollCanvasTaskUntilDone(node.taskId, { signal: ctrl.signal })
        .then((st) => {
          if (cancelled) return;
          if (st.status === 'succeeded' && st.text) {
            updateNodeSettings(id, { text: st.text });
            updateNode(id, { status: 'succeeded', error: undefined, taskId: undefined });
          } else if (st.status === 'succeeded') {
            // 配错模型（生成出媒体而非文本）——succeeded 但无 text，如实报错。
            updateNode(id, { status: 'failed', error: '上游未返回文本', taskId: undefined });
          } else {
            updateNode(id, { status: 'failed', error: st.error || '生成失败', taskId: undefined });
          }
          // 结果已由后端权威写回；这次持久化只为同步终态 status/taskId。
          persistNodeById(id);
        })
        .catch((err: unknown) => {
          const e = err as { message?: string };
          if (cancelled || e?.message === 'aborted') return;
          updateNode(id, { status: 'failed', error: e?.message || '轮询失败', taskId: undefined });
          persistNodeById(id);
        });
      return () => {
        cancelled = true;
        ctrl.abort();
      };
    }
    return undefined;
  }, [node?.status, node?.taskId, id, updateNode, updateNodeSettings]);

  const text = (node?.settings?.text as string | undefined) ?? '';
  // marked + DOMPurify 对长文本不便宜；不缓存的话拖动/选中本节点每一帧都整篇重解析。
  const renderedHtml = useMemo(() => renderTextMarkdown(text), [text]);
  // 「点一下进编辑」和「按住拖动节点」共用同一片区域。mousedown 不能
  // stopPropagation——那会吞掉 NodeShell 的选中+拖拽（曾导致节点拖不动、
  // Delete 删不了、生成面板因 isSelected 永远起不来而点不到）。这里只记录
  // 按下坐标，click 时超过阈值判定为拖动，不进编辑。（hook 必须在提前 return 之前。）
  const pressRef = useRef<{ x: number; y: number } | null>(null);

  if (!node) return null;
  const hasContent = text.trim().length > 0;

  const startEdit = () => {
    if (readOnly) return;
    setTouched(true);
    setEditing(true);
  };

  const trackPress = (e: React.MouseEvent) => {
    pressRef.current = { x: e.clientX, y: e.clientY };
  };
  const clickWasDrag = (e: React.MouseEvent) => {
    const p = pressRef.current;
    pressRef.current = null;
    return p ? Math.hypot(e.clientX - p.x, e.clientY - p.y) > 5 : false;
  };

  // 展开弹层打开时内联编辑器让位（两个 Tiptap 实例同时编辑同一字段会互相覆盖），
  // 节点本体退回只读渲染，内容经 store 实时同步。
  const inlineEditing = editing && !expandOpen && !readOnly;

  const isGenerating = node.status === 'generating';

  // 文本节点专属右键菜单（对标 LibTV，去掉媒体专属的保存资产/创建主体/复制到
  // 剪贴板三项）：标配两项 + 复制文本 + 粘贴 + 删除。
  const mac = isMacLike();
  const menuItems: NodeMenuItem[] = [
    ...standardNodeMenuItems(id, () => setMenuPos(null)),
    {
      key: 'copy-text',
      label: '复制文本',
      disabled: !hasContent,
      hint: hasContent ? '复制节点内的文本原文' : '还没有文本内容',
      onPick: () => {
        setMenuPos(null);
        void navigator.clipboard.writeText(text).then(() => message.success('已复制文本'));
      },
    },
    {
      key: 'paste',
      label: '粘贴',
      shortcut: mac ? '⌘V' : 'Ctrl+V',
      // 节点菜单的粘贴只接画布剪贴板的节点 payload；图片/文字粘贴走画布空白
      // 菜单与 ⌘V 全局路径，这里不重写那套异步剪贴板读取。
      disabled: !readMirror(),
      hint: readMirror() ? '把已复制的节点粘贴到本节点旁边' : '画布剪贴板里没有可粘贴的节点',
      onPick: () => {
        setMenuPos(null);
        const payload = readMirror();
        if (!payload) return;
        useCanvasStore.getState().pasteNodes(payload.nodes, payload.connections, {
          x: node.x + node.width / 2 + 60,
          y: node.y + node.height / 2 + 60,
        });
      },
    },
    // 删除固定放最后一项——与 useMediaNodeMenu / NodeShell 兜底菜单的约定一致。
    {
      key: 'delete',
      label: '删除',
      danger: true,
      shortcut: mac ? '⌫' : 'Del',
      onPick: () => {
        setMenuPos(null);
        deleteNodes([id]);
      },
      hint: '删除本节点，可撤销（Ctrl/Cmd+Z）',
    },
  ];

  return (
    <NodeShell node={node} minWidth={220} minHeight={160} bodyOverflow="visible">
      <div
        className="flex-1 min-h-0 flex flex-col relative"
        onContextMenu={(e) => {
          // 输入类元素放行浏览器原生菜单（复制/粘贴文字要用）——与 NodeShell
          // 壳级菜单同一条规则；其余区域接管为节点菜单。
          const target = e.target as HTMLElement;
          if (target.closest('input, textarea, [contenteditable="true"]')) return;
          e.preventDefault();
          e.stopPropagation();
          setMenuPos({ x: e.clientX, y: e.clientY });
        }}
      >
        {isGenerating ? (
          // 生成中：内容区整体让位给状态提示。不允许同时手动编辑——后端 sink
          // 随时会权威覆盖 settings.text，编辑到一半被覆盖比禁用编辑更伤。
          <div data-testid="text-node-generating" className="flex-1 flex flex-col items-center justify-center gap-3">
            <Loader2 size={18} className="animate-spin text-muted-foreground" />
            <div className="text-xs text-muted-foreground">AI 生成中…</div>
          </div>
        ) : inlineEditing ? (
          <InlineEditor
            nodeId={id}
            initialMarkdown={text}
            onExit={() => setEditing(false)}
            onExpand={() => setExpandOpen(true)}
          />
        ) : hasContent ? (
          <div
            data-testid="text-node-view"
            className={`markdown-body flex-1 min-h-0 overflow-y-auto custom-scrollbar px-4 py-3 text-sm ${readOnly ? '' : 'cursor-text'}`}
            onMouseDown={trackPress}
            onClick={(e) => {
              // 链接只拦默认导航（否则整个应用窗口被带走），点击语义仍是进编辑。
              interceptMarkdownLink(e);
              if (!clickWasDrag(e)) startEdit();
            }}
            // renderTextMarkdown 里已过 DOMPurify，见该函数注释。
            dangerouslySetInnerHTML={{ __html: renderedHtml }}
          />
        ) : touched && !readOnly ? (
          <div
            data-testid="text-node-empty"
            className="flex-1 flex flex-col items-center justify-center gap-5 cursor-text"
            onMouseDown={trackPress}
            onClick={(e) => {
              if (!clickWasDrag(e)) startEdit();
            }}
          >
            <div className="text-sm text-muted-foreground">请编写内容，开始你的创作。</div>
            <LinesGlyph />
          </div>
        ) : (
          // 菜单态容器不拦 mousedown：让 NodeShell 正常选中/拖拽，
          // 「自己编写内容」是 <button>，NodeShell 自己会把它排除在拖拽外。
          <div
            data-testid="text-node-menu"
            className="flex-1 flex flex-col items-center justify-center gap-6 px-5 py-4"
          >
            <LinesGlyph />
            {!readOnly && (
              <div className="w-full">
                <div className="text-[11px] text-muted-foreground/80 mb-1.5">尝试:</div>
                <button
                  type="button"
                  onClick={startEdit}
                  className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md border-none bg-transparent text-left text-xs text-foreground hover:bg-accent/60 cursor-pointer transition-colors"
                >
                  <FileText size={13} className="text-muted-foreground shrink-0" />
                  自己编写内容
                </button>
              </div>
            )}
          </div>
        )}
      </div>
      {/* AI 生成输入条：选中节点时浮在下方（同 GenImageNode 浮动 prompt 面板的
          定位方案：借 bodyOverflow=visible 溢出 + --inv-zoom 反缩放恒定屏幕尺寸）。
          编辑态收起（LibTV 同款：编辑时只留上方工具栏）——手写和 AI 生成是互斥
          入口，同时摆着既挡视线，生成回来还会覆盖正在手打的内容。 */}
      {!readOnly && !editing && !expandOpen && (
        <div
          className="absolute z-20"
          style={{
            top: '100%',
            left: '50%',
            transform: 'scale(var(--inv-zoom, 1)) translate(-50%, 12px)',
            transformOrigin: '0 0',
            width: 560,
            pointerEvents: isSelected ? 'auto' : 'none',
          }}
          onMouseDown={(e) => e.stopPropagation()}
        >
          <div
            style={{
              transform: `translateY(${isSelected ? '0' : '-6px'})`,
              opacity: isSelected ? 1 : 0,
              transition: 'opacity 180ms ease-out, transform 180ms ease-out',
            }}
          >
            <TextGenPanel nodeId={id} />
          </div>
        </div>
      )}
      {menuPos && (
        <NodeContextMenu x={menuPos.x} y={menuPos.y} items={menuItems} onClose={() => setMenuPos(null)} />
      )}
      {expandOpen && !readOnly && (
        <TextExpandDialog nodeId={id} initialMarkdown={text} onClose={() => setExpandOpen(false)} />
      )}
    </NodeShell>
  );
}
