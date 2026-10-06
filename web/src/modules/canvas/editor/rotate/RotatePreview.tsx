// 旋转与镜像的交互侧（对标 liblib「旋转」）：点工具条按钮后在源节点右侧出现
// 一个**画布内预览幽灵**——看起来是个节点（跟随源节点移动、随画布缩放），但
// 只是本组件的本地 DOM，不进 store：真节点经 addNode 会立即广播持久化 op，
// 临时预览要跟持久化/撤销/协同三方对抗，幽灵方案把这些全绕开；「保存」才创建
// 真节点（addLinkedInputImageNodes 自带连线）并把烘焙图走统一上传口写回，
// 「取消」只是卸载本组件，画布无痕。
//
// 预览用 CSS transform 旋转 <img>，不重绘 canvas——烘焙只在保存时做一次
// （bakeRotate.ts，变换顺序两边钉死一致）。
import { useEffect, useState } from 'react';
import { App as AntApp } from 'antd';
import { FlipHorizontal2, FlipVertical2, RotateCw, X } from 'lucide-react';
import { useCanvasStore } from '../store';
import { uploadFileIntoNode } from '../utils/uploadIntoNode';
import { bakeRotateMirror, loadImage } from './bakeRotate';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';
import { clampAngle, ROTATE_OFFSET_X, rotatedBox, rotatedNodeSize } from './rotateMath';

// 角度符号（∠+弧线）。lucide 没有对应图标，对标图里角度框前就是这个符号。
function AngleIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" className="shrink-0 text-muted-foreground">
      <path d="M3 13 L13 13 M3 13 L11 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
      <path d="M8.5 13 A 5.5 5.5 0 0 0 7 9.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" fill="none" />
    </svg>
  );
}

export function RotatePreview({
  sourceNodeId,
  url,
  onClose,
}: {
  sourceNodeId: string;
  url: string;
  onClose: () => void;
}) {
  const { message } = AntApp.useApp();
  const [angle, setAngle] = useState(0);
  // 角度输入框的原始文本单独存：受控 number 输入没法表达「正在清空重打」的
  // 中间态（空串会被立刻钳成 0，用户删不掉那个 0）。失焦/回车时才钳格落定。
  const [angleText, setAngleText] = useState('0');
  const [flipH, setFlipH] = useState(false);
  const [flipV, setFlipV] = useState(false);
  const [saving, setSaving] = useState(false);
  // 源节点的显示尺寸决定预览幽灵的世界尺寸；源节点被删时组件随节点子树卸载，
  // 这里不用防御 undefined 之外的情况。
  const source = useCanvasStore((s) => s.nodes.find((n) => n.id === sourceNodeId));

  // Esc 取消，对标 liblib；保存中不响应（避免上传途中卸载留下半截状态）。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !saving) onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose, saving]);

  if (!source) return null;

  const box = rotatedBox(source.width, source.height, angle);
  const dirty = angle % 360 !== 0 || flipH || flipV;

  const commitAngleText = () => {
    const next = clampAngle(Number(angleText));
    setAngle(next);
    setAngleText(String(next));
  };

  const rotate90 = () => {
    const next = (angle + 90) % 360;
    setAngle(next);
    setAngleText(String(next));
  };

  const handleSave = async () => {
    if (saving || !dirty) return;
    setSaving(true);
    try {
      const img = await loadImage(url);
      const file = await bakeRotateMirror(img, angle, flipH, flipV);
      const baked = rotatedBox(img.naturalWidth, img.naturalHeight, angle);
      const size = rotatedNodeSize(baked.width / baked.height);
      const st = useCanvasStore.getState();
      const src = st.nodes.find((n) => n.id === sourceNodeId);
      if (!src) throw new Error('源节点不存在');
      const ids = st.addLinkedInputImageNodes(sourceNodeId, [
        { x: src.x + src.width + ROTATE_OFFSET_X, y: src.y, ...size },
      ]);
      if (ids.length === 0) throw new Error('创建节点失败');
      // 上传失败已由 uploadFileIntoNode 落在节点 status/error 上，这里不拦截
      // 关闭——节点留在画布上带失败态，用户在节点上重试，与宫格切分同语义。
      await uploadFileIntoNode(ids[0], file, { autoFittedFor: undefined });
      onClose();
    } catch (err) {
      message.error(err instanceof Error ? err.message : '旋转保存失败');
      setSaving(false);
    }
  };

  const stop = {
    onMouseDown: (e: React.MouseEvent) => e.stopPropagation(),
    onDoubleClick: (e: React.MouseEvent) => e.stopPropagation(),
    onWheel: (e: React.WheelEvent) => e.stopPropagation(),
    onContextMenu: (e: React.MouseEvent) => {
      e.preventDefault();
      e.stopPropagation();
    },
  };

  // 幽灵连线的曲线参数逐字复刻 ConnectionLayer（dx 公式、发光底描边、端点
  // 圆），配色取它的 selected 态（#60a5fa）——保存后真连线会以同样视觉接替，
  // 用户看不出「幽灵换真身」的瞬间。坐标系以幽灵盒左上角为原点：源节点右缘
  // 中点在 (-OFFSET, source.height/2)，幽灵左缘中点在 (0, box.height/2)。
  const edgeStartY = source.height / 2;
  const edgeEndY = box.height / 2;
  const edgeDx = Math.max(52, Math.min(180, ROTATE_OFFSET_X * 0.42));
  const edgePath = `M ${-ROTATE_OFFSET_X} ${edgeStartY} C ${-ROTATE_OFFSET_X + edgeDx} ${edgeStartY}, ${-edgeDx} ${edgeEndY}, 0 ${edgeEndY}`;

  // 四角把手：屏幕尺寸恒定（inv-zoom），中心钉在幽灵盒四角上。
  const handleCorners: Array<{ left: string; top: string }> = [
    { left: '0%', top: '0%' },
    { left: '100%', top: '0%' },
    { left: '0%', top: '100%' },
    { left: '100%', top: '100%' },
  ];

  return (
    // 幽灵盒：绝对定位在源节点右侧（节点子树内 1px = 1 世界单位，随画布
    // 缩放/平移与源节点保持相对位置）。盒尺寸 = 旋转后的外接矩形，图片按源
    // 节点尺寸居中后 CSS 旋转/镜像，正好内切。
    <div
      className="absolute z-40"
      style={{ left: `calc(100% + ${ROTATE_OFFSET_X}px)`, top: 0, width: box.width, height: box.height }}
      {...stop}
    >
      {/* 幽灵连线：源节点右缘中点 → 幽灵左缘中点。 */}
      <svg className="absolute overflow-visible pointer-events-none" style={{ left: 0, top: 0, width: 1, height: 1 }}>
        <path d={edgePath} stroke="#09090b" strokeWidth={6} strokeOpacity={0.9} fill="none" />
        <path d={edgePath} stroke="#60a5fa" strokeWidth={2.5} strokeOpacity={0.92} fill="none" />
        <circle cx={-ROTATE_OFFSET_X} cy={edgeStartY} r={4.5} fill="#60a5fa" stroke="#09090b" strokeWidth={2} />
        <circle cx={0} cy={edgeEndY} r={4.5} fill="#60a5fa" stroke="#09090b" strokeWidth={2} />
      </svg>
      {/* 名字 chip：视觉对标真节点的标题（世界单位，随 zoom 缩放）。 */}
      <div className="absolute -top-6 left-0 text-xs text-muted-foreground whitespace-nowrap">
        旋转与镜像
      </div>
      <div className="relative h-full w-full overflow-visible rounded-lg outline outline-2 outline-sky-400">
        <img
          src={url}
          crossOrigin={mediaCrossOrigin(url)}
          alt="旋转预览"
          className="absolute left-1/2 top-1/2 max-w-none select-none"
          draggable={false}
          style={{
            width: source.width,
            height: source.height,
            // 顺序必须与 bakeRotateMirror 一致（CSS 右侧先作用 = 先镜像后旋转）。
            transform: `translate(-50%, -50%) rotate(${angle}deg) scale(${flipH ? -1 : 1}, ${flipV ? -1 : 1})`,
          }}
        />
      </div>
      {/* 四角选中把手（对标 liblib 的节点选中态；纯装饰，不可拖拽 resize）。 */}
      {handleCorners.map((c) => (
        <div
          key={`${c.left}-${c.top}`}
          className="absolute h-2.5 w-2.5 rounded-[2px] border-2 border-solid border-sky-400 bg-white"
          style={{
            left: c.left,
            top: c.top,
            transform: 'translate(-50%, -50%) scale(var(--inv-zoom, 1))',
          }}
        />
      ))}
      {/* 浮动工具条：预览盒上方，屏幕尺寸恒定（同 ImageActionsBar 的 inv-zoom
          技巧）。布局对标 liblib：✕标题 │ ∠角度框° │ 顺90/水平/垂直 │ 白色保存。 */}
      <div
        className="absolute z-50"
        style={{
          bottom: 'calc(100% + 28px)',
          left: '50%',
          transform: 'scale(var(--inv-zoom, 1)) translate(-50%, -10px)',
          transformOrigin: '0 100%',
        }}
      >
        <div className="flex items-center gap-1.5 px-2.5 py-2 rounded-2xl bg-card border border-border shadow-xl">
          <button
            type="button"
            title="取消"
            onClick={onClose}
            disabled={saving}
            className="h-9 rounded-lg px-2.5 flex items-center gap-2 border-none cursor-pointer text-sm whitespace-nowrap bg-transparent text-foreground hover:bg-accent/60 disabled:opacity-40"
          >
            <X size={16} />
            旋转与镜像
          </button>
          <div className="w-px h-5 bg-border mx-0.5 shrink-0" />
          <div className="flex items-center gap-1.5 h-9 px-2.5 rounded-lg bg-background/60 border border-border">
            <AngleIcon />
            <input
              value={angleText}
              onChange={(e) => setAngleText(e.target.value.replace(/[^\d]/g, ''))}
              onBlur={commitAngleText}
              onKeyDown={(e) => {
                if (e.key === 'Enter') commitAngleText();
                // 别让 Backspace/Delete 冒泡成画布删节点。
                e.stopPropagation();
              }}
              title="旋转角度（0–360 整数）"
              className="w-9 bg-transparent border-none text-center text-sm text-foreground outline-none p-0"
            />
            <span className="text-sm text-muted-foreground">°</span>
          </div>
          <button
            type="button"
            title="顺时针旋转 90°"
            onClick={rotate90}
            className="h-9 w-9 rounded-lg flex items-center justify-center border-none cursor-pointer bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60"
          >
            <RotateCw size={16} />
          </button>
          <button
            type="button"
            title="水平镜像"
            onClick={() => setFlipH((v) => !v)}
            className={`h-9 w-9 rounded-lg flex items-center justify-center border-none cursor-pointer transition-colors ${
              flipH
                ? 'bg-accent text-foreground'
                : 'bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60'
            }`}
          >
            <FlipHorizontal2 size={16} />
          </button>
          <button
            type="button"
            title="垂直镜像"
            onClick={() => setFlipV((v) => !v)}
            className={`h-9 w-9 rounded-lg flex items-center justify-center border-none cursor-pointer transition-colors ${
              flipV
                ? 'bg-accent text-foreground'
                : 'bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60'
            }`}
          >
            <FlipVertical2 size={16} />
          </button>
          <button
            type="button"
            disabled={saving || !dirty}
            onClick={handleSave}
            className="h-9 px-4 ml-0.5 rounded-lg border-none cursor-pointer text-sm whitespace-nowrap bg-white text-black hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {saving ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </div>
  );
}
