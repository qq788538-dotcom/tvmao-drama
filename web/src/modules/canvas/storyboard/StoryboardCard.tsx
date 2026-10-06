// 故事板分区列表里的卡片。刻意**不**复用节点组件——节点组件带着整个参数区，
// 塞进列表既撑爆高度又毫无意义（列表是用来挑的，不是用来改的）。
// 显示名走 NodeShell 导出的 displayLabelFor，保证跟画布上的抬头一字不差。
//
// 四个分区的 node.content 语义完全不同，媒体区必须按分区分叉渲染，不能
// 无脑当图片 URL 塞进 <img>：视频区是 mp4 URL、音频区是音频 URL、文本区
// 干脆不写 content（文本在 settings.text）。同款分叉先例见
// panels/HistoryPanel.tsx 的 isAudio/isVideo/else 三叉。
//
// 文本和音频这两个分区连卡片外形都不一样（文本=图标+标题一行，音频=竖方块），
// 所以它们不走「标题 + 媒体区 + 模型行」这套结构：文本在本组件顶部就早退，
// 音频整个走 AudioGrid。
import { FileText, Music } from 'lucide-react';
import { useThumbUrl } from '../editor/thumbs';
import { displayLabelFor } from '../editor/NodeShell';
import { zoneOf } from './zones';
import { beginNodeDrag, endNodeDrag, setDragNodeId } from './dragConnect';
import type { CanvasNode } from '../editor/types';
import { mediaCrossOrigin } from '../editor/utils/mediaCrossOrigin';

const STATUS_TEXT: Partial<Record<NonNullable<CanvasNode['status']>, string>> = {
  generating: '生成中',
  uploading: '上传中',
  failed: '失败',
};

/**
 * 媒体的尺寸上限：**最宽 287、最高 161**（287 的 16:9 高度）。
 *
 * 287 是对标站实测值（用户在真实站点上量的）。不要照着截图估这个数——
 * 截图有缩放，曾经按截图估成 510，比实际大了近一倍，右边该留的白全没了。
 *
 * 注意这是**上限**不是固定框：媒体按自己的原始比例缩放到能放进这个上限，
 * 容器随之收缩。所以 16:9 素材是 287×161（宽高都顶满），4:3 是 215×161
 * （高度顶满、宽度变窄），竖屏 9:16 是 91×161。**不变形、不裁剪、不留黑边。**
 *
 * 曾经这里是固定 `aspect-video` 的框 + object-contain：框永远 287×161，
 * 非 16:9 素材缩在中间、两侧留一大片背景色。视觉上那片留白跟素材本身混在
 * 一起，看不出图到底多大。
 */
const MEDIA_MAX = 'max-w-[287px] max-h-[161px]';

/**
 * 媒体行的固定高度。**这是布局稳定的关键，不是随便定的数字。**
 *
 * 在 max-w-287/max-h-161 的约束下，只要素材不比 16:9 更宽，它的最终高度就
 * 一定是 161（高度先顶满，宽度按比例收窄）：16:9 是 287×161，4:3 是 215×161，
 * 竖屏 9:16 是 91×161。所以把行高钉死在 161，媒体加载前后**高度完全一致**。
 *
 * 为什么必须这样：`useThumbUrl` 在缩略图换取完成前返回 undefined（见
 * editor/thumbs.ts 的注释——此时给 src 会让浏览器先下原图再下缩略图，流量
 * 翻倍），于是 <img> 没有 src、高度为 0，整列条目塌成一条挤在一起。更糟的是
 * 虚拟列表按**实测高度**给条目定位，高度从 0 变 161 会让后面所有条目位移、
 * 滚动位置漂移——图陆续加载的过程中列表会一直跳。
 *
 * 唯一的边界：比 16:9 更宽的素材（如 21:9）加载后高度不足 161，上下各留一点空。
 * 拿几像素留白换全程零跳动，值。
 *
 * 不用画布节点自己的 width/height 来推比例：那是节点框的尺寸，用户能拖 resize
 * 把手改，跟产物比例没有可靠关系。
 */
const MEDIA_ROW_H = 'h-[161px]';

/**
 * 待生成占位：没有产物就没有比例可循，只能用固定的 287×161 框。
 * 这也是对标站的做法（它的「待确认后生成」也是一个 16:9 的方块）。
 */
function PendingPlaceholder() {
  return (
    <div className="w-[287px] h-[161px] rounded-md bg-card flex items-center justify-center">
      <span className="text-muted-foreground text-[11px]">待生成</span>
    </div>
  );
}

/** 媒体区：按分区分叉，不对所有节点都假设 content 是图片 URL。 */
function MediaPreview({ node, title }: { node: CanvasNode; title: string }) {
  const zone = zoneOf(node);

  if (zone === 'video') {
    // 不加 controls：列表是用来挑的，点卡片进详情面板才播放；一排播放器
    // 既挤又会让点击落到控件上而不是选中卡片。靠 preload="metadata" 出首帧当封面。
    return node.content ? (
      <video
        src={node.content} crossOrigin={mediaCrossOrigin(node.content)}
        muted
        playsInline
        preload="metadata"
        className={`${MEDIA_ROW_H} max-w-[287px] w-auto rounded-md bg-card`}
      />
    ) : (
      <PendingPlaceholder />
    );
  }

  if (zone === 'audio') {
    // 正常路径下音频不走这个组件（音频分区用 AudioGrid 的竖方块卡片）。
    // 这个分支是防御：MediaPreview 按 zone 分叉，缺一档就意味着音频节点
    // 会掉进下面的 image 分支，把音频 URL 塞进 <img> 破图。
    return (
      <div className="w-full h-16 rounded-md bg-card flex items-center gap-2 px-3 text-muted-foreground text-xs">
        <Music size={16} className="shrink-0" />
        <span className="truncate">{title}</span>
      </div>
    );
  }

  // image，或（理论上不会出现在故事板里的）未知分区。
  return node.content ? <ImageMedia src={node.content} alt={title} /> : <PendingPlaceholder />;
}

/**
 * 图片媒体：缩略图换取期间显示同高度的骨架块，换到了再渲染 img。
 *
 * 这里直接用 useThumbUrl 而不是 ThumbImg 组件，就是为了拿到「还没换到」这个
 * 中间态——ThumbImg 在该态下渲染一个无 src 的 <img>（高度 0），列表会塌。
 * 骨架宽度取 287（上限），图到了之后按原始比例收窄；**高度两态都是 161**，
 * 所以只有水平方向变化，虚拟列表的垂直定位全程不动。
 */
function ImageMedia({ src, alt }: { src: string; alt: string }) {
  const thumb = useThumbUrl(src);
  return (
    <div className={`${MEDIA_ROW_H} flex items-center`}>
      {thumb ? (
        <img
          src={thumb}
          crossOrigin={mediaCrossOrigin(thumb)}
          alt={alt}
          loading="lazy"
          decoding="async"
          className={`${MEDIA_MAX} w-auto h-auto rounded-md`}
        />
      ) : (
        <div className="w-[287px] h-[161px] rounded-md bg-card" />
      )}
    </div>
  );
}

export function StoryboardCard({
  node,
  selected,
  onSelect,
}: {
  node: CanvasNode;
  selected: boolean;
  onSelect: (nodeId: string) => void;
}) {
  const zone = zoneOf(node);
  const title = displayLabelFor(node);
  const modelId = (node.settings?.modelId as string | undefined) ?? '';
  const statusText = node.status ? STATUS_TEXT[node.status] : undefined;

  // 文本条目是「图标 + 标题」的一行，不套「标题 + 内容区」那套结构。
  //
  // 曾经这里跟图片/视频一样：上面一行灰色小标题，下面一个大方块显示正文摘要。
  // 结果一条文本就占掉图片卡片那么高，四五条文本把整栏塞满，而摘要那几十个字
  // 既读不出内容也认不出是哪条——列表是用来挑的，挑文本靠的是标题不是正文。
  // 标题走 displayLabelFor，即用户在画布上给节点起的名字（settings.name），
  // 没起名才回落到类型默认标签。
  if (zone === 'text') {
    return (
      <button
        type="button"
        aria-pressed={selected}
        onClick={() => onSelect(node.id)}
        className={`w-full text-left px-2 py-2 rounded-lg bg-transparent cursor-pointer flex items-center gap-3 ${
          selected ? 'border border-primary' : 'border border-transparent hover:border-border'
        }`}
      >
        <span className="shrink-0 w-10 h-10 rounded-lg bg-card flex items-center justify-center">
          <FileText size={16} className="text-muted-foreground" />
        </span>
        <span className="min-w-0 flex-1 truncate text-foreground text-sm" title={title}>
          {title}
        </span>
      </button>
    );
  }

  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={() => onSelect(node.id)}
      // 图片/视频卡片可拖到详情面板的参考区建连线（用户拍板：来源只限左侧
      // 列表，文本/音频不参与）。draggable 挂在整张卡片上而不是缩略图上——
      // 拖拽目标是"这个节点"，抓哪儿都该能拖。
      draggable
      onDragStart={(e) => {
        setDragNodeId(e.dataTransfer, node.id);
        // 广播「正在拖」：参数面板靠它在拖拽全程显示灰虚线 + 提示文字，
        // 而不是等拖到面板上方才有反应。
        beginNodeDrag();
      }}
      onDragEnd={endNodeDrag}
      className={`w-full text-left p-2 rounded-lg bg-transparent cursor-pointer flex flex-col gap-1.5 ${
        selected ? 'border border-primary' : 'border border-transparent hover:border-border'
      }`}
    >
      <div className="text-sidebar-foreground text-xs truncate" title={title}>
        {title}
      </div>

      <MediaPreview node={node} title={title} />

      {(modelId || statusText) && (
        <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          {modelId && <span className="truncate">{modelId}</span>}
          {statusText && <span>{statusText}</span>}
        </div>
      )}
    </button>
  );
}
