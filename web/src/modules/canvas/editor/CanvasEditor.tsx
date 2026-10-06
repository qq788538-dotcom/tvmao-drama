import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { App as AntApp } from 'antd';
import { DEFAULT_SIZES, useCanvasStore } from './store';
import { nodeComponents } from './registry';
import { ConnectionLayer } from './ConnectionLayer';
import { ContextMenu } from './ContextMenu';
import { CanvasContextMenu } from './CanvasContextMenu';
import { GenerationHistoryPickerModal } from './GenerationHistoryPickerModal';
import { applyLibraryItemToCanvas, currentProjectId, importAssetRefs } from '@/shared/api';
import { persistNodeById } from './persistence/canvasMutations';
import { saveLocalView } from './persistence/localView';
import type { NodeType, ViewState } from './types';
import {
  classifyWheel,
  fitView,
  panView,
  wheelZoomFactor,
  zoomViewAt,
} from './utils/viewGestures';
import { isPhysicalCtrlHeld } from '@/shared/gestureGuard';
import { layoutDropPoints, partitionDroppedFiles } from './utils/fileDrop';
import { uploadFileIntoNode } from './utils/uploadIntoNode';
import {
  NODE_CLIPBOARD_MIME,
  buildPayload,
  collectMediaKeys,
  payloadFromClipboardEvent,
  pendingCopyOverride,
  writeMirror,
  type NodeClipboardPayload,
} from './utils/nodeClipboard';
import { renderRejected } from './utils/rejectedFiles';

const LIBRARY_DRAG_MIME = 'application/x-tvmao-library-item-id';

function nodeTypeForKind(kind: 'image' | 'video' | 'audio'): NodeType {
  if (kind === 'image') return 'input-image';
  if (kind === 'video') return 'video-input';
  return 'input-audio';
}

interface ContextMenuState {
  screenX: number;
  screenY: number;
  worldX: number;
  worldY: number;
  /** 设置后，选中的节点会自动从这个 source 节点连过来 */
  sourceNodeId?: string;
  /** actions=右键动作菜单（上传/添加节点/撤销/重做/粘贴，对标 libtv）；
   *  add-node=添加节点菜单（双击背景、拖连线松手、右键菜单点「添加节点」）。 */
  kind: 'actions' | 'add-node';
}

interface ConnectingDragState {
  fromId: string;
  screenX: number;
  screenY: number;
}

interface CanvasEditorProps {
  projectId?: number;
}

export function CanvasEditor({ projectId }: CanvasEditorProps = {}) {
  const nodes = useCanvasStore((s) => s.nodes);
  const view = useCanvasStore((s) => s.view);
  const setView = useCanvasStore((s) => s.setView);
  const clearSelection = useCanvasStore((s) => s.clearSelection);
  const selectMany = useCanvasStore((s) => s.selectMany);
  const deleteNodes = useCanvasStore((s) => s.deleteNodes);
  const duplicateNodes = useCanvasStore((s) => s.duplicateNodes);
  const undo = useCanvasStore((s) => s.undo);
  const redo = useCanvasStore((s) => s.redo);
  const addConnection = useCanvasStore((s) => s.addConnection);
  const addNode = useCanvasStore((s) => s.addNode);
  const updateNode = useCanvasStore((s) => s.updateNode);
  const setStoreConnecting = useCanvasStore((s) => s.setConnecting);
  const setConnectingHoverTarget = useCanvasStore((s) => s.setConnectingHoverTarget);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const selectedIds = useCanvasStore((s) => s.selectedIds);

  const readOnly = useCanvasStore((s) => s.readOnly);
  const { modal, message } = AntApp.useApp();

  const viewportRef = useRef<HTMLDivElement>(null);
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);
  // 「从生成历史选择」弹框——菜单点击即销毁,弹框必须由编辑器托管才能活过菜单。
  const [historyPicker, setHistoryPicker] = useState<{
    worldX: number;
    worldY: number;
    sourceNodeId?: string;
  } | null>(null);
  const [marquee, setMarquee] = useState<{ x1: number; y1: number; x2: number; y2: number } | null>(null);
  const [connecting, setConnecting] = useState<ConnectingDragState | null>(null);
  // 外部文件悬停时的高亮。用计数而不是布尔:光标在子元素之间移动会连续触发
  // dragleave→dragenter,单纯翻布尔会让高亮疯狂闪烁。
  const [fileHover, setFileHover] = useState(false);
  const dragDepthRef = useRef(0);

  const screenToWorld = useCallback(
    (screenX: number, screenY: number) => {
      const rect = viewportRef.current?.getBoundingClientRect();
      if (!rect) return { x: 0, y: 0 };
      return {
        x: (screenX - rect.left - view.x) / view.zoom,
        y: (screenY - rect.top - view.y) / view.zoom,
      };
    },
    [view],
  );

  const openContextMenuAt = useCallback(
    (clientX: number, clientY: number, sourceNodeId?: string, kind: 'actions' | 'add-node' = 'add-node') => {
      const world = screenToWorld(clientX, clientY);
      setContextMenu({
        screenX: clientX,
        screenY: clientY,
        worldX: world.x,
        worldY: world.y,
        sourceNodeId,
        kind,
      });
    },
    [screenToWorld],
  );

  // wheel 必须用原生非 passive 监听：React 17+ 把 onWheel 按 passive 挂在根容器，
  // handler 里 preventDefault 是空操作——这正是过去「画布缩放的同时浏览器也在
  // 缩放页面」的根因。手势分流见 classifyWheel：捏合/⌘+滚轮=缩放，普通滚动=平移。
  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;

    // 滚轮落在节点内部可滚动区域（提示词输入框、消息列表等）时让位给原生
    // 滚动——否则 preventDefault 会让这些区域彻底滚不动。只对平移让位：
    // 缩放手势带着明确的「操作画布」意图，照常缩放。
    const inScrollable = (target: EventTarget | null): boolean => {
      let cur = target instanceof HTMLElement ? target : null;
      while (cur && cur !== el) {
        const oy = getComputedStyle(cur).overflowY;
        if ((oy === 'auto' || oy === 'scroll') && cur.scrollHeight > cur.clientHeight + 1) {
          return true;
        }
        cur = cur.parentElement;
      }
      return false;
    };

    const applyView = (next: ViewState) => {
      setView(next);
      // pan/zoom 不再作为服务端内容，只按项目写本地存储。
      if (projectId) saveLocalView(projectId, next);
    };

    const onWheelNative = (e: WheelEvent) => {
      const kind = classifyWheel(e, isPhysicalCtrlHeld());
      if (kind === 'pan' && inScrollable(e.target)) return;
      e.preventDefault();
      const view = useCanvasStore.getState().view;
      if (kind === 'pan') {
        applyView(panView(view, e.deltaX, e.deltaY));
        return;
      }
      const rect = el.getBoundingClientRect();
      const factor = wheelZoomFactor(e.deltaY, kind === 'pinch' ? 'pinch' : 'wheel');
      applyView(
        zoomViewAt(view, e.clientX - rect.left, e.clientY - rect.top, view.zoom * factor),
      );
    };

    // Safari 的捏合不走 ctrl+wheel，走非标准的 gesture* 事件，单独接一路。
    // e.scale 是相对手势起点的累计倍率，所以要记住起点 zoom。
    let gestureStartZoom: number | null = null;
    const onGestureStart = (e: Event) => {
      e.preventDefault();
      gestureStartZoom = useCanvasStore.getState().view.zoom;
    };
    const onGestureChange = (e: Event) => {
      e.preventDefault();
      if (gestureStartZoom == null) return;
      const g = e as Event & { scale: number; clientX: number; clientY: number };
      const rect = el.getBoundingClientRect();
      const view = useCanvasStore.getState().view;
      applyView(
        zoomViewAt(view, g.clientX - rect.left, g.clientY - rect.top, gestureStartZoom * g.scale),
      );
    };
    const onGestureEnd = (e: Event) => {
      e.preventDefault();
      gestureStartZoom = null;
    };

    el.addEventListener('wheel', onWheelNative, { passive: false });
    el.addEventListener('gesturestart', onGestureStart, { passive: false });
    el.addEventListener('gesturechange', onGestureChange, { passive: false });
    el.addEventListener('gestureend', onGestureEnd, { passive: false });
    return () => {
      el.removeEventListener('wheel', onWheelNative);
      el.removeEventListener('gesturestart', onGestureStart);
      el.removeEventListener('gesturechange', onGestureChange);
      el.removeEventListener('gestureend', onGestureEnd);
    };
  }, [setView, projectId]);

  // 空白处按住拖动 / Space 抓手共用的平移逻辑。
  const beginPan = useCallback(
    (startClientX: number, startClientY: number) => {
      const startView = { ...useCanvasStore.getState().view };
      let moved = false;
      const onMove = (ev: MouseEvent) => {
        if (
          !moved &&
          (Math.abs(ev.clientX - startClientX) > 2 || Math.abs(ev.clientY - startClientY) > 2)
        ) {
          moved = true;
        }
        setView({
          x: startView.x + (ev.clientX - startClientX),
          y: startView.y + (ev.clientY - startClientY),
          zoom: startView.zoom,
        });
      };
      const onUp = () => {
        // 平移过程中只改本地 view，松手后写一次本地存储（避免每帧写 localStorage）。
        if (moved && projectId) saveLocalView(projectId, useCanvasStore.getState().view);
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
      };
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
    },
    [setView, projectId],
  );

  const onMouseDown = useCallback(
    (e: React.MouseEvent) => {
      if (e.button !== 0 && e.button !== 1) return;

      setContextMenu(null);

      const target = e.target as HTMLElement;
      if (
        target.tagName === 'INPUT' ||
        target.tagName === 'TEXTAREA' ||
        target.tagName === 'SELECT' ||
        target.tagName === 'BUTTON' ||
        target.isContentEditable ||
        target.closest('input, textarea, select, button, [contenteditable="true"]')
      ) {
        return;
      }

      // Ctrl/Cmd + 左键 = 框选
      if (e.button === 0 && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        clearSelection();
        const start = { x: e.clientX, y: e.clientY };
        setMarquee({ x1: start.x, y1: start.y, x2: start.x, y2: start.y });
        const onMove = (ev: MouseEvent) => {
          setMarquee({ x1: start.x, y1: start.y, x2: ev.clientX, y2: ev.clientY });
        };
        const onUp = (ev: MouseEvent) => {
          const rect = viewportRef.current?.getBoundingClientRect();
          if (rect) {
            const minX = Math.min(start.x, ev.clientX) - rect.left;
            const minY = Math.min(start.y, ev.clientY) - rect.top;
            const maxX = Math.max(start.x, ev.clientX) - rect.left;
            const maxY = Math.max(start.y, ev.clientY) - rect.top;
            const w1 = { x: (minX - view.x) / view.zoom, y: (minY - view.y) / view.zoom };
            const w2 = { x: (maxX - view.x) / view.zoom, y: (maxY - view.y) / view.zoom };
            const hit = useCanvasStore
              .getState()
              .nodes.filter(
                (n) =>
                  n.x + n.width >= w1.x &&
                  n.x <= w2.x &&
                  n.y + n.height >= w1.y &&
                  n.y <= w2.y,
              )
              .map((n) => n.id);
            if (hit.length > 0) selectMany(hit);
          }
          setMarquee(null);
          window.removeEventListener('mousemove', onMove);
          window.removeEventListener('mouseup', onUp);
        };
        window.addEventListener('mousemove', onMove);
        window.addEventListener('mouseup', onUp);
        return;
      }

      // 在背景上按普通左键或中键 = 平移
      e.preventDefault();
      clearSelection();
      beginPan(e.clientX, e.clientY);
    },
    [view, setView, clearSelection, selectMany, projectId, beginPan],
  );

  // 按住 Space = 临时抓手：盖一层透明 overlay 接管 mousedown，节点拖拽/框选
  // 全部让位，只做平移；不 clearSelection——抓手不该弄丢用户的选中。
  const [spaceHeld, setSpaceHeld] = useState(false);
  useEffect(() => {
    const isTyping = (t: EventTarget | null) => {
      const target = t as HTMLElement | null;
      const tag = target?.tagName;
      return (
        tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || !!target?.isContentEditable
      );
    };
    const onDown = (e: KeyboardEvent) => {
      if (e.code !== 'Space' || e.repeat || isTyping(e.target)) return;
      if (document.querySelector('[data-director-desk-open],[data-storyboard-open]')) return;
      // 空格默认会滚动页面/激活聚焦中的按钮，按住当抓手期间全部屏蔽。
      e.preventDefault();
      setSpaceHeld(true);
    };
    const onUp = (e: KeyboardEvent) => {
      if (e.code === 'Space') setSpaceHeld(false);
    };
    const onBlur = () => setSpaceHeld(false);
    window.addEventListener('keydown', onDown);
    window.addEventListener('keyup', onUp);
    window.addEventListener('blur', onBlur);
    return () => {
      window.removeEventListener('keydown', onDown);
      window.removeEventListener('keyup', onUp);
      window.removeEventListener('blur', onBlur);
    };
  }, []);

  // ⌘/Ctrl +、-、0 的键盘缩放；锚点取视口中心。
  const keyboardZoom = useCallback(
    (factor: number) => {
      const rect = viewportRef.current?.getBoundingClientRect();
      if (!rect) return;
      const v = useCanvasStore.getState().view;
      const next = zoomViewAt(v, rect.width / 2, rect.height / 2, v.zoom * factor);
      setView(next);
      if (projectId) saveLocalView(projectId, next);
    },
    [setView, projectId],
  );

  const fitToNodes = useCallback(() => {
    const rect = viewportRef.current?.getBoundingClientRect();
    if (!rect) return;
    const next = fitView(useCanvasStore.getState().nodes, rect.width, rect.height);
    setView(next);
    if (projectId) saveLocalView(projectId, next);
  }, [setView, projectId]);

  useEffect(() => {
    const findPortElement = (el: HTMLElement | null): HTMLElement | null => {
      // 从点击目标往上找带 data-port-type 的元素
      let current = el;
      for (let i = 0; i < 5 && current; i++) {
        if (current.dataset?.portType) return current;
        current = current.parentElement;
      }
      return null;
    };

    const onPortMouseDown = (e: MouseEvent) => {
      const portEl = findPortElement(e.target as HTMLElement | null);
      if (!portEl) return;
      const portType = portEl.dataset?.portType;
      if (portType !== 'output') return;
      const fromId = portEl.dataset?.portNode;
      if (!fromId) return;
      e.stopPropagation();
      e.preventDefault();

      setConnecting({ fromId, screenX: e.clientX, screenY: e.clientY });
      setStoreConnecting({ fromId, hoverTargetId: null });

      const findDropTarget = (clientX: number, clientY: number): { nodeId: string; inputType?: string } | null => {
        const el = document.elementFromPoint(clientX, clientY) as HTMLElement | null;
        // 优先级 1：带类型的 input 端口（Agent 的 meta/user）。捕获
        // inputType 字符串，让连接带上它。
        const portEl = el?.closest('[data-port-type="input"]') as HTMLElement | null;
        if (portEl) {
          const nodeId = portEl.dataset?.portNode;
          if (nodeId && nodeId !== fromId) {
            return { nodeId, inputType: portEl.dataset?.inputType };
          }
        }
        // 优先级 2：节点 body 内的任意位置——默认/隐式端口。
        const nodeEl = el?.closest('[data-node-id]') as HTMLElement | null;
        const nodeId = nodeEl?.dataset?.nodeId ?? null;
        return nodeId && nodeId !== fromId ? { nodeId } : null;
      };

      const onMove = (ev: MouseEvent) => {
        setConnecting({ fromId, screenX: ev.clientX, screenY: ev.clientY });
        setConnectingHoverTarget(findDropTarget(ev.clientX, ev.clientY)?.nodeId ?? null);
      };
      const onUp = (ev: MouseEvent) => {
        const hit = findDropTarget(ev.clientX, ev.clientY);
        if (hit) {
          addConnection(fromId, hit.nodeId, hit.inputType);
        } else {
          // 松手落在背景上 → 打开已预连到 source 的节点菜单。
          openContextMenuAt(ev.clientX, ev.clientY, fromId);
        }
        setConnecting(null);
        setStoreConnecting(null);
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
      };
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
    };
    window.addEventListener('mousedown', onPortMouseDown, true);
    return () => window.removeEventListener('mousedown', onPortMouseDown, true);
  }, [addConnection, openContextMenuAt, setStoreConnecting, setConnectingHoverTarget]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target?.isContentEditable) {
        return;
      }
      // 全屏/覆盖层挡住画布时整体停用画布快捷键，两种情况都要拦：
      // 1) 3D 导演台全屏面板打开——desk 自己在 window 上监听 Ctrl+C/V/Z（复制对象/
      //    撤销场景），不让路的话同一次按键双触发：画布把选中节点撤销/删除了，
      //    用户以为只动了 3D 场景。标记由 DirectorDeskNode 的 portal 包裹层携带。
      // 2) 故事板覆盖层打开——画布被完全盖住但没卸载，selectedIds 仍在 store 里；
      //    不拦的话用户在故事板视图按 Delete/Cmd+Z 会静默删掉/撤销画布上看不见的
      //    节点。标记由 CanvasEditorPage 的覆盖层 div 携带。
      // 两个标记都是面板/覆盖层关闭即消失，不需要额外清理。
      if (document.querySelector('[data-director-desk-open],[data-storyboard-open]')) {
        return;
      }
      // ⌘/Ctrl + '='(即 +)、'-'、'0'：缩放与适应画布。preventDefault 把浏览器
      // 自己的整页缩放快捷键在画布页里让位给画布（Lovart 同款取舍）。
      if ((e.metaKey || e.ctrlKey) && (e.key === '=' || e.key === '+')) {
        e.preventDefault();
        keyboardZoom(1.2);
        return;
      }
      if ((e.metaKey || e.ctrlKey) && (e.key === '-' || e.key === '_')) {
        e.preventDefault();
        keyboardZoom(1 / 1.2);
        return;
      }
      if ((e.metaKey || e.ctrlKey) && e.key === '0') {
        e.preventDefault();
        fitToNodes();
        return;
      }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'z' && !e.shiftKey) {
        e.preventDefault();
        undo();
        return;
      }
      if (
        (e.metaKey || e.ctrlKey) &&
        (e.key.toLowerCase() === 'y' ||
          (e.key.toLowerCase() === 'z' && e.shiftKey))
      ) {
        e.preventDefault();
        redo();
        return;
      }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'd') {
        e.preventDefault();
        duplicateNodes(Array.from(useCanvasStore.getState().selectedIds));
        return;
      }
      if (e.key === 'Delete' || e.key === 'Backspace') {
        const ids = Array.from(useCanvasStore.getState().selectedIds);
        if (ids.length > 0) {
          e.preventDefault();
          deleteNodes(ids);
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [undo, redo, deleteNodes, duplicateNodes, keyboardZoom, fitToNodes]);

  // 「复制节点」的写入侧。挂在 copy 事件而不是 keydown：⌘C 天然触发 copy，
  // 菜单入口经 copyNodesNow 的 execCommand('copy') 也走到这里——两条路一份逻辑。
  // 只有 copy 事件的 clipboardData 能写自定义 MIME（异步 API 写不了），格式外部
  // 应用不认识，所以微信等地方什么都粘不出（用户要求：只在画布内有效）。
  useEffect(() => {
    const onCopy = (e: ClipboardEvent) => {
      const override = pendingCopyOverride();
      if (!override) {
        // ⌘C 路径的三道放行：输入框里是复制文字；页面上有选中文字是复制文字；
        // 全屏面板/故事板盖住画布时不抢。菜单路径(override)是明确指令，不设防。
        const target = e.target as HTMLElement | null;
        if (target?.closest('input,textarea,[contenteditable="true"],[contenteditable=""]')) return;
        if ((window.getSelection()?.toString() ?? '') !== '') return;
        if (document.querySelector('[data-director-desk-open],[data-storyboard-open]')) return;
      }
      const ids = override ?? Array.from(useCanvasStore.getState().selectedIds);
      if (ids.length === 0) return;
      const payload = buildPayload(ids);
      if (!payload || !e.clipboardData) return;
      e.preventDefault();
      e.clipboardData.setData(NODE_CLIPBOARD_MIME, JSON.stringify(payload));
      writeMirror(payload);
      message.success(`已复制 ${payload.nodes.length} 个节点`);
    };
    document.addEventListener('copy', onCopy);
    return () => document.removeEventListener('copy', onCopy);
  }, [message]);

  const tempEdgePath = useMemo(() => {
    if (!connecting) return null;
    const rect = viewportRef.current?.getBoundingClientRect();
    if (!rect) return null;
    const src = nodes.find((n) => n.id === connecting.fromId);
    if (!src) return null;
    const sxWorld = src.x + src.width;
    const syWorld = src.y + src.height / 2;
    const startX = sxWorld * view.zoom + view.x;
    const startY = syWorld * view.zoom + view.y;
    const endX = connecting.screenX - rect.left;
    const endY = connecting.screenY - rect.top;
    const dx = Math.max(40, Math.abs(endX - startX) * 0.5);
    return `M ${startX} ${startY} C ${startX + dx} ${startY}, ${endX - dx} ${endY}, ${endX} ${endY}`;
  }, [connecting, nodes, view]);

  const rect = viewportRef.current?.getBoundingClientRect();
  const marqueeRect =
    marquee && rect
      ? {
          left: Math.min(marquee.x1, marquee.x2) - rect.left,
          top: Math.min(marquee.y1, marquee.y2) - rect.top,
          width: Math.abs(marquee.x2 - marquee.x1),
          height: Math.abs(marquee.y2 - marquee.y1),
        }
      : null;

  // 拖入的文件 → 每个合法文件建一个输入节点并上传,等同于走一次「上传」入口。
  // 非图片/音频/视频弹框拒绝;混合批次里合法的照常上传,不因一个坏文件惩罚整批。
  const uploadDroppedFiles = useCallback(
    (files: File[], world: { x: number; y: number }) => {
      const { accepted, rejected } = partitionDroppedFiles(files);

      if (rejected.length > 0) {
        modal.error({
          title: '这些文件不支持',
          content: renderRejected(rejected),
        });
      }
      if (accepted.length === 0) return;

      const points = layoutDropPoints(
        accepted.map((a) => DEFAULT_SIZES[a.nodeType]),
        world,
      );
      // 并发发起,各自独立成败——一个大视频传失败不该拖累同批的图片。
      accepted.forEach(({ file, nodeType }, i) => {
        const nodeId = addNode(nodeType, points[i]);
        if (!nodeId) return; // 只读模式下 addNode 返回空串
        void uploadFileIntoNode(nodeId, file);
      });
    },
    [modal, addNode],
  );

  // 粘贴的「节点」分支：重发 id 落画布。跨项目 payload 的资产引用登记也挂在
  // 这里——先落节点再登记，登记失败不回滚（节点本身立即可用，引用行只影响
  // 「删源项目后文件是否被 GC」这个远期问题，可重粘补救）。
  const pasteNodesFromPayload = useCallback(
    (payload: NodeClipboardPayload, world: { x: number; y: number }) => {
      const ids = useCanvasStore.getState().pasteNodes(payload.nodes, payload.connections, world);
      if (ids.length === 0) return;
      message.success(`已粘贴 ${ids.length} 个节点`);

      // 跨项目粘贴 → 给本项目登记这批媒体的引用行，防源项目删除后被 GC。
      // 失败不打断也不回滚：节点本身已可用，引用行只影响「删源项目后文件
      // 是否保得住」这个远期问题，且重粘一次即可补登记（端点幂等）。
      const targetProjectId = projectId ?? currentProjectId();
      if (!targetProjectId || !payload.sourceProjectId) return;
      if (payload.sourceProjectId === targetProjectId) return;
      const keys = collectMediaKeys(payload);
      if (keys.length === 0) return;
      importAssetRefs(targetProjectId, keys).then(
        (res) => {
          if (res.missing?.length) {
            // 老节点没有记账行属正常历史欠账，仅记录，不打扰用户。
            console.warn('importAssetRefs: missing keys', res.missing);
          }
        },
        (err) => {
          console.error('importAssetRefs failed', err);
          message.warning('媒体引用登记失败，可重新粘贴一次补登记');
        },
      );
    },
    [message, projectId],
  );

  // 粘贴的「文字」分支：建文字节点（对标 libtv）。
  const pasteTextAsNode = useCallback(
    (text: string, world: { x: number; y: number }) => {
      const id = addNode('text-node', world);
      if (!id) return; // 只读
      updateNodeSettings(id, { text });
      // addNode 只落了空壳，text 在 settings 里，必须显式持久化（同 uploadIntoNode 的道理）。
      persistNodeById(id);
    },
    [addNode, updateNodeSettings],
  );

  // ⌘/Ctrl+V 粘贴。走原生 paste 事件而不是 keydown + clipboard.read()：事件自带
  // clipboardData，能同步读到自定义 MIME 和文件，且不触发权限弹窗。
  // 优先级：节点 payload → 文件（图/音/视频，走拖拽同一套分类上传）→ 文字。
  // 落点取视口中心——按键没有鼠标坐标可用。
  useEffect(() => {
    const onPaste = (e: ClipboardEvent) => {
      if (readOnly) return;
      // 焦点在输入框/富文本里的粘贴是在输入文字，不该抢。
      const target = e.target as HTMLElement | null;
      if (target?.closest('input,textarea,[contenteditable="true"],[contenteditable=""]')) return;
      // 全屏面板/故事板盖住画布时停用，与快捷键 onKey 的守卫同理。
      if (document.querySelector('[data-director-desk-open],[data-storyboard-open]')) return;
      const rect = viewportRef.current?.getBoundingClientRect();
      if (!rect) return;
      const world = screenToWorld(rect.left + rect.width / 2, rect.top + rect.height / 2);

      const payload = payloadFromClipboardEvent(e);
      if (payload) {
        e.preventDefault();
        pasteNodesFromPayload(payload, world);
        return;
      }
      const files = Array.from(e.clipboardData?.files ?? []);
      if (files.length > 0) {
        e.preventDefault();
        uploadDroppedFiles(files, world);
        return;
      }
      const text = e.clipboardData?.getData('text/plain') ?? '';
      if (text.trim()) {
        e.preventDefault();
        pasteTextAsNode(text, world);
      }
    };
    window.addEventListener('paste', onPaste);
    return () => window.removeEventListener('paste', onPaste);
  }, [readOnly, screenToWorld, uploadDroppedFiles, pasteNodesFromPayload, pasteTextAsNode]);

  const onDropFromExternal = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      dragDepthRef.current = 0;
      setFileHover(false);

      // 操作系统拖进来的文件。放在最前面:内部 DnD 的 payload 走 getData,
      // 两者不会同时出现。
      const files = Array.from(e.dataTransfer.files ?? []);
      if (files.length > 0) {
        if (readOnly) return;
        uploadDroppedFiles(files, screenToWorld(e.clientX, e.clientY));
        return;
      }

      const libraryItemRaw = e.dataTransfer.getData(LIBRARY_DRAG_MIME);
      if (libraryItemRaw && projectId) {
        const itemId = parseInt(libraryItemRaw, 10);
        if (!Number.isFinite(itemId) || itemId <= 0) return;
        const world = screenToWorld(e.clientX, e.clientY);
        applyLibraryItemToCanvas(itemId, projectId)
          .then((res) => {
            const nodeId = addNode(nodeTypeForKind(res.kind), world);
            updateNode(nodeId, { content: res.url, status: 'succeeded' });
            updateNodeSettings(nodeId, {
              url: res.url,
              assetId: res.assetId,
              libraryItemId: itemId,
              mimeType: res.mimeType,
            });
            persistNodeById(nodeId);
          })
          .catch((err) => console.error('apply library item failed', err));
        return;
      }

      const raw = e.dataTransfer.getData('application/x-tvmao-history');
      if (!raw) return;
      try {
        const { url, isVideo, kind } = JSON.parse(raw) as {
          url: string;
          isVideo: boolean;
          kind?: 'image' | 'video' | 'audio';
        };
        if (!url) return;
        const world = screenToWorld(e.clientX, e.clientY);
        // kind 是三态真源（历史面板新版才带）；缺失时回退旧的二元 isVideo。
        const nodeType =
          kind === 'audio' ? 'input-audio' : (kind === 'video' || (!kind && isVideo)) ? 'video-input' : 'input-image';
        const nodeId = addNode(nodeType, world);
        updateNode(nodeId, { content: url, status: 'succeeded' });
        persistNodeById(nodeId);
      } catch (err) {
        console.error('invalid drop payload', err);
      }
    },
    [screenToWorld, addNode, updateNode, updateNodeSettings, projectId, readOnly, uploadDroppedFiles],
  );

  // 这里曾有一段「Node → 消息气泡同步」：选中单个带 agentMeta 的节点，就把
  // agent 面板切到该节点的 thread 并把对应气泡滚进视野。已按用户要求移除——
  // 选中节点是个高频动作（拖一下、点一下都算），它不该反过来劫持右侧面板：
  // 正在往上翻历史时面板自己滚走，甚至整个会话被换掉。反向定位（从气泡点回
  // 节点）仍在 AgentPanel/MessageBubble 里，那是用户主动发起的，保留。

  return (
    <div
      ref={viewportRef}
      // 画布底色不用全局 bg-background(纯黑)：纯黑上节点/连线对比过强，参考
      // Lovart 画布取 #141414 的深灰。只影响画布视口，不动全局主题。
      className="absolute inset-0 bg-[#141414] overflow-hidden"
      onMouseDown={onMouseDown}
      onDragOver={(e) => {
        if (
          e.dataTransfer.types.includes('application/x-tvmao-history') ||
          e.dataTransfer.types.includes(LIBRARY_DRAG_MIME) ||
          // 'Files' 分支必须 preventDefault:否则浏览器接管,把拖进来的文件
          // 在当前标签页直接打开——用户连人带未保存的画布一起被带离页面。
          (!readOnly && e.dataTransfer.types.includes('Files'))
        ) {
          e.preventDefault();
          e.dataTransfer.dropEffect = 'copy';
        }
      }}
      onDragEnter={(e) => {
        if (readOnly || !e.dataTransfer.types.includes('Files')) return;
        dragDepthRef.current += 1;
        setFileHover(true);
      }}
      onDragLeave={(e) => {
        if (readOnly || !e.dataTransfer.types.includes('Files')) return;
        dragDepthRef.current -= 1;
        if (dragDepthRef.current <= 0) {
          dragDepthRef.current = 0;
          setFileHover(false);
        }
      }}
      onDrop={onDropFromExternal}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
        // 右键弹动作菜单；双击背景仍是添加节点菜单，两个入口分工不同。
        openContextMenuAt(e.clientX, e.clientY, undefined, 'actions');
      }}
      onDoubleClick={(e) => {
        // 只有双击 canvas 背景时才打开节点菜单，双击节点不打开
        if (e.target === e.currentTarget || (e.target as HTMLElement).closest('[data-node-id]') === null) {
          e.preventDefault();
          e.stopPropagation();
          openContextMenuAt(e.clientX, e.clientY);
        }
      }}
    >
      {(() => {
        // TapNow 风格的背景点阵：整个图案随 zoom 缩放（gap 和点半径都缩放），
        // 所以放大时点会变大变清晰，缩小时点会变淡——和 React Flow 的
        // Background 一致。世界坐标系的图案（React Flow / TapNow 行为）：
        // gap 和点半径都随 zoom 线性缩放，没有下限。效果：
        //   • 放大 → 稀疏、更大、更明显的点
        //   • 缩小 → 密集的亚像素点，糊成一层淡淡的纹理
        const dotR = Math.max(0.5, 1.0 * view.zoom);
        const gap = 56 * view.zoom;
        // 透明度也随 zoom 缩放：zoom <=1 时淡，zoom>1 时更亮。
        const opacity = Math.max(0.08, Math.min(0.40, 0.18 * view.zoom));
        return (
          <div
            className="absolute inset-0 pointer-events-none"
            style={{
              backgroundImage: `radial-gradient(circle, rgba(255,255,255,${opacity}) ${dotR}px, transparent ${dotR}px)`,
              backgroundSize: `${gap}px ${gap}px`,
              backgroundPosition: `${view.x}px ${view.y}px`,
            }}
          />
        );
      })()}

      <div
        className="absolute left-0 top-0"
        style={{
          transform: `translate(${view.x}px, ${view.y}px) scale(${view.zoom})`,
          transformOrigin: '0 0',
          // 节点下方的浮动 prompt 面板要求屏幕上恒定大小（不随画布缩放），
          // 各面板用 scale(var(--inv-zoom)) 抵消这里的 scale(zoom)。变量在
          // 这层统一下发，面板侧就不用各自订阅 zoom。
          ['--inv-zoom' as string]: String(1 / view.zoom),
        }}
      >
        <ConnectionLayer nodes={nodes} />
        {nodes.map((node) => {
          const Component = nodeComponents[node.type];
          if (!Component) return null;
          return <Component key={node.id} id={node.id} />;
        })}
      </div>

      {nodes.length === 0 && (
        <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
          <div className="text-center select-none">
            <div className="inline-flex items-center gap-2 px-4 py-2 rounded-full bg-card/80 border border-input text-sidebar-foreground text-xs backdrop-blur">
              空白画布
            </div>
            <div className="mt-4 text-muted-foreground text-sm">右键空白区域，开始添加节点</div>
            <div className="mt-2 text-muted-foreground text-[11px]">
              图片输入 · 文字节点 · AI 绘图 · AI 视频 · 预览
            </div>
          </div>
        </div>
      )}

      {tempEdgePath && (
        <svg className="absolute inset-0 pointer-events-none" width="100%" height="100%" style={{ overflow: 'visible' }}>
          <path d={tempEdgePath} stroke="#60a5fa" strokeWidth={2} fill="none" strokeDasharray="4 4" />
        </svg>
      )}

      {marqueeRect && (
        <div
          className="absolute border border-blue-500 bg-blue-500/10 pointer-events-none"
          style={marqueeRect}
        />
      )}

      {/* 文件悬停反馈。没有它用户不知道能不能松手——画布此前对外部文件毫无反应。 */}
      {fileHover && (
        <div className="absolute inset-0 z-50 flex items-center justify-center pointer-events-none">
          <div className="absolute inset-2 rounded-2xl border-2 border-dashed border-blue-400/70 bg-blue-500/5" />
          <div className="relative px-4 py-2 rounded-full bg-card/90 border border-blue-400/40 text-sm text-foreground backdrop-blur">
            松开以上传 · 图片 / 音频 / 视频
          </div>
        </div>
      )}

      {/* Space 抓手：透明层截胡 mousedown，只平移，不选中/拖拽节点。 */}
      {spaceHeld && (
        <div
          className="absolute inset-0 z-40"
          style={{ cursor: 'grab' }}
          onMouseDown={(e) => {
            if (e.button !== 0 && e.button !== 1) return;
            e.preventDefault();
            e.stopPropagation();
            beginPan(e.clientX, e.clientY);
          }}
        />
      )}

      {contextMenu && contextMenu.kind === 'actions' && (
        <CanvasContextMenu
          x={contextMenu.screenX}
          y={contextMenu.screenY}
          worldX={contextMenu.worldX}
          worldY={contextMenu.worldY}
          // 原位切到添加节点菜单——坐标不变，只换 kind。
          onAddNode={() => setContextMenu({ ...contextMenu, kind: 'add-node' })}
          onPasteNodes={(payload) =>
            pasteNodesFromPayload(payload, { x: contextMenu.worldX, y: contextMenu.worldY })
          }
          onPasteFiles={(files) =>
            uploadDroppedFiles(files, { x: contextMenu.worldX, y: contextMenu.worldY })
          }
          onPasteText={(text) =>
            pasteTextAsNode(text, { x: contextMenu.worldX, y: contextMenu.worldY })
          }
          onClose={() => setContextMenu(null)}
        />
      )}
      {contextMenu && contextMenu.kind === 'add-node' && (
        <ContextMenu
          x={contextMenu.screenX}
          y={contextMenu.screenY}
          worldX={contextMenu.worldX}
          worldY={contextMenu.worldY}
          sourceNodeId={contextMenu.sourceNodeId}
          onPickFromHistory={
            projectId
              ? () =>
                  setHistoryPicker({
                    worldX: contextMenu.worldX,
                    worldY: contextMenu.worldY,
                    sourceNodeId: contextMenu.sourceNodeId,
                  })
              : undefined
          }
          onClose={() => setContextMenu(null)}
        />
      )}

      {historyPicker && projectId && (
        <GenerationHistoryPickerModal
          projectId={projectId}
          worldX={historyPicker.worldX}
          worldY={historyPicker.worldY}
          sourceNodeId={historyPicker.sourceNodeId}
          onClose={() => setHistoryPicker(null)}
        />
      )}
    </div>
  );
}
