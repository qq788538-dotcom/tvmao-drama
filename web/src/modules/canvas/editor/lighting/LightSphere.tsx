import { useId, useRef, useState } from 'react';
import { directionSpec, snapToDirection } from './directions';
import {
  FOCAL,
  LIGHT_DOT_R,
  PLANE_MAX_H,
  PLANE_MAX_W,
  VIEW_SIZE,
  centerZc,
  conePolygon,
  directionToWorld,
  gridPaths,
  homographyMatrix,
  projectPoint,
  rayToSphere,
  starPoints,
} from './projection';

export interface LightSphereProps {
  /** 当前主光源方向（26 档枚举 key） */
  direction: string;
  /** 中央展示的原图缩略 URL（可空，空时显示灰色占位板） */
  imageUrl?: string;
  /** 相机视角：透视=fov50@(11,8,10)，正面=fov50@(0,1,15)——liblib 实测参数 */
  view: 'persp' | 'front';
  /** 主光颜色（#rrggbb）：光锥与光晕跟随此色染色（对标 liblib 选蓝出蓝锥） */
  lightColor?: string;
  /** 亮度 0-100：实时驱动锥宽/锥亮/光晕/球面泛光（liblib 三档截图实测校准：
   * 0=细针、50=锥口 r2.5 基准、100=r3.8 更亮）。缺省 50。 */
  brightness?: number;
  /** 拖拽球面改光位时回调（值已吸附到 26 档） */
  onChange: (direction: string) => void;
}

/** 球体视觉直径（px）：正面相机下球轮廓 ≈ 172px，两视角共用一张底球 */
const BALL_PX = 172;
/** 拖拽起点必须落在光点这个半径内（CDP 实测 liblib：偏 14px 按下球完全不动） */
const GRAB_R = 26;

/** #rrggbb → rgba 字符串（非法输入回退白色）。 */
function tint(hex: string | undefined, alpha: number): string {
  const m = /^#([0-9a-fA-F]{6})$/.exec(hex ?? '');
  if (!m) return `rgba(255,255,255,${alpha})`;
  const n = parseInt(m[1], 16);
  return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${alpha})`;
}

/**
 * 打光效果的光球控件。全部几何来自对 liblib light-scene-3d 的 three devtools
 * 实测（见 projection.ts 头注释），渲染是一个小型投影管线：网格/星点/光锥走
 * SVG、图片板走单应矩阵、光点/光晕走投影定位——所有元素过同一台透视相机。
 * 拖拽 = 相机射线与球求交（光点贴着光标走，对标 liblib 手感）；输出永远是
 * 26 档枚举之一（吸附在 snapToDirection），连续位只存在于组件内部。
 */
export function LightSphere({
  direction,
  imageUrl,
  view,
  lightColor,
  brightness = 50,
  onChange,
}: LightSphereProps) {
  const gradId = useId();
  const dragRef = useRef<{ pointerId: number } | null>(null);
  // 连续光位（渲染真源）：拖拽时逐帧更新；外部改方向（按钮/预设/重置）时归位到档位角。
  const [pos, setPos] = useState<{ yaw: number; pitch: number }>(() => {
    const s = directionSpec(direction);
    return { yaw: s.yaw, pitch: s.pitch };
  });
  // 图片板宽高比（加载后取自然尺寸，limbo 期用 liblib 实测的 4.84:3.18）。
  const [imgRatio, setImgRatio] = useState(4.84 / 3.177);

  // 外部（方向按钮/预设/重置）把 direction 改到了与连续位不同的档：连续位归位。
  // 只在非拖拽期同步——拖拽中 onChange 与父组件回流存在一帧时差，这时同步会把
  // 连续位拽回旧档位、光点跳变（render 期 setState 是 React 认可的派生态写法）。
  const spec = directionSpec(direction);
  if (!dragRef.current && snapToDirection(pos.yaw, pos.pitch) !== direction) {
    setPos({ yaw: spec.yaw, pitch: spec.pitch });
  }

  const lightWorld = directionToWorld(pos.yaw, pos.pitch);
  const dot = projectPoint(view, lightWorld);
  const lightInFront = dot.zc < centerZc(view);
  // 光点视觉尺寸随深度缩放（真透视：转到远侧自然变小）
  const dotPx = ((2 * LIGHT_DOT_R * FOCAL) / dot.zc) | 0;

  const localXY = (e: React.PointerEvent<HTMLDivElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    return { x: e.clientX - rect.left, y: e.clientY - rect.top };
  };

  const handlePointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    // 控件在画布弹层里——冒泡上去会触发画布平移/框选，必须拦下。
    e.stopPropagation();
    const { x, y } = localXY(e);
    // 只有按在光点附近才开始拖（liblib 同款）：随手点球面不让光位跳变。
    if (Math.hypot(x - dot.x, y - dot.y) > GRAB_R) return;
    dragRef.current = { pointerId: e.pointerId };
    // jsdom 没有 setPointerCapture，可选调用。
    e.currentTarget.setPointerCapture?.(e.pointerId);
  };

  const handlePointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== e.pointerId) return;
    const { x, y } = localXY(e);
    const next = rayToSphere(view, x, y);
    setPos(next);
    const snapped = snapToDirection(next.yaw, next.pitch);
    if (snapped !== direction) onChange(snapped);
  };

  const endDrag = (e: React.PointerEvent<HTMLDivElement>) => {
    if (dragRef.current?.pointerId === e.pointerId) dragRef.current = null;
  };

  // 图片板：原图比例塞进 liblib 实测的最大盒，四角投影 → 单应矩阵贴图。
  const planeW = Math.min(PLANE_MAX_W, PLANE_MAX_H * imgRatio);
  const planeH = planeW / imgRatio;
  const corners = [
    [-planeW / 2, planeH / 2, 0],
    [planeW / 2, planeH / 2, 0],
    [planeW / 2, -planeH / 2, 0],
    [-planeW / 2, -planeH / 2, 0],
  ].map((p) => projectPoint(view, p as [number, number, number]));
  // 矩形源盒比例必须等于板的世界比例，内容才不被单应拉伸（16 只是像素密度系数）。
  const boxW = planeW * 16;
  const boxH = planeH * 16;
  const planeMat = homographyMatrix(boxW, boxH, corners);

  // 亮度联动（liblib 0/50/100 实测拟合）：锥口半径分段线性，50 档=实测基准 2.5；
  // 浓度/光晕/泛光同步小幅增强。渐变在 75% 处完全淡出——实测 liblib 的锥在到达
  // 图片板前就消失，收口比拉长更重要。
  const b = Math.min(100, Math.max(0, brightness));
  const coneR = b <= 50 ? 0.25 + (2.25 * b) / 50 : 2.5 + (1.3 * (b - 50)) / 50;
  const apexAlpha = (0.55 + (0.3 * b) / 100) * (lightInFront ? 1 : 0.65);
  const glowScale = 0.7 + (0.6 * b) / 100;
  const washAlpha = (0.1 + (0.08 * b) / 100) * (lightInFront ? 1 : 0.6);
  const cone = conePolygon(view, lightWorld, coneR);
  const half = VIEW_SIZE / 2;

  return (
    <div
      data-testid="light-sphere"
      className="relative select-none"
      style={{ width: VIEW_SIZE, height: VIEW_SIZE, touchAction: 'none' }}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
    >
      {/* 球体：半透明玻璃感——顶部微亮的环境光 + 柔和边缘 */}
      <div
        className="absolute rounded-full"
        style={{
          width: BALL_PX,
          height: BALL_PX,
          left: half - BALL_PX / 2,
          top: half - BALL_PX / 2,
          background:
            'radial-gradient(circle at 42% 24%, rgba(255,255,255,0.32) 0%, rgba(255,255,255,0.13) 40%, rgba(255,255,255,0.05) 70%, rgba(0,0,0,0.05) 100%)',
          backgroundColor: 'rgba(165,165,165,0.26)',
          boxShadow:
            'inset -6px -12px 28px rgba(0,0,0,0.28), inset 4px 8px 22px rgba(255,255,255,0.12)',
        }}
      />
      {/* 光点侧染色微光：贴着球面在光点附近提亮，让球的明暗跟着光走 */}
      <div
        className="pointer-events-none absolute rounded-full"
        style={{
          width: BALL_PX,
          height: BALL_PX,
          left: half - BALL_PX / 2,
          top: half - BALL_PX / 2,
          background: `radial-gradient(circle at ${((dot.x - (half - BALL_PX / 2)) / BALL_PX) * 100}% ${
            ((dot.y - (half - BALL_PX / 2)) / BALL_PX) * 100
          }%, ${tint(lightColor, washAlpha)} 0%, transparent 40%)`,
        }}
      />

      {/* 网格 + 星点 + 光锥：同一台相机投影，SVG 绘制 */}
      <svg
        width={VIEW_SIZE}
        height={VIEW_SIZE}
        className="pointer-events-none absolute inset-0"
        style={{ zIndex: 1 }}
      >
        <defs>
          <linearGradient
            id={gradId}
            gradientUnits="userSpaceOnUse"
            x1={cone.apex.x}
            y1={cone.apex.y}
            x2={half}
            y2={half}
          >
            <stop offset="0%" stopColor={tint(lightColor, lightInFront ? 0.7 : 0.45)} />
            <stop offset="100%" stopColor={tint(lightColor, 0.04)} />
          </linearGradient>
        </defs>
        {gridPaths(view).map((d, i) => (
          <path key={i} d={d} fill="none" stroke="rgba(255,255,255,0.07)" strokeWidth={1} />
        ))}
        {starPoints(view).map((p, i) => (
          <circle
            key={i}
            cx={p.x}
            cy={p.y}
            r={1.6}
            fill={`rgba(255,255,255,${p.front ? 0.55 : 0.12})`}
          />
        ))}
        <polygon data-testid="light-cone" points={cone.points} fill={`url(#${gradId})`} />
      </svg>

      {/* 中央原图：单应矩阵把矩形贴到板的投影四边形（白边框；占位盒同款变换） */}
      {imageUrl ? (
        <img
          src={imageUrl}
          alt=""
          draggable={false}
          onLoad={(e) => {
            const el = e.currentTarget;
            if (el.naturalWidth > 0 && el.naturalHeight > 0) {
              setImgRatio(el.naturalWidth / el.naturalHeight);
            }
          }}
          className="pointer-events-none absolute left-0 top-0 rounded-[2px] object-fill shadow-[0_6px_16px_rgba(0,0,0,0.45)]"
          style={{
            width: boxW,
            height: boxH,
            transformOrigin: '0 0',
            transform: planeMat,
            outline: '2px solid rgba(255,255,255,0.9)',
            outlineOffset: -1,
            zIndex: 2,
          }}
        />
      ) : (
        <div
          className="pointer-events-none absolute left-0 top-0 rounded-[2px] bg-white/10 shadow-[0_6px_16px_rgba(0,0,0,0.45)]"
          style={{
            width: boxW,
            height: boxH,
            transformOrigin: '0 0',
            transform: planeMat,
            outline: '2px solid rgba(255,255,255,0.6)',
            outlineOffset: -1,
            zIndex: 2,
          }}
        />
      )}

      {/* 光点：染色光晕 + 深色小球（尺寸随投影深度缩放；转到远侧沉到图片板下） */}
      <div
        data-testid="light-dot"
        className="pointer-events-none absolute rounded-full"
        style={{
          width: dotPx * 3 * glowScale,
          height: dotPx * 3 * glowScale,
          left: 0,
          top: 0,
          transform: `translate(${dot.x}px, ${dot.y}px) translate(-50%, -50%)`,
          background: `radial-gradient(circle, ${tint(lightColor, 0.5 + (0.3 * b) / 100)} 0%, ${tint(
            lightColor,
            0.25,
          )} 45%, transparent 70%)`,
          opacity: lightInFront ? 1 : 0.85,
          zIndex: lightInFront ? 4 : 1,
        }}
      />
      <div
        className="pointer-events-none absolute rounded-full"
        style={{
          width: dotPx,
          height: dotPx,
          left: 0,
          top: 0,
          transform: `translate(${dot.x}px, ${dot.y}px) translate(-50%, -50%)`,
          background: 'radial-gradient(circle at 35% 30%, #3c3c3c, #101010 70%)',
          boxShadow: '0 1px 4px rgba(0,0,0,0.6)',
          opacity: lightInFront ? 1 : 0.9,
          zIndex: lightInFront ? 5 : 1,
        }}
      />
    </div>
  );
}
