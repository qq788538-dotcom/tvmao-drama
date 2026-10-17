// 光球的 3D 投影管线。几何常量：
// 球半径 6、图片板原图比例零旋转居中、透视相机 fov50@(11,8,10)、正面相机
// fov50@(0,1,15)、光锥锥口 r2.5 开到球心、星点三环（赤道+俯仰±35°）。
// 网格/星点/光锥/图片板/光点全部过同一台相机投影——数学上不可能互相漂移。

export type Vec3 = [number, number, number];

/** 球半径（世界单位） */
export const SPHERE_R = 6;
/** 控件画布边长（CSS px） */
export const VIEW_SIZE = 200;
/** 相机竖直视场角（度） */
const FOV_DEG = 50;
/** 焦距（px）：画布半高 / tan(fov/2) */
export const FOCAL = VIEW_SIZE / 2 / Math.tan(((FOV_DEG / 2) * Math.PI) / 180);
/** 图片板最大盒（世界单位，横图 4.84×3.18；竖图按同宽缩入） */
export const PLANE_MAX_W = 4.84;
export const PLANE_MAX_H = 3.5;
/** 光锥缺省锥口半径（亮度 50 的实测值；亮度联动时由调用方传入） */
export const CONE_BASE_R_DEFAULT = 2.5;
/** 光点小球半径（世界单位） */
export const LIGHT_DOT_R = 0.4;
/** 星点三环：赤道半径 6.018（略凸出球面），高低环 y=±3.452、水平半径 4.93 */
const STAR_EQ_R = 6.018;
const STAR_LAT_Y = 3.452;
const STAR_LAT_R = 4.93;

const CAM_EYE: Record<'persp' | 'front', Vec3> = {
  persp: [11, 8, 10],
  front: [0.031, 1.02, 14.986],
};

const rad = (d: number) => (d * Math.PI) / 180;
const deg = (r: number) => (r * 180) / Math.PI;
const sub = (a: Vec3, b: Vec3): Vec3 => [a[0] - b[0], a[1] - b[1], a[2] - b[2]];
const cross = (a: Vec3, b: Vec3): Vec3 => [
  a[1] * b[2] - a[2] * b[1],
  a[2] * b[0] - a[0] * b[2],
  a[0] * b[1] - a[1] * b[0],
];
const dot = (a: Vec3, b: Vec3) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
const norm = (a: Vec3): Vec3 => {
  const l = Math.hypot(a[0], a[1], a[2]);
  return [a[0] / l, a[1] / l, a[2] / l];
};

interface Cam {
  eye: Vec3;
  fwd: Vec3;
  right: Vec3;
  up: Vec3;
  /** 球心的投影深度——星点/光点前后判定的分界 */
  centerZc: number;
}

const CAMS: Record<'persp' | 'front', Cam> = (() => {
  const make = (eye: Vec3): Cam => {
    const fwd = norm(sub([0, 0, 0], eye));
    const right = norm(cross(fwd, [0, 1, 0]));
    const up = cross(right, fwd);
    return { eye, fwd, right, up, centerZc: Math.hypot(...eye) };
  };
  return { persp: make(CAM_EYE.persp), front: make(CAM_EYE.front) };
})();

export interface Projected {
  x: number;
  y: number;
  /** 相机空间深度；与 centerZc 比较可判前后半球 */
  zc: number;
}

/** 世界点 → 画布像素坐标（透视投影）。 */
export function projectPoint(view: 'persp' | 'front', p: Vec3): Projected {
  const cam = CAMS[view];
  const d = sub(p, cam.eye);
  const zc = dot(d, cam.fwd);
  return {
    x: VIEW_SIZE / 2 + (FOCAL * dot(d, cam.right)) / zc,
    y: VIEW_SIZE / 2 - (FOCAL * dot(d, cam.up)) / zc,
    zc,
  };
}

export function centerZc(view: 'persp' | 'front'): number {
  return CAMS[view].centerZc;
}

/** 方向 (yaw,pitch) → 球面世界坐标。yaw 0=正前(+z)，向右为正；pitch 正=偏上。 */
export function directionToWorld(yawDeg: number, pitchDeg: number): Vec3 {
  const c = Math.cos(rad(pitchDeg));
  return [
    SPHERE_R * Math.sin(rad(yawDeg)) * c,
    SPHERE_R * Math.sin(rad(pitchDeg)),
    SPHERE_R * Math.cos(rad(yawDeg)) * c,
  ];
}

/**
 * 画布像素 → 相机射线与球求交 → 球面 (yaw,pitch)。拖拽的核心：光点永远落在
 * 光标指到的球面处（raycast 拖拽手感）。射线打不中球（光标在
 * 轮廓外）时取最近逼近点方向贴着轮廓滑动。命中取近侧表面（可见半球）。
 */
export function rayToSphere(view: 'persp' | 'front', sx: number, sy: number) {
  const cam = CAMS[view];
  const xc = (sx - VIEW_SIZE / 2) / FOCAL;
  const yc = (VIEW_SIZE / 2 - sy) / FOCAL;
  const dir = norm([
    cam.right[0] * xc + cam.up[0] * yc + cam.fwd[0],
    cam.right[1] * xc + cam.up[1] * yc + cam.fwd[1],
    cam.right[2] * xc + cam.up[2] * yc + cam.fwd[2],
  ]);
  const o = cam.eye;
  const b = dot(o, dir);
  const disc = b * b - (dot(o, o) - SPHERE_R * SPHERE_R);
  let p: Vec3;
  if (disc >= 0) {
    const t = -b - Math.sqrt(disc);
    p = [o[0] + t * dir[0], o[1] + t * dir[1], o[2] + t * dir[2]];
  } else {
    // 未命中：取射线到球心的最近点，法向拉回球面（贴轮廓滑动）
    const t = -b;
    p = norm([o[0] + t * dir[0], o[1] + t * dir[1], o[2] + t * dir[2]]).map(
      (v) => v * SPHERE_R,
    ) as Vec3;
  }
  return {
    yaw: deg(Math.atan2(p[0], p[2])),
    pitch: deg(Math.asin(Math.max(-1, Math.min(1, p[1] / SPHERE_R)))),
  };
}

/**
 * 4 点单应：把 w×h 的矩形（transform-origin 0 0）映射到画布上的投影四边形。
 * dst 顺序：左上、右上、右下、左下（与矩形四角一一对应）。返回 CSS matrix3d。
 */
export function homographyMatrix(
  w: number,
  h: number,
  dst: Array<{ x: number; y: number }>,
): string {
  const src = [
    [0, 0],
    [w, 0],
    [w, h],
    [0, h],
  ];
  const A: number[][] = [];
  const b: number[] = [];
  for (let i = 0; i < 4; i++) {
    const [x, y] = src[i];
    const { x: X, y: Y } = dst[i];
    A.push([x, y, 1, 0, 0, 0, -X * x, -X * y]);
    b.push(X);
    A.push([0, 0, 0, x, y, 1, -Y * x, -Y * y]);
    b.push(Y);
  }
  // 高斯-约当消元（8 元）
  for (let c = 0; c < 8; c++) {
    let pivot = c;
    for (let r = c + 1; r < 8; r++) if (Math.abs(A[r][c]) > Math.abs(A[pivot][c])) pivot = r;
    [A[c], A[pivot]] = [A[pivot], A[c]];
    [b[c], b[pivot]] = [b[pivot], b[c]];
    for (let r = 0; r < 8; r++) {
      if (r === c) continue;
      const f = A[r][c] / A[c][c];
      for (let k = c; k < 8; k++) A[r][k] -= f * A[c][k];
      b[r] -= f * b[c];
    }
  }
  const v = b.map((x, i) => x / A[i][i]);
  const [a, bb, c, d, e, f, g, hh] = v;
  return `matrix3d(${a},${d},0,${g},${bb},${e},0,${hh},0,0,1,0,${c},${f},0,1)`;
}

/** 网格环（赤道/±35°纬环/两根经线）的 SVG path d。 */
export function gridPaths(view: 'persp' | 'front'): string[] {
  const rings: Vec3[][] = [];
  const sample = (fn: (t: number) => Vec3) => {
    const pts: Vec3[] = [];
    for (let i = 0; i <= 64; i++) pts.push(fn((i / 64) * Math.PI * 2));
    rings.push(pts);
  };
  sample((t) => [STAR_EQ_R * Math.sin(t), 0, STAR_EQ_R * Math.cos(t)]);
  sample((t) => [STAR_LAT_R * Math.sin(t), STAR_LAT_Y, STAR_LAT_R * Math.cos(t)]);
  sample((t) => [STAR_LAT_R * Math.sin(t), -STAR_LAT_Y, STAR_LAT_R * Math.cos(t)]);
  sample((t) => [0, SPHERE_R * Math.cos(t), SPHERE_R * Math.sin(t)]);
  sample((t) => [SPHERE_R * Math.sin(t), SPHERE_R * Math.cos(t), 0]);
  return rings.map((pts) => {
    let d = '';
    for (let i = 0; i < pts.length; i++) {
      const p = projectPoint(view, pts[i]);
      d += (i ? 'L' : 'M') + p.x.toFixed(1) + ' ' + p.y.toFixed(1);
    }
    return d;
  });
}

/** 星点（24 颗，三环每 45° 一颗）的投影位置与前后。 */
export function starPoints(view: 'persp' | 'front'): Array<{ x: number; y: number; front: boolean }> {
  const out: Array<{ x: number; y: number; front: boolean }> = [];
  const cz = centerZc(view);
  for (const [y, r] of [
    [0, STAR_EQ_R],
    [STAR_LAT_Y, STAR_LAT_R],
    [-STAR_LAT_Y, STAR_LAT_R],
  ] as Array<[number, number]>) {
    for (let i = 0; i < 8; i++) {
      const t = (i / 8) * Math.PI * 2;
      const p = projectPoint(view, [r * Math.sin(t), y, r * Math.cos(t)]);
      out.push({ x: p.x, y: p.y, front: p.zc < cz });
    }
  }
  return out;
}

/**
 * 光锥多边形（SVG points）：锥尖在光点、底圆 r2.5 在球心，投影后取凸包。
 * 同时返回锥尖投影（渐变起点用）。
 */
export function conePolygon(
  view: 'persp' | 'front',
  lightPos: Vec3,
  baseR: number = CONE_BASE_R_DEFAULT,
) {
  const apex = projectPoint(view, lightPos);
  const axis = norm(sub([0, 0, 0], lightPos));
  let u = cross(axis, [0, 1, 0]);
  if (Math.hypot(...u) < 1e-6) u = [1, 0, 0];
  u = norm(u);
  const v = cross(axis, u);
  const pts: Array<[number, number]> = [[apex.x, apex.y]];
  for (let i = 0; i < 24; i++) {
    const t = (i / 24) * Math.PI * 2;
    const p = projectPoint(view, [
      baseR * (u[0] * Math.cos(t) + v[0] * Math.sin(t)),
      baseR * (u[1] * Math.cos(t) + v[1] * Math.sin(t)),
      baseR * (u[2] * Math.cos(t) + v[2] * Math.sin(t)),
    ]);
    pts.push([p.x, p.y]);
  }
  // Andrew 单调链凸包
  pts.sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  const cr = (o: number[], a: number[], b2: number[]) =>
    (a[0] - o[0]) * (b2[1] - o[1]) - (a[1] - o[1]) * (b2[0] - o[0]);
  const lower: Array<[number, number]> = [];
  for (const p of pts) {
    while (lower.length >= 2 && cr(lower[lower.length - 2], lower[lower.length - 1], p) <= 0)
      lower.pop();
    lower.push(p);
  }
  const upper: Array<[number, number]> = [];
  for (const p of [...pts].reverse()) {
    while (upper.length >= 2 && cr(upper[upper.length - 2], upper[upper.length - 1], p) <= 0)
      upper.pop();
    upper.push(p);
  }
  const hull = lower.slice(0, -1).concat(upper.slice(0, -1));
  return { points: hull.map((p) => `${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' '), apex };
}
