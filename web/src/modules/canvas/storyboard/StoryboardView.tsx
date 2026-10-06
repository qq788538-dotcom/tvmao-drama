// 故事板布局壳。整个视图只有一个状态：focused。
//
// 布局的不对称是有原因的：音频和文本本来就住在左栏，点它们不用搬家；
// 图片和视频住在中栏和右栏，而中右要让位给详情面板，所以它们的列表搬到
// 左栏下半。左栏下半因此是个「当前聚焦类型的列表」槽位。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ChevronDown, ChevronUp, Check } from 'lucide-react';
import { groupByZone, ZONE_LABELS, type StoryboardZone } from './zones';
import { ZoneList } from './ZoneList';
import { AudioGrid } from './AudioGrid';
import { StoryboardDetail } from './StoryboardDetail';
import { useCanvasStore } from '../editor/store';

interface Focused {
  zone: StoryboardZone;
  nodeId: string;
}

/** 左下槽位能切换的三种类型。音频不在内——它是左栏上半独立固定的一条。 */
const SWITCHABLE_ZONES: StoryboardZone[] = ['text', 'image', 'video'];

/**
 * 标题旁的类型切换器：`⌄` 展开一个菜单，选中项打勾。
 * 只有左下那个槽位用它，其余分区的标题是纯文字。
 */
function ZoneSwitcher({
  current,
  onPick,
}: {
  current: StoryboardZone;
  onPick: (zone: StoryboardZone) => void;
}) {
  const [open, setOpen] = useState(false);
  const boxRef = useRef<HTMLDivElement | null>(null);

  // 点菜单外面就收起。挂在 document 上而不是给遮罩层——遮罩会吃掉那一次点击，
  // 用户想直接点到别处得点两下。
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
        aria-label="切换类型"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex items-center justify-center w-6 h-6 rounded bg-transparent border-none cursor-pointer text-sidebar-foreground hover:text-foreground hover:bg-accent"
      >
        {open ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
      </button>

      {open && (
        <div className="absolute left-0 top-7 z-50 min-w-[120px] py-1 rounded-lg bg-popover border border-border shadow-xl">
          {SWITCHABLE_ZONES.map((z) => (
            <button
              key={z}
              type="button"
              onClick={() => {
                onPick(z);
                setOpen(false);
              }}
              className="w-full flex items-center justify-between gap-4 px-3 py-1.5 text-left bg-transparent border-none cursor-pointer text-sm text-popover-foreground hover:bg-accent"
            >
              {ZONE_LABELS[z]}
              {z === current && <Check size={14} />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function ZonePanel({
  zone,
  children,
  fixedHeight,
  switcher,
}: {
  zone: StoryboardZone;
  children: React.ReactNode;
  /** 传了就按内容高度收缩（音频那条用），否则平分剩余高度。 */
  fixedHeight?: boolean;
  switcher?: React.ReactNode;
}) {
  // 分区是独立的圆角面板：#262626 的背景压在更暗的页面底色(#141414)上，
  // 靠这一层深浅差 + 圆角 + 边框把三个模块分开。
  //
  // 曾经试过反过来做——去掉圆角边框背景、页面和面板同一个色、只留 1px 线，
  // 结果是"模块之间根本没分开"：没有色差的话那条线在暗色下几乎看不见。
  // 分隔靠的是**底色对比**，不是描边。
  return (
    <div
      data-testid={`zone-${zone}`}
      className={`${
        fixedHeight ? 'shrink-0' : 'flex-1 min-h-0'
      } flex flex-col rounded-lg border border-border bg-[#262626] overflow-hidden`}
    >
      <div className="px-3 py-2 flex items-center gap-1 shrink-0">
        <span className="text-foreground text-sm">{ZONE_LABELS[zone]}</span>
        {switcher}
      </div>
      {children}
    </div>
  );
}

export function StoryboardView() {
  const nodes = useCanvasStore((s) => s.nodes);
  const grouped = useMemo(() => groupByZone(nodes), [nodes]);
  const [focused, setFocused] = useState<Focused | null>(null);

  // 左下槽位的手动覆盖。null = 跟随 focused（默认行为）。
  // 点任何一张卡片都会清掉它——「默认跟随」优先于上一次的手动选择，否则
  // 用户点了图片却发现左下还停在他上次手动选的文本上，会以为点击没生效。
  const [lowerOverride, setLowerOverride] = useState<StoryboardZone | null>(null);

  // 关闭详情必须同时清掉手动覆盖，否则左下会停在用户切过去的那个类型上：
  // focused 变 null 后跟随值算出来是「文本」，但 lowerOverride 还压着「视频」。
  // ‹ 返回、✕ 关闭、以及节点被删掉时的自动收敛，三条路都走这里。
  const closeDetail = useCallback(() => {
    setFocused(null);
    setLowerOverride(null);
  }, []);

  // 聚焦的节点可能被删掉（故事板里删、或画布那边删）。故事板不持有数据副本，
  // 所以这里不是"同步"，是让本地的 focused 指针自己收敛——否则详情面板会
  // 卡在一个已经不存在的节点上。
  useEffect(() => {
    if (focused && !nodes.some((n) => n.id === focused.nodeId)) {
      closeDetail();
    }
    // closeDetail 是稳定的 useCallback，不进依赖也不会读到旧值
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodes, focused]);

  const select = (zone: StoryboardZone) => (nodeId: string) => {
    setFocused({ zone, nodeId });
    setLowerOverride(null);
    // 同步设成画布的选中项。这不是"顺手做个联动"，是详情面板里参数区能不能
    // 用的前提：生成节点的参数面板（模型/比例/提示词/生成按钮）用
    // `opacity: isSelected ? 1 : 0` + `pointerEvents: isSelected ? 'auto' : 'none'`
    // 控制显隐，而 isSelected 是节点自己从 store 的 selectedIds 里读的
    // （见 GenImageNode.tsx）。不设选中的话参数区照样渲染，只是全透明且点不着
    // ——用户看到的就是"只有一张图，没有参数"。
    //
    // 副作用是切回画布时这个节点是选中态，这恰恰是想要的：两个视图共享同一份
    // 数据，选中态也该是同一个。
    useCanvasStore.getState().selectNode(nodeId, false);
  };

  // 没有音频就整块不渲染，而不是显示一个「暂无音频」的空面板——那块空壳
  // 白占左栏顶部一截，而它能提供的信息（这个项目没有音频）从"它不在"本身
  // 就看得出来。图片/视频不同：它们是主内容区，空着也得留位置。
  const audioPanel =
    grouped.audio.length > 0 ? (
      // fixedHeight：音频这栏按内容收缩（一行卡片起，最多三行），左栏剩下的
      // 高度全给下面的槽位。不这么做的话它会跟下面平分左栏，一个音频也占半屏。
      <ZonePanel zone="audio" fixedHeight>
        <AudioGrid
          nodes={grouped.audio}
          focusedId={focused?.zone === 'audio' ? focused.nodeId : null}
          onSelect={select('audio')}
        />
      </ZonePanel>
    ) : null;

  // 左栏下半的槽位：默认是文本，点了图片/视频后跟随过去；用户也能用标题旁的
  // 下拉手动切成三者中任意一个（手动优先，直到下次点卡片重新跟随）。
  const followedZone: StoryboardZone =
    focused && (focused.zone === 'image' || focused.zone === 'video') ? focused.zone : 'text';
  const lowerZone: StoryboardZone = lowerOverride ?? followedZone;

  const lowerPanel = (
    <ZonePanel
      zone={lowerZone}
      // 切换器只在详情态给：默认态三栏已经把文本/图片/视频都摆出来了，
      // 这时左下再切成视频，屏幕上就有两个视频列表。切换的价值只存在于
      // 「中右被详情面板占了、左下只能显示一个类型」的时候。
      switcher={focused ? <ZoneSwitcher current={lowerZone} onPick={setLowerOverride} /> : undefined}
    >
      {/* key 强制类型切换时重挂载：ZoneList 内部的虚拟化滚动位置挂在 DOM 节点上，
          复用实例会把上一个类型的 scrollTop 带过来——从长列表切到短列表会算出
          超界的可视区，显示空白。这个 key 不是给 map 用的，别当冗余删掉。 */}
      <ZoneList
        key={lowerZone}
        nodes={grouped[lowerZone]}
        focusedId={focused?.zone === lowerZone ? focused.nodeId : null}
        onSelect={select(lowerZone)}
        emptyText={`暂无${ZONE_LABELS[lowerZone]}`}
      />
    </ZonePanel>
  );

  // 模块之间靠 gap 拉开——间隙里露出的是更暗的页面底色(#141414)，那道暗缝
  // 才是"分隔开"的实际来源。用 border 描边代替 gap 试过，暗色下线几乎看不见。
  return (
    // pt-14 给浮动顶栏让位：覆盖层现在铺满整个视口（否则顶栏两侧会漏出画布），
    // 所以内容得自己避开顶栏那 56px，不然三栏会被顶栏压住。
    <div className="w-full h-full flex gap-2 p-2 pt-14">
      <div className="w-[360px] shrink-0 flex flex-col gap-2">
        {audioPanel}
        {lowerPanel}
      </div>

      {focused ? (
        // 详情态：中栏和右栏合并成一个面板。
        <div data-testid="storyboard-detail" className="flex-1 min-w-0">
          <StoryboardDetail nodeId={focused.nodeId} onClose={closeDetail} />
        </div>
      ) : (
        <>
          <ZonePanel zone="image">
            <ZoneList
              nodes={grouped.image}
              focusedId={null}
              onSelect={select('image')}
              emptyText="暂无图片"
            />
          </ZonePanel>
          <ZonePanel zone="video">
            <ZoneList
              nodes={grouped.video}
              focusedId={null}
              onSelect={select('video')}
              emptyText="暂无视频"
            />
          </ZonePanel>
        </>
      )}
    </div>
  );
}
