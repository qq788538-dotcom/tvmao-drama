import { useRef } from 'react';
import { ChevronDown, ChevronLeft, ChevronRight, ChevronUp } from 'lucide-react';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';

export interface MultiAngleSphereProps {
  /** 水平环绕角 0~359，向右为正 */
  rotation: number;
  /** 垂直俯仰角 -90~90，正=俯拍 */
  tilt: number;
  /** 景别：中央图尺寸随之缩放（全景<中景<特写，对标站行为），缺省=中景 */
  zoom?: 'closeup' | 'medium' | 'panorama';
  /** 中央展示的原图缩略 URL（可空，空时显示灰色占位块） */
  imageUrl?: string;
  /** 用户通过箭头步进或拖拽球面改角度时回调（值已归一/钳制） */
  onChange: (next: { rotation: number; tilt: number }) => void;
}

/** 景别 → 中央图边长：拉近景别图变大（全景<中景<特写）。 */
const ZOOM_IMAGE_SIZE: Record<'closeup' | 'medium' | 'panorama', number> = {
  panorama: 62,
  medium: 88,
  closeup: 114,
};

/** rotation 归一到 [0, 360)——负值也要落回正区间，所以先取模再补 360 再取模。 */
function normalizeRotation(r: number): number {
  return ((r % 360) + 360) % 360;
}

function clampTilt(t: number): number {
  return Math.min(90, Math.max(-90, t));
}

/** 球壳线框半径。比容器半径明显小——四向箭头要放在球外留白处，不压线框
 *（对标站的球体在容器里有呼吸边距，箭头是无底纯图标）。 */
const SHELL_R = 86;
/** 相机指示器的投影半径——略小于球壳，让指示器贴着线框走 */
const CAM_R = 82;
/** 每 px 拖拽换算的度数 */
const DRAG_DEG_PER_PX = 0.8;
/** 箭头单次步进度数 */
const STEP = 15;

/** 纬线放置高度（球心为 0，成对 ±h）；直径按 sqrt(R²-h²) 收缩 */
const LATITUDE_HEIGHTS = [0, 28, 54, 74];

/**
 * 多角度编辑器的球体角度可视化控件（对标 liblib.tv 逆向结构：纯 CSS 3D）。
 * 球壳整体随 rotation/tilt 转动，相机指示器用正交投影贴在容器平面上。
 */
export function MultiAngleSphere({ rotation, tilt, zoom = 'medium', imageUrl, onChange }: MultiAngleSphereProps) {
  const imageSize = ZOOM_IMAGE_SIZE[zoom] ?? ZOOM_IMAGE_SIZE.medium;
  // 拖拽基线：pointerdown 时快照起点坐标与当时的角度，move 全程只做
  // 「起点 + 位移」的纯函数换算——不依赖父组件受控值的回流时序，拖快了也不抖。
  const dragRef = useRef<{
    pointerId: number;
    startX: number;
    startY: number;
    startRotation: number;
    startTilt: number;
  } | null>(null);

  const emit = (nextRotation: number, nextTilt: number) => {
    // 拖拽按 0.8°/px 换算会出一串浮点（2.7406250000000227°），角度全程取整——
    // 显示、提交、服务端拼词都只认整数度。
    onChange({
      rotation: normalizeRotation(Math.round(nextRotation)),
      tilt: clampTilt(Math.round(nextTilt)),
    });
  };

  const handlePointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    // 控件会被放进画布弹框——冒泡上去会触发画布平移/框选，必须拦下。
    e.stopPropagation();
    // 箭头按钮走 click 步进，不参与拖拽。
    if ((e.target as HTMLElement).closest('button')) return;
    dragRef.current = {
      pointerId: e.pointerId,
      startX: e.clientX,
      startY: e.clientY,
      startRotation: rotation,
      startTilt: tilt,
    };
    // jsdom 没有 setPointerCapture，可选调用。
    e.currentTarget.setPointerCapture?.(e.pointerId);
  };

  const handlePointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== e.pointerId) return;
    const dx = e.clientX - drag.startX;
    const dy = e.clientY - drag.startY;
    // 向右拖 = rotation 增；向下拖 = tilt 减（把球往下压 = 视角抬高）。
    emit(drag.startRotation + dx * DRAG_DEG_PER_PX, drag.startTilt - dy * DRAG_DEG_PER_PX);
  };

  const endDrag = (e: React.PointerEvent<HTMLDivElement>) => {
    if (dragRef.current?.pointerId === e.pointerId) dragRef.current = null;
  };

  // 相机指示器：球面坐标 → 正交投影到容器平面。
  const yaw = (rotation * Math.PI) / 180;
  const pitch = (tilt * Math.PI) / 180;
  const camX = CAM_R * Math.sin(yaw) * Math.cos(pitch);
  const camY = -CAM_R * Math.sin(pitch);
  const camZ = CAM_R * Math.cos(yaw) * Math.cos(pitch);
  const camBehind = camZ < 0;

  return (
    // 故意不加 perspective：透视投影会让旋转后的线框整体偏离容器中心，
    // 图片/相机/球心就对不齐了（liblib 的观感也是近正交——线框永远同心）。
    <div
      className="relative select-none rounded-2xl bg-white/[0.04]"
      style={{ width: 240, height: 240, touchAction: 'none' }}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
    >
      {/* 线框球壳：整体随角度转动（liblib 同款——滑杆转球壳，不转相机） */}
      <div
        className="absolute inset-0"
        style={{
          transformStyle: 'preserve-3d',
          transform: `rotateX(${tilt}deg) rotateY(${-rotation}deg)`,
        }}
      >
        {/* 经线：12 个满径圆环，绕 Y 轴每 15° 一根 */}
        {Array.from({ length: 12 }, (_, i) => (
          <div
            key={`m${i}`}
            data-testid="meridian"
            className="absolute rounded-full border border-solid border-white/10"
            style={{
              width: SHELL_R * 2,
              height: SHELL_R * 2,
              left: `calc(50% - ${SHELL_R}px)`,
              top: `calc(50% - ${SHELL_R}px)`,
              borderColor: 'rgba(255,255,255,0.12)',
              transform: `rotateY(${i * 15}deg)`,
            }}
          />
        ))}
        {/* 纬线：放平（rotateX 90°）后上下平移，直径按 sqrt(R²-h²) 收缩 */}
        {LATITUDE_HEIGHTS.flatMap((h) => (h === 0 ? [0] : [h, -h])).map((h) => {
          const d = 2 * Math.sqrt(SHELL_R * SHELL_R - h * h);
          return (
            <div
              key={`lat${h}`}
              data-testid="latitude"
              className="absolute rounded-full border border-solid"
              style={{
                width: d,
                height: d,
                left: `calc(50% - ${d / 2}px)`,
                top: `calc(50% - ${d / 2}px)`,
                borderColor: 'rgba(255,255,255,0.12)',
                transform: `translateY(${h}px) rotateX(90deg)`,
              }}
            />
          );
        })}
      </div>

      {/* 中央原图：不进 3D 变换，贴在球心视觉位置。外层 div 钳制尺寸并裁剪，
          图片本体撑满——尺寸约束不依赖 img 自身样式，任何比例的原图都不会溢出。 */}
      {/* 居中修正用内联 transform 钉死——线上出现过 Tailwind translate 类未生效、
          图片以左上角对齐球心整体偏右下 44px 的问题，内联样式不受构建产物影响。 */}
      <div
        className="absolute overflow-hidden rounded-xl bg-white/10"
        style={{
          width: imageSize,
          height: imageSize,
          left: '50%',
          top: '50%',
          transform: 'translate(-50%, -50%)',
          zIndex: 10,
          transition: 'width 200ms ease-out, height 200ms ease-out',
        }}
      >
        {imageUrl && (
          <img
            src={imageUrl}
            crossOrigin={mediaCrossOrigin(imageUrl)}
            alt=""
            draggable={false}
            className="h-full w-full object-cover"
          />
        )}
      </div>

      {/* 取景连线：从相机盒边缘画到图片正中心——内端点固定钉在图心 (120,120)，
          任何方位下线都指向同一个点（曾按「距中心18px沿相机方向」取动态端点，
          相机换方位时落点在中心附近漂移，用户点名钉死）。层级压在中央图之上；
          相机贴近图心时线自然缩短到没有。 */}
      {(() => {
        const len = Math.hypot(camX, camY);
        const startDist = len - 12; // 从盒边缘出发，不从立方体里穿出
        if (startDist <= 8) return null; // 盒子已贴到图心，线没意义
        const ux = camX / len;
        const uy = camY / len;
        return (
          <svg
            className="pointer-events-none absolute inset-0"
            style={{ zIndex: 16 }}
            width={240}
            height={240}
            aria-hidden
          >
            <line
              x1={120 + ux * startDist}
              y1={120 + uy * startDist}
              x2={120}
              y2={120}
              stroke={camBehind ? 'rgba(255,255,255,0.25)' : 'rgba(255,255,255,0.75)'}
              strokeWidth={1.5}
            />
          </svg>
        );
      })()}

      {/* 相机指示器：CSS 3D 六面小立方体（对标站造型），**正交渲染**——此前
          给过 perspective:120，26px 盒子在这个透视距离下面片被极端拉扯直接
          "散架"，去透视后立方体在任何角度都扎实。朝向随 rotation/tilt 联动：
          屏幕面(贴原图缩略)始终背对球心朝向观察者一侧、镜头面朝球心——与
          对标站"镜头指着中央图"的语义一致。z<0 = 绕到球背面，淡化 + 缩小。 */}
      <div
        data-testid="camera-indicator"
        className="pointer-events-none absolute"
        style={{
          width: 20,
          height: 20,
          left: '50%',
          top: '50%',
          zIndex: 20,
          transform: `translate(calc(-50% + ${camX}px), calc(-50% + ${camY}px))${
            camBehind ? ' scale(0.8)' : ''
          }`,
          opacity: camBehind ? 0.35 : 1,
        }}
      >
        <div
          className="relative h-full w-full"
          style={{
            transformStyle: 'preserve-3d',
            transform: `rotateY(${rotation}deg) rotateX(${tilt}deg)`,
          }}
        >
          {/* 屏幕面（+Z，朝观察者）：原图缩略 */}
          <div
            className="absolute inset-0 overflow-hidden rounded-[4px] border border-solid border-white/30 bg-neutral-900"
            style={{ transform: 'translateZ(10px)' }}
          >
            {imageUrl && <img src={imageUrl} crossOrigin={mediaCrossOrigin(imageUrl)} alt="" draggable={false} className="h-full w-full object-cover opacity-90" />}
          </div>
          {/* 镜头面（-Z，朝球心）：镜头圆环 */}
          <div
            className="absolute inset-0 flex items-center justify-center rounded-[4px] border border-solid border-white/20 bg-neutral-900"
            style={{ transform: 'rotateY(180deg) translateZ(10px)' }}
          >
            <span className="h-2 w-2 rounded-full border border-solid border-white/60 bg-black" />
          </div>
          {/* 四个侧面：深色机身 */}
          {(['rotateY(90deg)', 'rotateY(-90deg)', 'rotateX(90deg)', 'rotateX(-90deg)'] as const).map((r) => (
            <div
              key={r}
              className="absolute inset-0 rounded-[4px] border border-solid border-white/15 bg-neutral-800"
              style={{ transform: `${r} translateZ(10px)` }}
            />
          ))}
        </div>
      </div>

      {/* 四向步进箭头：左右改 rotation（归一 0~359），上下改 tilt（钳制 ±90，上=俯拍加） */}
      <button
        type="button"
        aria-label="向上"
        className="absolute flex cursor-pointer items-center justify-center border-none bg-transparent text-white/45 hover:text-white"
        style={{ width: 32, height: 32, zIndex: 30, top: 2, left: '50%', transform: 'translateX(-50%)' }}
        onClick={() => emit(rotation, tilt + STEP)}
      >
        <ChevronUp size={16} />
      </button>
      <button
        type="button"
        aria-label="向下"
        className="absolute flex cursor-pointer items-center justify-center border-none bg-transparent text-white/45 hover:text-white"
        style={{ width: 32, height: 32, zIndex: 30, bottom: 2, left: '50%', transform: 'translateX(-50%)' }}
        onClick={() => emit(rotation, tilt - STEP)}
      >
        <ChevronDown size={16} />
      </button>
      <button
        type="button"
        aria-label="向左"
        className="absolute flex cursor-pointer items-center justify-center border-none bg-transparent text-white/45 hover:text-white"
        style={{ width: 32, height: 32, zIndex: 30, left: 2, top: '50%', transform: 'translateY(-50%)' }}
        onClick={() => emit(rotation - STEP, tilt)}
      >
        <ChevronLeft size={16} />
      </button>
      <button
        type="button"
        aria-label="向右"
        className="absolute flex cursor-pointer items-center justify-center border-none bg-transparent text-white/45 hover:text-white"
        style={{ width: 32, height: 32, zIndex: 30, right: 2, top: '50%', transform: 'translateY(-50%)' }}
        onClick={() => emit(rotation + STEP, tilt)}
      >
        <ChevronRight size={16} />
      </button>
    </div>
  );
}
