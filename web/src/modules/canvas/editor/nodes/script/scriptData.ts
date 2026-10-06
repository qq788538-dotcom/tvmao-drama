import { nanoid } from 'nanoid';

// 脚本节点数据模型——对齐 Drama Skill 的 candidate.json（定稿见
// docs/superpowers/specs/2026-08-01-canvas-script-node-skill-align.md）。
// 一行 = skill 的一个 storyboard（一次视频生成提交），prompt 是唯一真源：
// 景别/运镜/对白/音效等全部活在 prompt 的「镜头N：」行里，与步骤 2 资产的
// 关联也只靠 prompt 内的 @[类型-名称] token，不再有散字段第二真源。

export type ColorMark = '' | 'red' | 'yellow' | 'green' | 'blue' | 'gray';

export interface ScriptRow {
  id: string;
  /** 展示镜号，按数组序自动重排 */
  shotNumber: number;
  /** 分镜简述，skill 硬约束 ≤15 字（前端只做红点提示，不阻塞保存） */
  name: string;
  /** 整数秒，4 ~ durationSec（主切割时长） */
  duration: number;
  /** skill 语义完整提示词：固定风格串 + 镜头N 行，内嵌 @[类型-名称] token */
  prompt: string;
  /** 行颜色标记——我们自己的辅助功能，不进导出 JSON */
  colorMark: ColorMark;
}

export interface ScriptAsset {
  id: string;
  /** 同类型内唯一——它是 prompt token 的外键，改名必须全表联动替换 token */
  name: string;
  /** ≡ skill 资产的 prompt（生图提示词） */
  description: string;
  /** 上游画布图片节点 id；图片本体永远归节点管 */
  linkedNodeId: string;
}

export interface ScriptAssets {
  characters: ScriptAsset[];
  scenes: ScriptAsset[];
  props: ScriptAsset[];
}

/** 「剧本生成分镜脚本」生成器的持久化状态——taskId 落 settings 才能在刷新
 * 页面后续轮询（任务在服务端异步跑，浏览器只是观察者）。 */
export interface ScriptGenState {
  /** 进行中的服务端任务 id；0 = 无任务 */
  taskId: number;
  /** 最近选择的风格展示名（回填选择器） */
  style: string;
  /** 最近选择的 chat 模型 slug（回填选择器；随画布快照持久化） */
  model: string;
  /** 最近选择的最长秒数 DURATION_SEC（4-15，回填选择器） */
  durationSec: number;
  /** 最近一次失败原因；空 = 无失败 */
  error: string;
}

export interface ScriptSettings {
  /** 「剧本生成分镜脚本」入口的剧本原文（M2 跑 skill 用） */
  scriptPrompt: string;
  /** M2 跑 skill 的文本模型，显式必填 */
  modelId: string;
  rows: ScriptRow[];
  assets: ScriptAssets;
  /** skill 输出的全局风格（展示用） */
  globalStyle: string;
  /** 主切割时长 DURATION_SEC：duration 上限 + M2 传给 skill 的参数 */
  durationSec: number;
  /** 剧本生成分镜的任务状态（见 ScriptGenState） */
  gen: ScriptGenState;
}

export const MIN_SHOT_SECONDS = 4;
// 默认 15：durationSec 的配置入口（生成器设置）要 M2 才有，默认值就是当前用户
// 能用到的唯一上限——取 M1 时代的 15s 上限，别拿 skill 常用参数 10 卡住手写用户。
export const DEFAULT_DURATION_SEC = 15;
export const MAX_NAME_LEN = 15;
// 剧本长度上限（生成器输入框 + 上游文字节点拼接后的总长）。后端 Submit 有同值
// 权威拦截（canvasscript.MaxScriptRunes），这里只是前端友好提示的那道。
export const MAX_SCRIPT_LEN = 5000;

export function createScriptRow(): ScriptRow {
  return {
    id: nanoid(8),
    shotNumber: 0,
    name: '',
    duration: MIN_SHOT_SECONDS,
    prompt: '',
    colorMark: '',
  };
}

export function defaultScriptSettings(): ScriptSettings {
  return {
    scriptPrompt: '',
    modelId: '',
    rows: [],
    assets: { characters: [], scenes: [], props: [] },
    globalStyle: '',
    durationSec: DEFAULT_DURATION_SEC,
    gen: { taskId: 0, style: '', model: '', durationSec: DEFAULT_DURATION_SEC, error: '' },
  };
}

export function renumberRows(rows: ScriptRow[]): ScriptRow[] {
  return rows.map((r, i) => (r.shotNumber === i + 1 ? r : { ...r, shotNumber: i + 1 }));
}

// 容错读取：settings 来自快照 JSON，可能是旧 schema（2026-08-01 前的 10 列散字段版）
// 或半残数据。旧版行没有 name/prompt 这套字段——脚本节点无存量用户数据（定稿文档
// 「兼容性说明」），不做字段迁移，识别不出的字段按默认值降级，别猜。
export function readScriptSettings(raw: Record<string, unknown> | undefined): ScriptSettings {
  const d = defaultScriptSettings();
  if (!raw) return d;
  const rows = Array.isArray(raw.rows)
    ? (raw.rows as Partial<ScriptRow>[]).map((r) => ({
        ...createScriptRow(),
        ...(typeof r.name === 'string' ? { name: r.name } : {}),
        ...(typeof r.duration === 'number' ? { duration: Math.round(r.duration) } : {}),
        ...(typeof r.prompt === 'string' ? { prompt: r.prompt } : {}),
        ...(typeof r.colorMark === 'string' ? { colorMark: r.colorMark as ColorMark } : {}),
        id: typeof r.id === 'string' && r.id ? r.id : nanoid(8),
      }))
    : d.rows;
  const rawAssets = (raw.assets ?? {}) as Partial<ScriptAssets>;
  return {
    scriptPrompt: typeof raw.scriptPrompt === 'string' ? raw.scriptPrompt : d.scriptPrompt,
    modelId: typeof raw.modelId === 'string' ? raw.modelId : d.modelId,
    rows: renumberRows(rows),
    assets: {
      characters: Array.isArray(rawAssets.characters) ? rawAssets.characters : [],
      scenes: Array.isArray(rawAssets.scenes) ? rawAssets.scenes : [],
      props: Array.isArray(rawAssets.props) ? rawAssets.props : [],
    },
    globalStyle: typeof raw.globalStyle === 'string' ? raw.globalStyle : d.globalStyle,
    // 恒定 15（4~15s，用户拍板）：这是步骤 1 时长编辑的上限，不跟随生成器选的
    // DURATION_SEC——skill 生成的行天然 ≤ 所选秒数，用户手动放宽到 15 是自由；
    // 存量快照里残留的早期默认值 10 也不该反过来卡住手写用户。
    durationSec: d.durationSec,
    gen: readGenState(raw.gen),
  };
}

function readGenState(raw: unknown): ScriptGenState {
  const d = defaultScriptSettings().gen;
  if (!raw || typeof raw !== 'object') return d;
  const g = raw as Partial<ScriptGenState>;
  const dur = typeof g.durationSec === 'number' ? Math.round(g.durationSec) : d.durationSec;
  return {
    taskId: typeof g.taskId === 'number' && g.taskId > 0 ? g.taskId : 0,
    style: typeof g.style === 'string' ? g.style : '',
    model: typeof g.model === 'string' ? g.model : '',
    durationSec: dur >= MIN_SHOT_SECONDS && dur <= DEFAULT_DURATION_SEC ? dur : d.durationSec,
    error: typeof g.error === 'string' ? g.error : '',
  };
}

// 卡片/步骤条共用的进度统计。两步制：确认分镜（行数）→ 准备资产（配图进度）。
export interface ScriptStepStats {
  shots: number;
  assetsReady: number;
  assetsTotal: number;
}

export function scriptStepStats(s: ScriptSettings): ScriptStepStats {
  const all = [...s.assets.characters, ...s.assets.scenes, ...s.assets.props];
  return {
    shots: s.rows.length,
    assetsReady: all.filter((a) => a.linkedNodeId !== '').length,
    assetsTotal: all.length,
  };
}
