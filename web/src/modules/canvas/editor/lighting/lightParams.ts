// 打光面板 → PresetParams 的组装（纯函数，独立成文件为了可测）。
// 契约与服务端 ComposeLightControlText 一一对应：**缺 key = 用户没碰该组 = 不拼**
// （对标 liblib 的脏标记语义 sceneActive/brightnessActive/colorActive——缺省交给
// 模型自由发挥，不发默认值捆住模型）。

export interface LightFormState {
  direction: string; // 26 档枚举，UI 缺省 'front'
  sceneTouched: boolean; // 用户碰过球面/方向按钮/轮廓光才算
  rimEnabled: boolean;
  brightness: number; // 0-100，UI 缺省 50
  brightnessTouched: boolean;
  lightColor: string; // #rrggbb，UI 缺省 #ffffff
  colorTouched: boolean;
  presetKey: string; // ''=未选预设
  hasReferenceImage: boolean;
}

export function buildLightPresetParams(s: LightFormState): Record<string, string> {
  const params: Record<string, string> = {};
  if (s.sceneTouched) {
    params.keyLight = s.direction;
    // 主光源在正后方时轮廓光无意义（光已经从背面来了）——liblib 同款跳过。
    if (s.rimEnabled && s.direction !== 'back') params.rimLight = 'back';
  }
  if (s.brightnessTouched) params.brightness = String(s.brightness);
  if (s.colorTouched) params.lightColor = s.lightColor;
  if (s.presetKey) params.preset = s.presetKey;
  if (s.hasReferenceImage) params.hasRef = '1';
  return params;
}

/** 有没有任何可提交的内容（↑ 按钮的禁用判定，对标 liblib：全默认时不可提交）。 */
export function hasLightDirtyState(s: LightFormState, prompt: string): boolean {
  return (
    s.sceneTouched ||
    s.brightnessTouched ||
    s.colorTouched ||
    Boolean(s.presetKey) ||
    s.hasReferenceImage ||
    prompt.trim() !== ''
  );
}
