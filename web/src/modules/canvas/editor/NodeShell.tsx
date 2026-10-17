import { ReactNode, useCallback, useEffect, useRef, useState } from 'react';
import { Tooltip } from 'antd';
import {
  Box,
  Camera,
  Image as ImageIcon,
  Music,
  Plus,
  Type,
  Video as VideoIcon,
} from 'lucide-react';
import { NodeContextMenu } from './nodes/NodeContextMenu';
import { standardNodeMenuItems } from './nodes/standardNodeMenuItems';
import { isInVideoControlsArea } from './nodes/videoControlsGuard';
import { useCanvasStore } from './store';
import { useStoryboardMode } from '../storyboard/StoryboardContext';
import {
  flushNodePatch,
  geometryPatchFromNode,
  getCanvasOwnerToken,
  persistNodePositions,
  scheduleNodePatch,
} from './persistence/canvasMutations';
import type { CanvasNode, NodeType } from './types';

// 从 URL 里取当前 project id，这样重命名能强制 flush snapshot。
// 没有这个的话，重命名后立刻刷新（在 autosave 的 10s 尾随 debounce 之内）
// 会丢掉新名字。
function currentProjectId(): number | null {
  if (typeof window === 'undefined') return null;
  const m = /\/canvas\/(\d+)/.exec(window.location.pathname);
  return m ? parseInt(m[1], 10) : null;
}

const DEFAULT_LABELS: Record<NodeType, string> = {
  'input-image': '图片',
  'input-audio': '音频',
  'video-input': '视频',
  'text-node': '文本',
  'gen-image': 'AI 绘图',
  'gen-video': 'AI 视频',
  'gen-audio': 'AI 音频',
  'image-compare': '对比',
  'local-save': '本地保存',
  preview: '预览',
  'subtitle-erase': '擦除字幕',
  'video-upscale': '视频转高清',
  'video-whitemodel': '视频转白模',
  script: '脚本',
  'director-desk': '3D导演台',
};

export function defaultLabelFor(node: CanvasNode): string {
  if (node.type === 'preview') {
    // preview 是通用媒体容器，kind 三种都真实存在（agent 会落 kind='audio'，
    // 见 agent/AgentPanel.tsx 的 kind 映射）。漏掉 audio 的话音频预览节点
    // 会顶着「图片」这个名字，在画布和故事板里都是错的。
    const kind = node.settings?.kind as string | undefined;
    if (kind === 'video') return '视频';
    if (kind === 'audio') return '音频';
    return '图片';
  }
  return DEFAULT_LABELS[node.type] ?? node.type;
}

/**
 * 节点在 UI 上实际显示的名字：用户自定义名优先，没有就退回类型默认标签。
 * 之前这句 `customName || defaultLabelFor(node)` 在 NodeShell / StoryboardCard /
 * StoryboardDetail 三处各写一遍，抽成一个函数保证三处永远一致。
 */
export function displayLabelFor(node: CanvasNode): string {
  const customName = (node.settings?.name as string | undefined)?.trim() ?? '';
  return customName || defaultLabelFor(node);
}

function iconFor(node: CanvasNode) {
  switch (node.type) {
    case 'input-image':
    case 'gen-image':
    case 'image-compare':
      return ImageIcon;
    case 'input-audio':
    case 'gen-audio':
      return Music;
    case 'video-input':
    case 'gen-video':
      return VideoIcon;
    case 'text-node':
      return Type;
    case 'director-desk':
      return Camera;
    case 'preview': {
      const kind = node.settings?.kind as string | undefined;
      if (kind === 'video') return VideoIcon;
      if (kind === 'audio') return Music;
      return ImageIcon;
    }
    default:
      return Box;
  }
}

interface NodeShellProps {
  node: CanvasNode;
  /** Input 端口数量，均匀分布。对没有输入的节点设为 0。 */
  inputs?: number;
  /** 右侧的 output 端口数量。preview 设为 0。 */
  outputs?: number;
  children: ReactNode;
  minWidth?: number;
  minHeight?: number;
  /**
   * "hidden"（默认）把 children 裁剪到节点的包围盒内。"visible" 让节点能在
   * 自己的边界之外渲染额外的绝对定位内容——被 GenImageNode 用来在预览方块
   * 下方浮一个更宽的 Prompt 面板（分栏布局）。选中环
   * 仍然只框住节点本身的边界；溢出的 children 自己带 border / shadow。
   */
  bodyOverflow?: 'hidden' | 'visible';
  /**
   * 挂在名字 chip 右侧的额外内容（状态角标之类）。放这里而不是节点内部，是因为
   * 卡片上方那一行本来就是节点的"抬头"，状态标记贴在这儿既不遮挡内容，也不会
   * 因为 bodyOverflow=visible 而糊到卡片外面去。
   */
  titleExtra?: ReactNode;
  /**
   * 应用到节点外层 wrapper 以及每个内部表面的圆角（px）。默认 12px
   *（匹配之前 `rounded-xl` 的观感）。AI 绘图传 16，用更大的卡片圆角。
   * 这里用 inline style 而非 Tailwind class，免得动态值被 JIT 丢掉。
   */
  radiusPx?: number;
  /**
   * 为 false 时不渲染右下角的 resize 把手，并禁用手动 resize。AI 绘图 用了
   * 这个——它的节点宽高比由所选输出比例驱动，而不是由用户拖动决定。
   */
  resizable?: boolean;
}

export function NodeShell({
  node,
  inputs = 1,
  outputs = 1,
  children,
  minWidth = 220,
  minHeight = 160,
  bodyOverflow = 'hidden',
  titleExtra,
  radiusPx = 12,
  resizable = true,
}: NodeShellProps) {
  // 故事板模式：同一个节点组件渲染在普通容器里而不是画布坐标系里。
  const storyboardMode = useStoryboardMode();
  const isSelected = useCanvasStore((s) => s.selectedIds.has(node.id));
  const selectNode = useCanvasStore((s) => s.selectNode);
  const deleteNodes = useCanvasStore((s) => s.deleteNodes);
  const moveNodes = useCanvasStore((s) => s.moveNodes);
  const resizeNode = useCanvasStore((s) => s.resizeNode);
  const commit = useCanvasStore((s) => s.commit);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);

  const displayLabel = displayLabelFor(node);
  const NodeIcon = iconFor(node);
  const [editingName, setEditingName] = useState(false);
  const [nameDraft, setNameDraft] = useState(displayLabel);
  const nameInputRef = useRef<HTMLInputElement | null>(null);
  // 壳级右键菜单：所有节点的兜底删除入口。媒体节点在内容区有自己的
  // useMediaNodeMenu（那份菜单也带「删除」且 stopPropagation，不会落到这里）；
  // 这份覆盖没有专属菜单的节点类型和媒体节点的非内容区域。
  const [shellMenuPos, setShellMenuPos] = useState<{ x: number; y: number } | null>(null);

  // 重命名时，只要在 input 之外按下指针就保存 + 退出。canvas 根节点会
  // 取消/捕获 mousedown 事件，所以点击空白 canvas 时 input.onBlur 经常
  // 不触发。这个监听器把那种情况兜住。
  useEffect(() => {
    if (!editingName) return;
    const handler = (ev: MouseEvent) => {
      const el = nameInputRef.current;
      if (!el) return;
      if (el.contains(ev.target as Node)) return;
      const name = el.value.trim();
      updateNodeSettings(node.id, { name });
      scheduleNodePatch(node.id, { settings: { name } });
      void flushNodePatch(node.id);
      setEditingName(false);
    };
    document.addEventListener('mousedown', handler, true);
    return () => document.removeEventListener('mousedown', handler, true);
  }, [editingName, node.id, updateNodeSettings]);
  const isConnectTarget = useCanvasStore(
    (s) => s.connecting != null && s.connecting.fromId !== node.id && s.connecting.hoverTargetId === node.id,
  );

  const dragRef = useRef<{ lastX: number; lastY: number; committed: boolean } | null>(null);
  const resizeRef = useRef<{
    startX: number; startY: number; startW: number; startH: number; committed: boolean;
  } | null>(null);

  // 拖拽/缩放挂在 window 上的那几个监听的摘除函数。卸载时必须摘干净：不摘的话
  // 组件没了、闭包还活着，之后每一次 mousemove 都在给一个不存在的节点算位移
  // （moveNodes 对已删节点是 no-op，但 mouseup 那条路会照常发一次 PATCH）。
  const windowListenersRef = useRef<(() => void) | null>(null);
  const detachWindowListeners = useCallback(() => {
    windowListenersRef.current?.();
    windowListenersRef.current = null;
  }, []);
  useEffect(() => () => {
    detachWindowListeners();
  }, [detachWindowListeners]);

  const onMouseDownCapture = useCallback((e: React.MouseEvent) => {
    // 故事板里没有画布坐标系——同下面 onMouseDown 那条早退一样的道理，选中/
    // 多选这类画布副作用在故事板详情面板里无处可去。这条捕获阶段的早退之前
    // 漏加：点详情面板里的 input/button/video 仍会悄悄调 selectNode 改画布
    // 选中态，Ctrl/Cmd+点还会切换画布多选。
    if (storyboardMode) return;
    if (e.button !== 0 || e.target === e.currentTarget) return;
    const interactive = (e.target as HTMLElement).closest(
      'input, textarea, select, button, a, video, audio, [contenteditable="true"], [data-video-controls]',
    );
    if (!interactive) return;
    // video 只有底部控制条算交互区，画面区域要走冒泡阶段的单节点拖动。
    // 整个 video 都按交互处理的话：标记让冒泡入口早退、又没人 stopPropagation，
    // 事件一路冒到画布根触发视图平移——按住视频画面拖，满屏节点一起动。
    // 画面区域仍做兜底选中（子视频可能自己吃掉冒泡，点击也得能选中），但
    // **不设标记**，冒泡入口照常启动拖动；ctrl 多选留给冒泡处理，这里再做
    // 会跟冒泡各 toggle 一次、相互抵消。
    if (
      interactive instanceof HTMLVideoElement &&
      !isInVideoControlsArea(interactive, e.clientY)
    ) {
      if (!e.ctrlKey && !e.metaKey && !isSelected) selectNode(node.id, false);
      return;
    }
    if ((e.nativeEvent as any).__tvmaoSelectionHandled) return;
    (e.nativeEvent as any).__tvmaoSelectionHandled = true;
    if (e.ctrlKey || e.metaKey) {
      selectNode(node.id, true);
    } else if (!isSelected) {
      selectNode(node.id, false);
    }
  }, [node.id, isSelected, selectNode, storyboardMode]);

  const onMouseDown = useCallback((e: React.MouseEvent) => {
    // 故事板里没有画布坐标系，拖动无处可去；而 moveNodes 会真的改 node.x/y
    // 并 PATCH 到后端——不早退的话，在故事板里划一下就把画布布局改了。
    if (storyboardMode) return;
    if ((e.nativeEvent as any).__tvmaoSelectionHandled) return;
    if (e.button !== 0) return;
    const target = e.target as HTMLElement;
    if (target.dataset.portType || target.closest('[data-port-type]')) return;
    // 与节点内的表单控件交互时不要开始拖动。
    if (target.closest('input, textarea, select, button, a, label, [contenteditable="true"]')) {
      e.stopPropagation();
      return;
    }
    e.stopPropagation();

    if (e.ctrlKey || e.metaKey) {
      selectNode(node.id, true);
    } else {
      const alreadySelected = isSelected && useCanvasStore.getState().selectedIds.size > 1;
      if (alreadySelected) {
        // 保持多选，只是确保当前节点是主选中项
      } else {
        selectNode(node.id, false);
      }
    }

    // 开始拖动
    dragRef.current = { lastX: e.clientX, lastY: e.clientY, committed: false };
    const handleMove = (ev: MouseEvent) => {
      const d = dragRef.current;
      if (!d) return;
      if (!d.committed) { commit(); d.committed = true; }
      const zoom = useCanvasStore.getState().view.zoom;
      const dx = (ev.clientX - d.lastX) / zoom;
      const dy = (ev.clientY - d.lastY) / zoom;
      d.lastX = ev.clientX;
      d.lastY = ev.clientY;
      const sel = useCanvasStore.getState().selectedIds;
      const targets = sel.has(node.id) ? Array.from(sel) : [node.id];
      moveNodes(targets, dx, dy);
    };
    const handleUp = () => {
      const committed = dragRef.current?.committed;
      dragRef.current = null;
      detachWindowListeners();
      // 拖动过程中只改本地，松手后一次性 PATCH 受影响节点的坐标。没真正
      // 移动（committed=false，只是点选）就不发请求。
      if (!committed) return;
      const pid = currentProjectId();
      const token = getCanvasOwnerToken();
      if (!pid || !token) return;
      const state = useCanvasStore.getState();
      const targets = state.selectedIds.has(node.id) ? Array.from(state.selectedIds) : [node.id];
      const patches = targets
        .map((id) => state.nodes.find((n) => n.id === id))
        .filter((n): n is CanvasNode => Boolean(n))
        .map((n) => geometryPatchFromNode(n));
      persistNodePositions(pid, token, patches);
    };
    // 第三条退出路径：window blur。切标签页/切应用时浏览器**不会**补一个
    // mouseup——没有它，切回来一动鼠标节点就接着跟手飘。按 mouseup 同样处理并
    // **照常落库**：用户切窗前看到的就是这个位置，丢掉等于回滚了他刚做的移动。
    // 先摘掉上一轮可能残留的监听，再挂本轮的（同一时刻只该有一组）。
    detachWindowListeners();
    window.addEventListener('mousemove', handleMove);
    window.addEventListener('mouseup', handleUp);
    window.addEventListener('blur', handleUp);
    windowListenersRef.current = () => {
      window.removeEventListener('mousemove', handleMove);
      window.removeEventListener('mouseup', handleUp);
      window.removeEventListener('blur', handleUp);
    };
  }, [node.id, isSelected, selectNode, commit, moveNodes, storyboardMode, detachWindowListeners]);

  const onResizeMouseDown = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    e.preventDefault();
    resizeRef.current = {
      startX: e.clientX, startY: e.clientY,
      startW: node.width, startH: node.height,
      committed: false,
    };
    const handleMove = (ev: MouseEvent) => {
      const r = resizeRef.current;
      if (!r) return;
      if (!r.committed) { commit(); r.committed = true; }
      const zoom = useCanvasStore.getState().view.zoom;
      const dx = (ev.clientX - r.startX) / zoom;
      const dy = (ev.clientY - r.startY) / zoom;
      resizeNode(node.id, Math.max(minWidth, r.startW + dx), Math.max(minHeight, r.startH + dy));
    };
    const handleUp = () => {
      const committed = resizeRef.current?.committed;
      resizeRef.current = null;
      detachWindowListeners();
      // 缩放过程中只改本地，结束后 PATCH 节点尺寸。
      if (!committed) return;
      const pid = currentProjectId();
      const token = getCanvasOwnerToken();
      if (!pid || !token) return;
      const current = useCanvasStore.getState().nodes.find((n) => n.id === node.id);
      // 同拖拽走 persistNodePositions：成功后顺带推进基线。
      if (current) persistNodePositions(pid, token, [geometryPatchFromNode(current)]);
    };
    // 先摘掉上一轮可能残留的监听，再挂本轮的（同一时刻只该有一组）。
    detachWindowListeners();
    window.addEventListener('mousemove', handleMove);
    window.addEventListener('mouseup', handleUp);
    window.addEventListener('blur', handleUp);
    windowListenersRef.current = () => {
      window.removeEventListener('mousemove', handleMove);
      window.removeEventListener('mouseup', handleUp);
      window.removeEventListener('blur', handleUp);
    };
  }, [node.id, node.width, node.height, minWidth, minHeight, commit, resizeNode, detachWindowListeners]);

  const inputPorts: Array<{ pos: string }> = (() => {
    const n = inputs ?? 1;
    if (n <= 0) return [];
    const positions = n === 1 ? ['50%'] : n === 2 ? ['33%', '66%'] : Array.from({ length: n }, (_, i) => `${((i + 1) * 100) / (n + 1)}%`);
    return positions.map((pos) => ({ pos }));
  })();

  return (
    <div
      data-node-id={node.id}
      className={`${storyboardMode ? 'relative' : 'absolute'} shadow-xl transition-shadow duration-150 group flex flex-col node-wrapper ${
        isSelected ? 'ring-1 ring-primary shadow-primary/20' : 'border border-border shadow-black/40'
      } ${
        isConnectTarget ? 'connect-target' : ''
      } bg-card`}
      style={{
        // 故事板模式下节点铺满外层容器（详情面板给多大就多大），画布模式下
        // 按自己的坐标和尺寸绝对定位。
        ...(storyboardMode
          ? { width: '100%', height: '100%' }
          : { left: node.x, top: node.y, width: node.width, height: node.height, zIndex: isSelected ? 30 : 1 }),
        borderRadius: radiusPx,
        cursor: dragRef.current ? 'grabbing' : 'default',
        WebkitFontSmoothing: 'antialiased',
        MozOsxFontSmoothing: 'grayscale',
        textRendering: 'optimizeLegibility',
        transform: 'translateZ(0)',
        backfaceVisibility: 'hidden',
      }}
      onMouseDownCapture={onMouseDownCapture}
      onMouseDown={onMouseDown}
      onContextMenu={(e) => {
        e.stopPropagation();
        // 输入类元素放行浏览器原生菜单——prompt 编辑区等地方用户要复制/粘贴，
        // 拦掉原生菜单只给一个「删除」是净伤害。
        const target = e.target as HTMLElement;
        if (target.closest('input, textarea, [contenteditable="true"]')) return;
        e.preventDefault();
        setShellMenuPos({ x: e.clientX, y: e.clientY });
      }}
    >
      {shellMenuPos && (
        <NodeContextMenu
          x={shellMenuPos.x}
          y={shellMenuPos.y}
          items={[
            // 复制节点/创建副本是所有节点右键菜单的标配（standardNodeMenuItems）——
            // 兜底菜单也要有，否则没有自定义菜单的节点类型就享受不到。
            ...standardNodeMenuItems(node.id, () => setShellMenuPos(null)),
            // 与 useMediaNodeMenu 的约定一致：删除是菜单最后一项。原先节点右上角
            // 的悬浮 X 因误点风险移除，右键菜单是唯一的鼠标删除入口。
            {
              key: 'delete',
              label: '删除',
              danger: true,
              onPick: () => {
                setShellMenuPos(null);
                deleteNodes([node.id]);
              },
              hint: '删除本节点，可撤销（Ctrl/Cmd+Z）',
            },
          ]}
          onClose={() => setShellMenuPos(null)}
        />
      )}

      {/* 名字 chip——就在卡片正上方。点击可就地重命名。
          把用户选的名字存到 node.settings.name；未设置时回退到
          基于类型的默认 label。 */}
      <div
        className="absolute -top-6 left-1 flex items-center gap-1 text-[11px] text-sidebar-foreground select-none z-20"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <NodeIcon size={12} className="text-muted-foreground shrink-0" />
        {editingName ? (
          <input
            ref={nameInputRef}
            autoFocus
            autoComplete="off"
            autoCorrect="off"
            spellCheck={false}
            value={nameDraft}
            onChange={(e) => setNameDraft(e.target.value)}
            onFocus={(e) => e.target.select()}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
                e.preventDefault();
                const v = nameDraft.trim();
                updateNodeSettings(node.id, { name: v });
                scheduleNodePatch(node.id, { settings: { name: v } });
                void flushNodePatch(node.id);
                setEditingName(false);
              }
              if (e.key === 'Escape') {
                setNameDraft(displayLabel);
                setEditingName(false);
              }
            }}
            onBlur={() => {
              const v = nameDraft.trim();
              updateNodeSettings(node.id, { name: v });
              scheduleNodePatch(node.id, { settings: { name: v } });
              void flushNodePatch(node.id);
              setEditingName(false);
            }}
            onClick={(e) => e.stopPropagation()}
            className="bg-card border border-input rounded px-1.5 py-0.5 text-[11px] text-foreground outline-none w-44 leading-tight"
          />
        ) : (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              // 用 chip 当前显示的内容（自定义名字或类型默认值）预填 draft，
              // 让用户能就地编辑，而不是看到一个空白 input。
              setNameDraft(displayLabel);
              setEditingName(true);
            }}
            title="点击重命名"
            className="bg-transparent border-none p-0 cursor-text hover:text-foreground transition-colors truncate max-w-[200px]"
          >
            {displayLabel}
          </button>
        )}
        {titleExtra}
      </div>

      {resizable && !storyboardMode && (
        <div
          className={`absolute bottom-1 right-1 w-4 h-4 z-[100] resize-handle flex items-end justify-end p-0.5 transition-opacity ${
            isSelected ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'
          }`}
          onMouseDown={onResizeMouseDown}
        >
          <svg width="6" height="6" viewBox="0 0 8 8" fill="none" className="text-muted-foreground">
            <path d="M8 0L8 8L0 8" stroke="currentColor" strokeWidth="2" />
          </svg>
        </div>
      )}

      <div
        className={`${bodyOverflow === 'visible' ? 'overflow-visible' : 'overflow-hidden'} flex-1 flex flex-col pointer-events-none h-full w-full relative bg-card`}
        style={{ borderRadius: radiusPx }}
      >
        <div
          className="relative w-full h-full flex flex-col transition-colors pointer-events-auto bg-card/80"
          style={{ borderRadius: radiusPx }}
          onWheel={(e) => e.stopPropagation()}
        >
          {children}
        </div>
      </div>

      {!storyboardMode && inputPorts.map((p, i) => (
        <div
          key={`in-${i}`}
          data-port-node={node.id}
          data-port-type="input"
          className="input-point"
          style={{ top: p.pos }}
          title="输入"
        />
      ))}

      {!storyboardMode && outputs > 0 && (
        <div
          data-port-node={node.id}
          data-port-type="output"
          className="connector connector-right"
          title="输出"
        >
          <Plus size={10} />
        </div>
      )}

      {/* 错误提示：单行条带钉在最顶部，免得和底部的操作栏打架。
          完整信息用 AntD Tooltip 即显（mouseEnterDelay=0）——原生 title 要悬停 ~1s
          才出现，用户以为没有提示。 */}
      {node.error && (
        <Tooltip title={node.error} mouseEnterDelay={0} placement="top">
          <div
            className="absolute left-0 right-0 top-0 bg-red-950/85 border-b border-red-500/50 text-red-200 text-[10px] leading-4 px-2 py-0.5 z-20 cursor-help"
            style={{
              whiteSpace: 'nowrap',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              borderTopLeftRadius: radiusPx,
              borderTopRightRadius: radiusPx,
            }}
          >
            ⚠ {node.error}
          </div>
        </Tooltip>
      )}
    </div>
  );
}
