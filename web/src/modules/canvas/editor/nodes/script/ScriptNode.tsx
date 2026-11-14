import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { AlignLeft, Check, FileText } from 'lucide-react';
import { useCanvasStore } from '../../store';
import { NodeShell } from '../../NodeShell';
import { NodeContextMenu, type NodeMenuItem } from '../NodeContextMenu';
import { standardNodeMenuItems } from '../standardNodeMenuItems';
import { readScriptSettings, scriptStepStats } from './scriptData';
import { ScriptPanel } from './ScriptPanel';
import { BatchVideoDialog } from './BatchVideoDialog';
import { ScriptGenerator, useScriptGenPolling } from './ScriptGenerator';

// 卡片中央的「四横线」文本占位图形——脚本生成器卡片的视觉锚点。
// 用 div 画而不是引图标：图形是不等宽的几条圆角横线，lucide 没有同形图标，
// 硬凑（AlignLeft 之类）线条宽度节奏对不上。
function TextGlyph() {
  return (
    <div className="flex flex-col gap-1.5 items-start" aria-hidden>
      <div className="h-1.5 w-10 rounded-full bg-muted-foreground/40" />
      <div className="h-1.5 w-14 rounded-full bg-muted-foreground/40" />
      <div className="h-1.5 w-14 rounded-full bg-muted-foreground/40" />
      <div className="h-1.5 w-7 rounded-full bg-muted-foreground/40" />
    </div>
  );
}

// 三步横向步骤圆圈：完成=白底黑勾，未完成=灰圈数字，步骤间细连接线。
// 与全屏面板顶部步骤条同一套完成判定语义，只是卡片上的紧凑投影。
function StepCircles({ stats }: { stats: ReturnType<typeof scriptStepStats> }) {
  const steps = [
    { label: '确认分镜', done: stats.shots > 0 },
    { label: '准备资产', done: stats.assetsTotal > 0 && stats.assetsReady === stats.assetsTotal },
  ];
  return (
    <div className="flex items-start justify-center">
      {steps.map((s, i) => (
        <div key={s.label} className="flex items-start">
          {i > 0 && <div className="w-7 h-px bg-border mx-1 mt-2.5" />}
          <div className="flex flex-col items-center gap-1">
            <div
              className={
                s.done
                  ? 'w-5 h-5 rounded-full bg-foreground text-background flex items-center justify-center'
                  : 'w-5 h-5 rounded-full border border-muted-foreground/50 text-muted-foreground flex items-center justify-center text-[10px]'
              }
            >
              {s.done ? <Check size={12} strokeWidth={3} /> : i + 1}
            </div>
            <span className="text-[10px] text-muted-foreground whitespace-nowrap">{s.label}</span>
          </div>
        </div>
      ))}
    </div>
  );
}

export function ScriptNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodesById.get(id));
  // 面板开合是纯 UI 状态，不需要持久化到节点 settings——刷新画布后默认收起即可。
  const [open, setOpen] = useState(false);
  // 右键菜单坐标 + 批量生视频弹层开合，两个都是纯本地 UI 状态。菜单只有「批量
  // 生成视频」一项，没必要为单项复用 useMediaNodeMenu——那个 hook 捆的是媒体节点
  // 专属的合规校验/复制/保存到素材库，脚本节点不沾这些。
  const [menuPos, setMenuPos] = useState<{ x: number; y: number } | null>(null);
  const [batchVideoOpen, setBatchVideoOpen] = useState(false);
  // 生成器面板开合。任务轮询不挂在面板里而挂在节点层：taskId 持久化在
  // settings.gen，面板关掉/页面刷新都不中断观察。
  const [genOpen, setGenOpen] = useState(false);
  const isSelected = useCanvasStore((s) => s.selectedIds.has(id));
  const deleteNodes = useCanvasStore((s) => s.deleteNodes);
  // 取消选中节点 = 退出生成模式:genOpen 若永久为 true,节点以后每次选中都会
  // 弹生成器且手写入口被藏死,用户想换回「自己编写」就没路了。
  useEffect(() => {
    if (!isSelected) setGenOpen(false);
  }, [isSelected]);
  useScriptGenPolling(id);

  if (!node) return null;
  const settings = readScriptSettings(node.settings);
  const stats = scriptStepStats(settings);
  const genRunning = settings.gen.taskId > 0;
  // 生成器面板可见性：任务进行中恒显示（用户要能看到进度/结果）；否则跟随
  // 「点过入口 + 节点被选中」——对齐 GenImageNode 浮动 prompt 面板的聚焦语义。
  const genVisible = genRunning || (genOpen && isSelected);
  // 批量生视频的可用门槛：存在 prompt 非空的分镜行（skill 对齐版没有"合成完成"
  // 状态机，prompt 有内容即可铺节点）。
  const hasSpawnableRows = settings.rows.some((r) => r.prompt.trim() !== '');
  // 空态判定：一行镜头都没有 = 用户还没开始，卡片展示「尝试：」引导入口；
  // 有行之后切换为三步进度态。
  const empty = stats.shots === 0;

  const menuItems: NodeMenuItem[] = [
    {
      key: 'batch-video',
      label: '批量生成视频',
      onPick: () => {
        setMenuPos(null);
        setBatchVideoOpen(true);
      },
      disabled: !hasSpawnableRows,
      hint: hasSpawnableRows ? undefined : '需先有带提示词的分镜',
    },
    // 复制节点/创建副本是所有节点右键菜单的标配（standardNodeMenuItems）。
    ...standardNodeMenuItems(id, () => setMenuPos(null)),
    // 删除固定放最后一项——与 useMediaNodeMenu / NodeShell 兜底菜单的约定一致。
    // 本卡片的 onContextMenu 拦截了整个 body，壳级兜底菜单到不了这里，得自带。
    {
      key: 'delete',
      label: '删除',
      danger: true,
      onPick: () => {
        setMenuPos(null);
        deleteNodes([id]);
      },
      hint: '删除本节点，可撤销（Ctrl/Cmd+Z）',
    },
  ];

  return (
    // bodyOverflow=visible：下方浮动生成器面板靠它溢出节点边界（同 GenImageNode
    // 的浮动 prompt 面板）——默认 overflow-hidden 会把面板整个裁没，点了像没反应。
    <NodeShell node={node} inputs={0} outputs={1} minWidth={260} minHeight={180} bodyOverflow="visible">
      <div
        data-testid="script-node-card"
        className="script-borders flex-1 flex flex-col p-3 min-h-0"
        onContextMenu={(e) => {
          e.preventDefault();
          e.stopPropagation();
          setMenuPos({ x: e.clientX, y: e.clientY });
        }}
      >
        {empty ? (
          <>
            {/* 空态＝引导态：中央占位图形 + 「尝试：」入口列表。 */}
            <div className="flex-1 flex items-center justify-center py-3">
              <TextGlyph />
            </div>
            {genVisible ? (
              /* 生成模式进行时两个「尝试」入口整体隐藏——两条路互斥，开着生成器
                 还能点「自己编写」会岔出第二条路（面板一开还自动种一行空分镜，
                 卡片立刻跳成步骤态）。点画布空白取消选中即可退出生成模式。 */
              <div className="pb-1 text-[11px] text-muted-foreground">
                {genRunning ? '分镜生成中…' : '在下方输入框描述剧情，生成分镜脚本'}
              </div>
            ) : (
              <div className="flex flex-col gap-0.5 pb-1">
                <span className="text-[11px] text-muted-foreground mb-1">尝试：</span>
                <button
                  type="button"
                  data-testid="script-try-generate"
                  onClick={() => setGenOpen(true)}
                  className="flex items-center gap-2 py-1 text-xs text-foreground bg-transparent border-none text-left cursor-pointer hover:text-blue-400 transition-colors p-0"
                >
                  <AlignLeft size={14} />
                  剧本生成分镜脚本
                </button>
                <button
                  type="button"
                  data-testid="script-try-manual"
                  onClick={() => setOpen(true)}
                  className="flex items-center gap-2 py-1 text-xs text-foreground bg-transparent border-none text-left cursor-pointer hover:text-blue-400 transition-colors p-0"
                >
                  <FileText size={14} />
                  自己编写分镜脚本
                </button>
              </div>
            )}
          </>
        ) : (
          <>
            <div className="flex-1 flex items-center justify-center">
              <TextGlyph />
            </div>
            <div className="pb-3 pt-1">
              <StepCircles stats={stats} />
            </div>
            {/* 显式 bg + border-none：原生 <button> 的 UA 默认样式是浅灰底，暗色画布
                里会渲染成刺眼白块。这里用深灰次级按钮，不用主色调。 */}
            <button
              type="button"
              data-testid="script-open-panel"
              onClick={() => setOpen(true)}
              className="w-full flex items-center justify-center py-2 bg-muted hover:bg-accent text-foreground text-xs rounded-lg transition-colors border-none cursor-pointer"
            >
              打开脚本节点 →
            </button>
          </>
        )}
      </div>
      {/* 浮动生成器面板——借助 NodeShell body 的 overflow:visible 溢出到节点下方，
          水平居中（同 GenImageNode 浮动 prompt 面板的定位方案）。 */}
      {genVisible && (
        <div
          className="absolute z-20"
          style={{
            top: '100%',
            left: '50%',
            // scale(1/zoom) 抵消画布缩放，面板屏幕上恒定 680px 宽（同 GenImageNode）。
            transform: 'scale(var(--inv-zoom, 1)) translate(-50%, 12px)',
            transformOrigin: '0 0',
            width: 680,
          }}
          onMouseDown={(e) => e.stopPropagation()}
        >
          <ScriptGenerator nodeId={id} />
        </div>
      )}
      {open && createPortal(
        <ScriptPanel nodeId={id} onClose={() => setOpen(false)} />,
        document.body,
      )}
      {menuPos && (
        <NodeContextMenu
          x={menuPos.x}
          y={menuPos.y}
          items={menuItems}
          onClose={() => setMenuPos(null)}
        />
      )}
      {batchVideoOpen && createPortal(
        <BatchVideoDialog nodeId={id} onClose={() => setBatchVideoOpen(false)} />,
        document.body,
      )}
    </NodeShell>
  );
}
