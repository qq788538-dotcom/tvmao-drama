// 故事板详情面板。**不是**仿一个面板——它把画布上那个节点组件本身渲染进来，
// 靠 StoryboardModeContext 让 NodeShell 去掉绝对定位、铺满这个容器。
// 于是参数区、模型选择、提示词编辑器、积分预估、生成按钮、⋯ 菜单、删除、
// 生成进度与错误展示，全部零代码复用，而且天然实时（同一个 store）。
//
// 代价：详情面板长什么样，取决于那个节点在画布上长什么样。比如 input-image
// 是纯素材节点，画布上就没有参数区，这里也不会有。要让某类节点在故事板里
// 长得不一样，就得改那个节点组件本身，画布上会跟着变。
import { useEffect, useRef, useState } from 'react';
import { ChevronLeft, Copy, MoreHorizontal, Trash2, X } from 'lucide-react';
import { nodeComponents } from '../editor/registry';
import { displayLabelFor } from '../editor/NodeShell';
import { useCanvasStore } from '../editor/store';
import { StoryboardModeContext } from './StoryboardContext';

// 节点预览区的高度。压到这个值而不是铺满，是为了给下方浮出的参数面板腾地方
// ——顺带也解决了「图占满整个面板」：420 高的预览区配 400w 的缩略图正好清晰，
// 铺满时那张缩略图被拉到上千像素宽反而糊。
const NODE_H = 420;

// 参数面板的落脚空间。GenImageNode/GenVideoNode 的面板宽 680、含提示词框 +
// 模型行 + 比例行，实测 300-400 高；留 460 有余量，不够时容器会滚动，不会截断。
const PARAMS_SPACE = 460;


/**
 * 顶栏的 ⋯ 菜单：只放「创建副本」和「删除」两项。
 *
 * 两个动作都直接走 store：duplicateNodes 自带 commit(可撤销)、错开位置、
 * 落库；deleteNodes 同理并清理连线。不复用节点内容区的 useMediaNodeMenu
 * ——那份菜单还带「保存到素材库/下载/擦除字幕」等一长串，是给画布上右键
 * 用的，塞进详情面板顶栏太重。
 *
 * 删除后必须收起详情面板：节点没了，面板会指向一个不存在的 id。上层
 * StoryboardView 的 focused 收敛 effect 也会兜住，但这里显式关掉更直接。
 */
function NodeActionsMenu({ nodeId, onDeleted }: { nodeId: string; onDeleted: () => void }) {
  const [open, setOpen] = useState(false);
  const boxRef = useRef<HTMLDivElement | null>(null);
  const duplicateNodes = useCanvasStore((s) => s.duplicateNodes);
  const deleteNodes = useCanvasStore((s) => s.deleteNodes);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!boxRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [open]);

  return (
    <div ref={boxRef} className="relative">
      <button
        type="button"
        aria-label="更多"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="p-1 rounded bg-transparent border-none cursor-pointer text-sidebar-foreground hover:text-foreground"
      >
        <MoreHorizontal size={16} />
      </button>

      {open && (
        <div className="absolute right-0 top-8 z-50 min-w-[132px] py-1 rounded-lg bg-popover border border-border shadow-xl">
          <button
            type="button"
            onClick={() => {
              setOpen(false);
              duplicateNodes([nodeId]);
            }}
            className="w-full flex items-center gap-2 px-3 py-1.5 text-left bg-transparent border-none cursor-pointer text-sm text-popover-foreground hover:bg-accent"
          >
            <Copy size={14} />
            创建副本
          </button>
          <button
            type="button"
            onClick={() => {
              setOpen(false);
              deleteNodes([nodeId]);
              onDeleted();
            }}
            className="w-full flex items-center gap-2 px-3 py-1.5 text-left bg-transparent border-none cursor-pointer text-sm text-destructive-foreground hover:bg-accent"
          >
            <Trash2 size={14} />
            删除
          </button>
        </div>
      )}
    </div>
  );
}

export function StoryboardDetail({ nodeId, onClose }: { nodeId: string; onClose: () => void }) {
  const node = useCanvasStore((s) => s.nodesById.get(nodeId));
  if (!node) return null;

  const Component = nodeComponents[node.type];
  if (!Component) return null;

  const title = displayLabelFor(node);

  return (
    // 跟分区面板同一档底色(#262626)、同样的圆角描边：详情态时它顶替中右两栏，
    // 视觉上应该还是"浮在暗底上的一块面板"，不能变成一片和背景齐平的区域。
    <div className="h-full flex flex-col rounded-lg border border-border bg-[#262626] overflow-hidden">
      <div className="flex items-center gap-2 px-3 py-2 border-b border-border shrink-0">
        {/* 返回箭头与右侧的 ✕ 功能相同（都是收起详情、回到三栏），并存是有意的：
            左上角「返回」符合从列表进详情的心智，右上角「关闭」
            符合浮层的心智，用户从哪个方向想都能找到出口。 */}
        <button
          type="button"
          aria-label="返回"
          onClick={onClose}
          className="p-1 rounded bg-transparent border-none cursor-pointer text-sidebar-foreground hover:text-foreground"
        >
          <ChevronLeft size={18} />
        </button>
        <div className="text-foreground text-sm truncate flex-1" title={title}>
          {title}
        </div>
        <NodeActionsMenu nodeId={node.id} onDeleted={onClose} />
        <button
          type="button"
          aria-label="关闭"
          onClick={onClose}
          className="p-1 rounded bg-transparent border-none cursor-pointer text-sidebar-foreground hover:text-foreground"
        >
          <X size={16} />
        </button>
      </div>

      {/* 节点区**不能**铺满整块区域，必须给下方留白。
          生成节点（AI 绘图/视频/音频）的参数面板是 `absolute; top:100%` ——
          浮在节点**底边以下**（见 GenImageNode.tsx 那段 `top:'100%'`）。节点要是
          高度 100%，参数面板就浮到容器外面，被 overflow-auto 裁得一干二净，
          用户只看得到一张占满面板的大图。
          所以这里把节点区高度压到 NODE_H，下方留 PARAMS_SPACE 的滚动空间给
          那块浮出的面板。两个值都是按现有参数面板的实际高度定的，改参数面板
          布局时要回来核对。
          顶部 padding 比其它三边大：节点的名字 chip 是 `-top-6`（-24px）负偏移，
          伸到 wrapper 上方；容器带 overflow-auto，padding 不够 chip 就被裁掉半截。 */}
      <div className="flex-1 min-h-0 overflow-auto pt-8 px-3 pb-3">
        <StoryboardModeContext.Provider value={true}>
          <div style={{ height: NODE_H }}>
            <Component id={node.id} />
          </div>
        </StoryboardModeContext.Provider>
        {/* 浮出参数面板的落脚空间。它是 absolute 的、不占流，不留这块的话
            容器算不出滚动高度，面板照样滚不到。 */}
        <div style={{ height: PARAMS_SPACE }} aria-hidden />
      </div>
    </div>
  );
}
