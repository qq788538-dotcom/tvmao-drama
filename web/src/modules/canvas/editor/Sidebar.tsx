import { ReactNode } from 'react';
import {
  FolderOpen,
  History,
  Layout,
  MousePointer2,
  Redo2,
  Undo2,
} from 'lucide-react';
import { useCanvasStore } from './store';
import { autoArrangeNodes } from './utils/autoArrange';
import {
  enqueueCanvasMutation,
  geometryPatchFromNode,
  getCanvasOwnerToken,
} from './persistence/canvasMutations';
import { patchCanvasNodePositions } from '@/shared/api';

type Tool = 'select' | 'history';

interface SidebarProps {
  projectId: number;
  activeTool: Tool;
  onToolChange: (tool: Tool) => void;
  historyOpen: boolean;
  onToggleHistory: () => void;
  libraryOpen: boolean;
  onToggleLibrary: () => void;
}

/**
 * 浮动药丸式侧栏（左侧 dock）。
 * 外层列占据布局空间（这样右侧的相邻元素能自然定位）；可见的药丸在这一列里
 * 垂直居中。所有 action 都和之前扁平侧栏完全一致——这是一次纯换皮重构。
 */
export function Sidebar({
  projectId,
  activeTool,
  onToolChange,
  historyOpen,
  onToggleHistory,
  libraryOpen,
  onToggleLibrary,
}: SidebarProps) {
  const undo = useCanvasStore((s) => s.undo);
  const redo = useCanvasStore((s) => s.redo);
  const undoLen = useCanvasStore((s) => s.undoStack.length);
  const redoLen = useCanvasStore((s) => s.redoStack.length);

  const handleAutoArrange = () => {
    const state = useCanvasStore.getState();
    if (state.selectedIds.size < 2) {
      alert('请至少选中两个节点进行智能整理');
      return;
    }
    state.commit();
    const arranged = autoArrangeNodes(state.nodes, state.connections, state.selectedIds);
    useCanvasStore.setState({ nodes: arranged });
    // 几何更新不走 store 的结构性 op 广播（见 store.ts 的 CanvasOp 注释），必须
    // 在调用点显式落库——和拖动松手（NodeShell）同一条 PATCH 路径。漏掉这步
    // 整理只改内存，刷新即回弹：上过生产的真实回归。
    const token = getCanvasOwnerToken();
    if (!token) return;
    const patches = arranged
      .filter((n) => state.selectedIds.has(n.id))
      .map((n) => geometryPatchFromNode(n));
    void enqueueCanvasMutation(() => patchCanvasNodePositions(projectId, token, patches));
  };

  const isSelectActive = activeTool === 'select';
  const isHistoryActive = activeTool === 'history';

  return (
    // 用绝对定位浮在 canvas 之上，这样 canvas 的点阵图案能延伸到它背后。
    // 外层列是 pointer-events-none，让药丸上下的空白处
    // 仍能做 canvas 的 pan/select；药丸本身重新启用 pointer events。
    <div className="absolute inset-y-0 left-0 w-[68px] flex flex-col items-center justify-center z-40 py-4 pointer-events-none">
      {/* 药丸本体：rounded-4xl、模糊的深色表面、
          淡淡的 1px 白色边框。这里必须用 inline style，因为我们的 tailwind
          配置关掉了 preflight，意味着 `border` 和 `backdrop-blur-*` 这些
          utility 拿不到默认的 border-style / backdrop-filter。 */}
      <div
        className="flex flex-col gap-1 items-center rounded-[32px] p-1.5 box-border shadow-xl shadow-black/40 pointer-events-auto"
        style={{
          backgroundColor: 'rgba(28, 28, 28, 0.95)',
          borderWidth: 1,
          borderStyle: 'solid',
          borderColor: 'rgba(255, 255, 255, 0.10)',
          backdropFilter: 'blur(24px)',
          WebkitBackdropFilter: 'blur(24px)',
        }}
      >
        {/* 主 CTA——自动整理（白色圆圈）。 */}
        <DockCta
          onClick={handleAutoArrange}
          icon={<Layout size={18} strokeWidth={2.2} />}
          title="自动整理节点（对齐、排列、去除堆叠）— 需先选中 2+ 节点"
        />

        {/* 工具 & 操作组 */}
        <div className="w-full flex flex-col items-center gap-1 pt-1">
          <DockBtn
            onClick={undo}
            disabled={undoLen === 0}
            icon={<Undo2 size={18} />}
            title="撤销 (Ctrl+Z)"
          />
          <DockBtn
            onClick={redo}
            disabled={redoLen === 0}
            icon={<Redo2 size={18} />}
            title="重做 (Ctrl+Shift+Z)"
          />
          <DockBtn
            onClick={() => onToolChange('select')}
            active={isSelectActive}
            icon={<MousePointer2 size={18} />}
            title="选择"
          />
          <DockBtn
            onClick={() => {
              onToolChange('history');
              onToggleHistory();
            }}
            active={isHistoryActive || historyOpen}
            icon={<History size={18} />}
            title="生成历史"
          />
          <DockBtn
            onClick={onToggleLibrary}
            active={libraryOpen}
            icon={<FolderOpen size={18} />}
            title="素材库"
          />
        </div>

        {/* 这里原来还有「保存项目」和「AI 对话」两个次要按钮。
            保存：局部 mutation 重构之后每个动作都即时落库，它只剩「把 1.5s
            debounce 里的输入提前 flush」这点作用，而失焦本来就会 flush；留着
            反而让人以为不点就会丢东西（Ctrl+S 保留，仍可手动 flush）。
            AI 对话：入口移到了顶栏右侧的「Agent」按钮（CanvasEditorPage 顶栏）。 */}
      </div>
    </div>
  );
}

/** 白色圆形主 CTA。 */
function DockCta({
  onClick,
  icon,
  title,
}: {
  onClick: () => void;
  icon: ReactNode;
  title: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      className="size-10 rounded-full bg-foreground hover:bg-muted text-background hover:text-muted-foreground flex items-center justify-center border-none cursor-pointer transition-colors shadow-md"
    >
      {icon}
    </button>
  );
}

/** 方形图标按钮——次要 dock 项。40px 可点击区域。 */
function DockBtn({
  onClick,
  icon,
  title,
  active = false,
  disabled = false,
}: {
  onClick: () => void;
  icon: ReactNode;
  title: string;
  active?: boolean;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={`size-10 rounded-md flex items-center justify-center border-none cursor-pointer transition-colors ${
        active
          ? 'bg-sidebar-accent text-sidebar-accent-foreground'
          : 'bg-transparent text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground'
      } disabled:opacity-30 disabled:cursor-not-allowed disabled:hover:bg-transparent`}
    >
      {icon}
    </button>
  );
}
