/**
 * AI 绘图 / AI 视频节点的「多版本历史」数据结构。
 *
 * 背景：老快照里 `settings.history` 是纯 URL 数组 `string[]`，只记了「出了哪些图」，
 * 没记「每张是用什么提示词出的」。用户改提示词重新生成后，`settings.prompt` 被覆盖，
 * 上一版对应的那句话在画布数据里就永久消失了，翻回旧图也只能看到最新的提示词。
 *
 * 现在把条目升级成对象，生成时把当次的 prompt + 入参一起写进去，之后「哪一版对应哪句
 * 提示词」纯从画布快照本地可读，不依赖后端。
 *
 * 向后兼容是硬约束（线上存量画布不能有任何行为变化）：
 *   - 读：`normalizeHistory` 把 `string` 条目升成 `{ url }`，两种格式在下游完全同构；
 *   - 显示：老条目没有自带 prompt，一律回退到节点当前的 `settings.prompt`
 *     （见 `historyPromptAt`）——也就是和今天看到的一模一样，不会出现空值或
 *     「未记录」之类的新文案。
 */
export interface GenHistoryItem {
  /** 结果媒体 URL（media:// 协议），等价于老格式里的那个字符串。 */
  url: string;
  /** 生成这一版时实际提交的提示词。老条目没有，读的时候回退到节点当前 prompt。 */
  prompt?: string;
  /** 生成这一版时的完整入参快照（比例/分辨率/时长等）。整块存，避免手挑字段在换模型后漏。 */
  params?: Record<string, unknown>;
  /** 生成完成时间（ISO）。仅供展示，缺省不影响任何逻辑。 */
  at?: string;
  /** 生成这一版时选中的提示词预设 key（gen-image 专用，选填）。内部标识，不进用户视觉层。 */
  presetKey?: string;
  /**
   * 预设当时的显示名快照。单独存一份而不是每次按 key 反查配置——预设日后被
   * 后台删除/改名，历史仍能显示当时的名字（约定：落库用 key、展示用 name）。
   */
  presetName?: string;
  /** 生成这一版时的预设参数选择（维度 key → 选项 key）。与 presetKey 同理是提交时刻的事实快照。 */
  presetParams?: Record<string, string>;
}

/**
 * 把 `settings.history` 归一化成对象数组，兼容三种来源：
 *   - 新格式 `[{url, prompt, ...}]` —— 原样透传；
 *   - 老格式 `['media://a', ...]` —— 升成 `{ url }`，无 prompt；
 *   - 没有 history 但有 `node.content` —— 当作只有一条、用该 URL 初始化
 *     （维持老逻辑：更早期的节点根本没写过 history）。
 *
 * 脏数据（null / 空串 / 缺 url 的对象）一律丢弃，避免渲染出空白版本格。
 */
export function normalizeHistory(raw: unknown, fallbackContent?: string): GenHistoryItem[] {
  const items: GenHistoryItem[] = [];
  if (Array.isArray(raw)) {
    for (const entry of raw) {
      if (typeof entry === 'string') {
        if (entry) items.push({ url: entry });
        continue;
      }
      if (entry && typeof entry === 'object') {
        const e = entry as Partial<GenHistoryItem>;
        if (typeof e.url === 'string' && e.url) {
          items.push({
            url: e.url,
            prompt: typeof e.prompt === 'string' ? e.prompt : undefined,
            params: e.params && typeof e.params === 'object' ? e.params : undefined,
            at: typeof e.at === 'string' ? e.at : undefined,
            presetKey: typeof e.presetKey === 'string' ? e.presetKey : undefined,
            presetName: typeof e.presetName === 'string' ? e.presetName : undefined,
            presetParams:
              e.presetParams && typeof e.presetParams === 'object' ? e.presetParams : undefined,
          });
        }
      }
    }
  }
  if (items.length === 0 && fallbackContent) return [{ url: fallbackContent }];
  return items;
}

/**
 * 取第 idx 版展示用的提示词。这是「老数据零变化」的落点：条目自带就用自带的，
 * 没有（老条目）就回退到节点当前的 prompt——和改造前的表现逐字一致。
 */
export function historyPromptAt(
  items: GenHistoryItem[],
  idx: number,
  currentPrompt: string,
): string {
  return items[idx]?.prompt ?? currentPrompt;
}

/**
 * 追加一条新版本。`snap` 是提交那一刻的提示词/入参快照（见节点里的 `pendingGen`）——
 * 不能用「当前」的 prompt，因为生成期间用户完全可能已经改了输入框，那样记下来的
 * 提示词是错的，比不记更糟。
 *
 * 幂等：同 URL 已存在就原样返回。后端 sink 也会写同一条（用户关着页面时它是唯一
 * 的写入方），页面开着时两边都会写；不去重的话，快照被重新拉取过的那些时机就会
 * 长出重复版本。后端 `appendHistory` 里是同一套规则。
 */
export function appendHistoryItem(
  items: GenHistoryItem[],
  url: string,
  snap?: PendingGenSnapshot,
  at?: string,
): GenHistoryItem[] {
  if (items.some((h) => h.url === url)) return items;
  const item: GenHistoryItem = { url };
  if (snap?.prompt) item.prompt = snap.prompt;
  if (snap?.params && Object.keys(snap.params).length > 0) item.params = snap.params;
  if (at) item.at = at;
  // 预设快照跟着 prompt 一起进历史：这一版「用了哪个预设」也是提交那一刻的事实。
  if (snap?.presetKey) item.presetKey = snap.presetKey;
  if (snap?.presetName) item.presetName = snap.presetName;
  if (snap?.presetParams && Object.keys(snap.presetParams).length > 0) {
    item.presetParams = snap.presetParams;
  }
  return [...items, item];
}

/** 生成提交时暂存到 `settings.pendingGen` 的形状：这次提交用的提示词 + 入参 + 预设快照。 */
export interface PendingGenSnapshot {
  prompt?: string;
  /** 用户点「生成」的时刻（ISO）。计时条的起点，刷新页面后靠它续算而不是从 0 重来。 */
  submittedAt?: string;
  params?: Record<string, unknown>;
  presetKey?: string;
  presetName?: string;
  presetParams?: Record<string, string>;
}

/**
 * 读回 `settings.pendingGen`。这份快照在点「生成」时写入并持久化，所以刷新页面
 * 恢复轮询后依然取得到；形状不对就当没有（宁可退化成不记 prompt，也不能记错）。
 */
export function readPendingGen(raw: unknown): PendingGenSnapshot | undefined {
  if (!raw || typeof raw !== 'object') return undefined;
  const p = raw as Partial<PendingGenSnapshot>;
  const out: PendingGenSnapshot = {};
  if (typeof p.prompt === 'string') out.prompt = p.prompt;
  if (typeof p.submittedAt === 'string') out.submittedAt = p.submittedAt;
  if (p.params && typeof p.params === 'object') out.params = p.params as Record<string, unknown>;
  if (typeof p.presetKey === 'string') out.presetKey = p.presetKey;
  if (typeof p.presetName === 'string') out.presetName = p.presetName;
  if (p.presetParams && typeof p.presetParams === 'object') {
    out.presetParams = p.presetParams as Record<string, string>;
  }
  return Object.keys(out).length === 0 ? undefined : out;
}
