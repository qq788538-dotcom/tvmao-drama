// 图片节点选中态的浮动操作工具条（对标参考产品），所有按钮均已接真实功能：
// 预设类（全景/人像质感/九宫格下拉）铺下游节点；编辑类（多角度/打光/宫格切分/
// 标注/旋转镜像）开各自面板或模式；查看类（下载/放大查看）纯本地零积分。
//
// 定位方案同 TextNode 的浮动工具栏：借 NodeShell bodyOverflow=visible 溢出
// 到节点上方 + scale(var(--inv-zoom)) 抵消画布缩放 → 屏幕尺寸恒定、跟随节点。
// 锚点是 calc(100% + 28px)：28px 是世界单位，用来让开名字 chip（它在 -top-6，
// 同样随 zoom 缩放，所以让位距离也必须用世界单位才能在任意 zoom 下都不重叠）；
// 再由 translate 里的 -10px 叠一段屏幕像素恒定的间隙。
import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { extFromUrl, saveMediaToDisk, saveWithFeedback } from '@/shared/saveToDisk';
import {
  Brush,
  ChevronDown,
  Download,
  FlipHorizontal2,
  Grid3x3,
  Hash,
  Maximize2,
  Orbit,
  ScanFace,
  Sun,
  Telescope,
} from 'lucide-react';
import { fetchGenImagePresets, fetchLightControlConfig, fetchMultiAngleConfig } from '@/shared/api';
import { DEFAULT_SIZES, useCanvasStore } from '../store';
import { persistNodeById } from '../persistence/canvasMutations';
import { groupPresets } from '../utils/presetMenu';
import { PresetIcon } from '../utils/presetIcon';
import { MultiAnglePanel } from '../multiangle/MultiAnglePanel';
import {
  GridSplitMenu,
  GridSplitModeBar,
  GridSplitOverlay,
  type GridSplitMode,
} from './gridsplit/GridSplit';
import { executeGridSplit } from './gridsplit/executeGridSplit';
import { AnnotateMode } from './annotate/AnnotateMode';
import { LightPanel } from '../lighting/LightPanel';
import { RotatePreview } from '../rotate/RotatePreview';

const iconSize = 14;

function BarButton({
  label,
  title,
  active,
  chevron,
  onAction,
  children,
}: {
  label?: string;
  title?: string;
  active?: boolean;
  chevron?: boolean;
  onAction?: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      title={title ?? label}
      // 同 TextToolbar：preventDefault 防抢焦点，stopPropagation 防冒泡到
      // 画布开始拖节点/框选。
      onMouseDown={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
      onClick={(e) => {
        e.stopPropagation();
        onAction?.();
      }}
      className={`h-8 rounded-lg flex items-center gap-1.5 border-none cursor-pointer text-[13px] whitespace-nowrap transition-colors ${
        label ? 'px-2' : 'w-8 justify-center'
      } ${
        active
          ? 'bg-accent text-foreground'
          : 'bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60'
      }`}
    >
      {children}
      {label && <span>{label}</span>}
      {chevron && <ChevronDown size={12} className="opacity-60" />}
    </button>
  );
}

function Divider() {
  return <div className="w-px h-4 bg-border mx-1 shrink-0" />;
}

// 「全景」按钮绑定的预设 key——后台「绘图预设设置」里的 720全景。代码只钉 key，
// 模型/占位文案/图片数约束全部跟着后台该预设的配置走。
const PANORAMA_PRESET_KEY = '720_panorama';
// 「人像质感调节」按钮绑定的预设 key——同全景：代码只钉 key，参数面板的
// 维度/档位全部来自后台该预设的 params 配置。
const PORTRAIT_PRESET_KEY = 'portrait_texture_adjust';
// 「九宫格」下拉里固定置顶的预设 key（多机位九宫格），其余按后台配置顺序排。
const GRID_FIRST_PRESET_KEY = 'multi_cam_9_panel';

export function ImageActionsBar({ nodeId, visible }: { nodeId: string; visible: boolean }) {
  // 只在「单选中本节点」时显示：多选通常是在框选拖动，一次冒出 N 条工具条
  // 只会互相叠罗汉（GenImageNode 的 prompt 面板没这个问题，它在节点正下方
  // 跟着各自节点走；工具条更宽、在节点上方，多选时重叠概率高得多）。
  const show = useCanvasStore(
    (s) => s.selectedIds.has(nodeId) && s.selectedIds.size === 1 && !s.readOnly,
  );
  // 下拉菜单开合（九宫格 / 宫格切分 两个；枚举保证同时最多开一个）。
  const [openMenu, setOpenMenu] = useState<'grid' | 'split' | null>(null);
  // 宫格切分模式（选完规格后：节点上叠网格逐格点选）。生命周期独立于工具条
  // 显隐（同 multiAnglePanel）：点格子的过程中节点可能失去单选态，不能因此
  // 把用户已选的格子清掉。
  const [splitMode, setSplitMode] = useState<GridSplitMode | null>(null);
  const [splitBusy, setSplitBusy] = useState(false);
  // 标注模式（对标 liblib「标注」）。生命周期同 splitMode：独立于工具条显隐，
  // 绘制过程中节点失去单选态不打断；状态全部在 AnnotateMode 内自持。
  const [annotateOpen, setAnnotateOpen] = useState(false);

  // 把「本节点在切分模式」镜像进 store：gen-image 的 prompt 面板（另一棵组件
  // 树）靠它让位，不然面板盖住网格。清理只清自己的标记——每个图片节点都挂着
  // 一条工具条，无条件清会把别的节点刚立起的标记抹掉。
  useEffect(() => {
    const { setGridSplittingNode } = useCanvasStore.getState();
    if (splitMode) setGridSplittingNode(nodeId);
    return () => {
      if (useCanvasStore.getState().gridSplittingNodeId === nodeId) setGridSplittingNode(null);
    };
  }, [splitMode, nodeId]);

  // 「本节点在标注模式」同款镜像：gen-image 的下方提示词面板靠它让位——
  // 标注条/绘制层叠在节点上时输入框还展开着（用户点名要隐藏）。
  useEffect(() => {
    const { setAnnotatingNode } = useCanvasStore.getState();
    if (annotateOpen) setAnnotatingNode(nodeId);
    return () => {
      if (useCanvasStore.getState().annotatingNodeId === nodeId) setAnnotatingNode(null);
    };
  }, [annotateOpen, nodeId]);
  const rootRef = useRef<HTMLDivElement>(null);
  const { message } = AntApp.useApp();
  const addNode = useCanvasStore((s) => s.addNode);
  const addConnection = useCanvasStore((s) => s.addConnection);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);

  // 与 GenImageNode 同 queryKey，画布上共享一份缓存，不多打请求。
  const presetsQuery = useQuery({
    queryKey: ['gen-image-presets'],
    queryFn: fetchGenImagePresets,
    staleTime: 300_000,
  });
  // 多角度编辑器配置（与弹框同 queryKey 共享缓存）；弹框由本组件托管，
  // AntD Modal portal 到 body，不受画布 zoom 影响。
  const multiAngleQuery = useQuery({
    queryKey: ['multi-angle-config'],
    queryFn: fetchMultiAngleConfig,
    staleTime: 300_000,
  });
  const [multiAngleOpen, setMultiAngleOpen] = useState(false);
  // 打光面板配置（与面板同 queryKey 共享缓存）；面板同样由本组件托管、挂节点下方。
  const lightQuery = useQuery({
    queryKey: ['light-control-config'],
    queryFn: fetchLightControlConfig,
    staleTime: 300_000,
  });
  const [lightOpen, setLightOpen] = useState(false);
  // 旋转与镜像：打开时存住解析好的图片 URL（非 null 即打开）。URL 在打开瞬间
  // 定格——预览/烘焙全程用同一张图，编辑中途上游换图不跟着变。
  const [rotateUrl, setRotateUrl] = useState<string | null>(null);
  const setDockPanelNodeId = useCanvasStore((s) => s.setDockPanelNodeId);

  // 下挂面板（多角度/打光）开合登记到 store：gen-image 节点靠它把自己的
  // 下方提示词面板让位隐藏（提示词面板更宽，边缘会从编辑器两侧露出来）。
  // 关闭/卸载只清自己登记的值，避免切换选中节点时旧实例误清新实例的登记。
  useEffect(() => {
    const open = multiAngleOpen || lightOpen || rotateUrl !== null;
    if (open) {
      setDockPanelNodeId(nodeId);
      return () => {
        if (useCanvasStore.getState().dockPanelNodeId === nodeId) setDockPanelNodeId(null);
      };
    }
    if (useCanvasStore.getState().dockPanelNodeId === nodeId) setDockPanelNodeId(null);
    return undefined;
  }, [multiAngleOpen, lightOpen, rotateUrl, nodeId, setDockPanelNodeId]);

  const handleMultiAngle = () => {
    const cfg = multiAngleQuery.data;
    if (!cfg) {
      message.warning('多角度配置加载中，请稍后再试');
      return;
    }
    if (!cfg.models[0]) {
      message.warning('后台未配置多角度模型');
      return;
    }
    // 编辑类面板互斥打开（下挂面板共用节点下方空间；旋转预览虽在右侧，
    // 同时开两个编辑器也只会互相干扰）。
    setLightOpen(false);
    setRotateUrl(null);
    setMultiAngleOpen(true);
  };

  const handleLight = () => {
    const cfg = lightQuery.data;
    if (!cfg) {
      message.warning('打光配置加载中，请稍后再试');
      return;
    }
    if (!cfg.models[0]) {
      message.warning('后台未配置打光模型');
      return;
    }
    setMultiAngleOpen(false);
    setRotateUrl(null);
    setLightOpen(true);
  };

  // 工具条统一的取图规则（旋转/切分/下载/放大共用）：自己的 content 优先，
  // 「上游引用」态（content 空、显示的是连线来的图）退回连线图。
  const resolveImageUrl = () => {
    const st = useCanvasStore.getState();
    const node = st.nodes.find((n) => n.id === nodeId);
    return node?.content || st.getConnectedInputs(nodeId).images[0] || '';
  };

  // 旋转与镜像：纯前端（CSS 预览 + canvas 烘焙 + 统一上传口），零后端、零积分。
  const handleRotate = () => {
    const url = resolveImageUrl();
    if (!url) {
      message.warning('当前节点没有可旋转的图片');
      return;
    }
    setMultiAngleOpen(false);
    setLightOpen(false);
    setRotateUrl(url);
  };

  // 下载：统一走 saveMediaToDisk（Tauri 里弹系统保存对话框、Rust 直接复制本地媒体；
  // <a download> 在 Tauri/macOS 下会被 WebView 直接取消，见 shared/saveToDisk.ts）。
  const handleDownload = () => {
    const url = resolveImageUrl();
    if (!url) {
      message.warning('当前节点没有可下载的图片');
      return;
    }
    void saveWithFeedback(() => saveMediaToDisk(url, `image-${Date.now()}.${extFromUrl(url, 'png')}`), message);
  };

  // 放大查看：走全局 Lightbox（与双击节点图片同一个弹框，自带下载/Esc 关闭）。
  // title 与各节点双击入口的文案保持一致。
  const handlePreview = () => {
    const url = resolveImageUrl();
    if (!url) {
      message.warning('当前节点没有可预览的图片');
      return;
    }
    const st = useCanvasStore.getState();
    const node = st.nodes.find((n) => n.id === nodeId);
    st.setLightbox({
      url,
      kind: 'image',
      title: node?.type === 'gen-image' ? 'AI 绘图' : '图片输入',
    });
  };

  // 按预设 key 在当前图片右侧铺一个 gen-image 下游节点并连线，预选该预设 +
  // 后台绑定模型（配了多个取第一个）。只铺节点预填参数，绝不自动提交生成——
  // 生成必须用户在新节点上手动点（生成动作不能替用户做）。
  // 调用方负责保证配置已加载、预设存在、模型已绑定。
  const spawnPresetNode = (presetKey: string) => {
    const cfg = presetsQuery.data;
    const modelId = cfg?.models[0];
    if (!modelId) return;
    const st = useCanvasStore.getState();
    const source = st.nodes.find((n) => n.id === nodeId);
    if (!source) return;
    // 连点多下每个新节点都落同一坐标会完全重叠。这里不用「发送到画布」的扑克
    // 牌堆叠——那是故意让副本像一手牌叠着；生成节点叠起来没法各自操作。改成
    // 竖向排队：数这个源的**所有** gen-image 下游（不分预设，全景和九宫格混着
    // 点也不互相压住），整节点高度往下错开。
    const size = DEFAULT_SIZES['gen-image'];
    const count = st.connections.filter((c) => {
      if (c.from !== nodeId) return false;
      const t = st.nodes.find((n) => n.id === c.to);
      return t?.type === 'gen-image';
    }).length;
    // addNode 以给定点为中心放置，加半个尺寸把「左上角」意图转成中心点输入。
    const newId = addNode('gen-image', {
      x: source.x + source.width + 160 + size.width / 2,
      y: source.y + count * (size.height + 60) + size.height / 2,
    });
    if (!newId) return;
    updateNodeSettings(newId, { modelId, promptPreset: presetKey });
    addConnection(nodeId, newId);
    // addNode/addConnection 走 op 通道自动落库，settings 补丁要显式整节点持久化
    //（同 pickPreset：低频动作不走 1.5s 防抖）。
    persistNodeById(newId);
  };

  const handlePortrait = () => {
    const cfg = presetsQuery.data;
    if (!cfg) {
      message.warning('预设配置加载中，请稍后再试');
      return;
    }
    if (!cfg.presets.some((p) => p.key === PORTRAIT_PRESET_KEY) || !cfg.models[0]) {
      message.warning('后台未配置 人像质感调节 预设');
      return;
    }
    spawnPresetNode(PORTRAIT_PRESET_KEY);
  };

  const handlePanorama = () => {
    const cfg = presetsQuery.data;
    if (!cfg) {
      message.warning('预设配置加载中，请稍后再试');
      return;
    }
    if (!cfg.presets.some((p) => p.key === PANORAMA_PRESET_KEY) || !cfg.models[0]) {
      message.warning('后台未配置 720全景 预设');
      return;
    }
    spawnPresetNode(PANORAMA_PRESET_KEY);
  };

  // 九宫格下拉的内容：后台绘图预设全量平铺（顺序 = 预设菜单同款：分组序 +
  // 组内 sort），multi_cam_9_panel 固定提到首位。
  const gridPresets = (() => {
    const cfg = presetsQuery.data;
    if (!cfg) return [];
    const flat = groupPresets(cfg.presets).flatMap((g) => g.items);
    const idx = flat.findIndex((p) => p.key === GRID_FIRST_PRESET_KEY);
    return idx <= 0 ? flat : [flat[idx], ...flat.slice(0, idx), ...flat.slice(idx + 1)];
  })();

  const handleGridToggle = () => {
    if (openMenu === 'grid') {
      setOpenMenu(null);
      return;
    }
    const cfg = presetsQuery.data;
    if (!cfg) {
      message.warning('预设配置加载中，请稍后再试');
      return;
    }
    if (!cfg.presets.length || !cfg.models[0]) {
      message.warning('后台未配置绘图预设');
      return;
    }
    setOpenMenu('grid');
  };

  const enterSplitMode = (rows: number, cols: number) => {
    setOpenMenu(null);
    setSplitMode({ rows, cols, selected: new Set() });
  };

  const toggleSplitCell = (key: string) => {
    setSplitMode((m) => {
      if (!m) return m;
      const selected = new Set(m.selected);
      if (selected.has(key)) selected.delete(key);
      else selected.add(key);
      return { ...m, selected };
    });
  };

  const handleSplitCreate = async () => {
    if (!splitMode || splitBusy) return;
    const url = resolveImageUrl();
    if (!url) {
      message.warning('当前节点没有可切分的图片');
      return;
    }
    setSplitBusy(true);
    try {
      const { created, failed } = await executeGridSplit({
        sourceId: nodeId,
        url,
        rows: splitMode.rows,
        cols: splitMode.cols,
        selected: splitMode.selected,
      });
      if (failed > 0) message.warning(`${failed} 个宫格上传失败，已在对应节点上标记`);
      else if (created > 0) message.success(`已切分出 ${created} 个图片节点`);
      // 成功才退出模式；失败（跨域等）留在模式里，用户不用重选格子。
      setSplitMode(null);
    } catch (err) {
      message.error(err instanceof Error ? err.message : '宫格切分失败');
    } finally {
      setSplitBusy(false);
    }
  };

  // 隐藏时顺手收起下拉，否则取消选中再选回来菜单还开着。
  useEffect(() => {
    if (!show || !visible) setOpenMenu(null);
  }, [show, visible]);

  // 点工具条外面任何地方都收起下拉。用 pointerdown 而不是 mousedown：工具条
  // 自己的按钮把 mousedown stopPropagation 了，但 pointerdown 仍会冒到 window，
  // 所以必须用 contains 判定「点在条内」而不是指望冒泡被拦。
  useEffect(() => {
    if (!openMenu) return;
    const close = (e: PointerEvent) => {
      if (rootRef.current?.contains(e.target as Node)) return;
      setOpenMenu(null);
    };
    window.addEventListener('pointerdown', close);
    return () => window.removeEventListener('pointerdown', close);
  }, [openMenu]);

  // 多角度面板：挂在节点正下方（对标 liblib——节点仍可见，面板贴下边缘展开）。
  // 定位与工具条同一套 inv-zoom 技巧（屏幕尺寸恒定、跟随节点），生命周期独立于
  // 工具条显隐：编辑期间节点失去单选态（工具条隐藏）也不打断。
  const multiAnglePanel = multiAngleOpen ? (
    <div
      className="absolute z-50"
      style={{
        top: 'calc(100% + 16px)',
        left: '50%',
        transform: 'scale(var(--inv-zoom, 1)) translate(-50%, 0)',
        transformOrigin: '0 0',
      }}
      onMouseDown={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
      onWheel={(e) => e.stopPropagation()}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      <MultiAnglePanel sourceNodeId={nodeId} onClose={() => setMultiAngleOpen(false)} />
    </div>
  ) : null;

  // 打光面板：与多角度同一套「节点正下方」锚点与生命周期（见上方注释）。
  const lightPanel = lightOpen ? (
    <div
      className="absolute z-50"
      style={{
        top: 'calc(100% + 16px)',
        left: '50%',
        transform: 'scale(var(--inv-zoom, 1)) translate(-50%, 0)',
        transformOrigin: '0 0',
      }}
      onMouseDown={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
      onWheel={(e) => e.stopPropagation()}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      <LightPanel sourceNodeId={nodeId} onClose={() => setLightOpen(false)} />
    </div>
  ) : null;

  // 旋转预览：源节点右侧的画布内幽灵 + 自带浮动工具条（见 RotatePreview 头注）。
  // 生命周期同 multiAnglePanel：独立于工具条显隐，编辑期间失去单选态不打断。
  const rotateUi = rotateUrl ? (
    <RotatePreview sourceNodeId={nodeId} url={rotateUrl} onClose={() => setRotateUrl(null)} />
  ) : null;

  // 切分模式 UI：模式工具条顶掉常规工具条（位置样式同款），网格 overlay 铺满
  // 节点。渲染不依赖 show——点格子过程中节点可能失去单选态，模式不能被打断。
  const splitUi = splitMode ? (
    <>
      <div
        className="absolute z-40"
        style={{
          bottom: 'calc(100% + 28px)',
          left: '50%',
          transform: 'scale(var(--inv-zoom, 1)) translate(-50%, -10px)',
          transformOrigin: '0 100%',
        }}
        onMouseDown={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onContextMenu={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
      >
        <div className="flex items-center gap-0.5 px-1.5 py-1 rounded-xl bg-card border border-border shadow-xl">
          <GridSplitModeBar
            mode={splitMode}
            busy={splitBusy}
            onClose={() => setSplitMode(null)}
            onCreate={handleSplitCreate}
          />
        </div>
      </div>
      <GridSplitOverlay mode={splitMode} onToggle={toggleSplitCell} />
    </>
  ) : null;

  if (annotateOpen) {
    return (
      <>
        <AnnotateMode nodeId={nodeId} onClose={() => setAnnotateOpen(false)} />
        {multiAnglePanel}
        {lightPanel}
      </>
    );
  }

  if (splitMode) {
    return (
      <>
        {splitUi}
        {multiAnglePanel}
        {lightPanel}
        {rotateUi}
      </>
    );
  }

  // 面板打开期间工具条整条隐藏（用户点名对标 liblib——编辑器展开时不留一排
  // 悬浮按钮叠在节点上方），关闭面板后工具条随选中态自然回来。打光/旋转同规则。
  if (!show || !visible || multiAngleOpen || lightOpen || rotateUrl)
    return (
      <>
        {multiAnglePanel}
        {lightPanel}
        {rotateUi}
      </>
    );

  return (
    <>
    <div
      className="absolute z-40"
      style={{
        bottom: 'calc(100% + 28px)',
        left: '50%',
        transform: 'scale(var(--inv-zoom, 1)) translate(-50%, -10px)',
        transformOrigin: '0 100%',
      }}
      onMouseDown={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
      onContextMenu={(e) => {
        // 工具条上的右键不该弹出节点菜单（NodeShell 在 onContextMenu 里接管）。
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      <div
        ref={rootRef}
        className="relative flex items-center gap-0.5 px-1.5 py-1 rounded-xl bg-card border border-border shadow-xl"
      >
        <BarButton label="人像质感调节" title="人像质感调节" onAction={handlePortrait}>
          <ScanFace size={iconSize} />
        </BarButton>
        <BarButton label="全景" title="生成720全景图" onAction={handlePanorama}>
          <Telescope size={iconSize} />
        </BarButton>
        <BarButton label="多角度" title="多角度编辑器" onAction={handleMultiAngle}>
          <Orbit size={iconSize} />
        </BarButton>
        <BarButton label="打光" title="打光效果" onAction={handleLight}>
          <Sun size={iconSize} />
        </BarButton>
        <Divider />
        {/* 九宫格下拉 = 后台「绘图预设设置」的预设列表（多机位九宫格置顶），
            点任意一项按该预设铺下游节点，行为与「全景」同款。 */}
        <div className="relative">
          <BarButton label="九宫格" chevron active={openMenu === 'grid'} onAction={handleGridToggle}>
            <Grid3x3 size={iconSize} />
          </BarButton>
          {openMenu === 'grid' && (
            <div className="absolute left-0 top-[calc(100%+10px)] w-44 max-h-80 overflow-y-auto custom-scrollbar rounded-2xl border border-solid border-white/25 bg-muted p-1 shadow-[0_12px_32px_rgba(0,0,0,0.55)] z-50">
              {gridPresets.map((p) => (
                <button
                  key={p.key}
                  type="button"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                  }}
                  onClick={(e) => {
                    e.stopPropagation();
                    setOpenMenu(null);
                    spawnPresetNode(p.key);
                  }}
                  className="w-full flex items-center gap-2.5 px-3 py-2 rounded-xl text-[13px] border-none cursor-pointer transition-colors bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60"
                >
                  <PresetIcon icon={p.value.icon} size={iconSize} className="shrink-0" />
                  <span className="truncate">{p.value.name}</span>
                </button>
              ))}
            </div>
          )}
        </div>
        {/* 宫格切分：选规格 → 节点上叠网格逐格点选 → 创建分镜组（对标 libtv）。
            切图纯前端（canvas 裁剪 + 走统一上传口）。 */}
        <div className="relative">
          <BarButton
            label="宫格切分"
            chevron
            active={openMenu === 'split'}
            onAction={() => setOpenMenu(openMenu === 'split' ? null : 'split')}
          >
            <Hash size={iconSize} />
          </BarButton>
          {openMenu === 'split' && <GridSplitMenu onPick={enterSplitMode} />}
        </div>
        <Divider />
        {/* 标注：图片上画笔/矩形/文字涂鸦，保存合成新图落 input-image 节点
            连回源（对标 liblib，纯前端合成）。 */}
        <BarButton title="标注" onAction={() => setAnnotateOpen(true)}>
          <Brush size={iconSize} />
        </BarButton>
        <BarButton title="旋转与镜像" onAction={handleRotate}>
          <FlipHorizontal2 size={iconSize} />
        </BarButton>
        <BarButton title="下载" onAction={handleDownload}>
          <Download size={iconSize} />
        </BarButton>
        <BarButton title="放大查看" onAction={handlePreview}>
          <Maximize2 size={iconSize} />
        </BarButton>
      </div>
    </div>
    {multiAnglePanel}
    {lightPanel}
    {rotateUi}
    </>
  );
}
