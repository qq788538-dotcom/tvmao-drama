// 打光方向的几何真源：26 档枚举（8 水平方位 × 3 高度 + top/bottom，对标 liblib
// UI_KeyLight 词表）与球面吸附。key 与服务端 lightDirectionPhrases 一一对应——
// 两侧词表漂移会让「拖到的位置」和「拼出的文案」说两回事，加档位必须两端同步。

/** 每档的球面坐标（度）。yaw：0=正前，向右为正；pitch：0=赤道，正=偏上。 */
export interface LightDirectionSpec {
  key: string;
  yaw: number;
  pitch: number;
}

const RING: Array<[string, number]> = [
  ['front', 0],
  ['front-right', 45],
  ['right', 90],
  ['back-right', 135],
  ['back', 180],
  ['back-left', 225],
  ['left', 270],
  ['front-left', 315],
];

/** 高/低环的俯仰角。35°=liblib 星点环的实测俯仰（y=3.452 / r=6.018）。 */
const ELEVATED_PITCH = 35;

export const LIGHT_DIRECTIONS: LightDirectionSpec[] = [
  ...RING.map(([key, yaw]) => ({ key, yaw, pitch: 0 })),
  ...RING.map(([key, yaw]) => ({ key: `high-${key}`, yaw, pitch: ELEVATED_PITCH })),
  ...RING.map(([key, yaw]) => ({ key: `low-${key}`, yaw, pitch: -ELEVATED_PITCH })),
  { key: 'top', yaw: 0, pitch: 90 },
  { key: 'bottom', yaw: 0, pitch: -90 },
];

const BY_KEY = new Map(LIGHT_DIRECTIONS.map((d) => [d.key, d]));

export function directionSpec(key: string): LightDirectionSpec {
  return BY_KEY.get(key) ?? BY_KEY.get('front')!;
}

const rad = (deg: number) => (deg * Math.PI) / 180;

/** 球面坐标 → 单位向量。x=右，y=上，z=朝向观察者（front 的朝向）。 */
export function directionVector(yaw: number, pitch: number): [number, number, number] {
  const y = Math.sin(rad(pitch));
  const c = Math.cos(rad(pitch));
  return [Math.sin(rad(yaw)) * c, y, Math.cos(rad(yaw)) * c];
}

const VECTORS = LIGHT_DIRECTIONS.map((d) => ({
  key: d.key,
  v: directionVector(d.yaw, d.pitch),
}));

/**
 * 连续 (yaw, pitch) 吸附到最近的方向档（余弦最近邻）。拖拽期间连续值在控件内部
 * 保持，只有吸附结果对外——对标 liblib：球面拖到哪都落到 26 档之一。
 */
export function snapToDirection(yaw: number, pitch: number): string {
  const v = directionVector(yaw, pitch);
  let best = 'front';
  let bestDot = -Infinity;
  for (const { key, v: dv } of VECTORS) {
    const dot = v[0] * dv[0] + v[1] * dv[1] + v[2] * dv[2];
    if (dot > bestDot) {
      bestDot = dot;
      best = key;
    }
  }
  return best;
}
