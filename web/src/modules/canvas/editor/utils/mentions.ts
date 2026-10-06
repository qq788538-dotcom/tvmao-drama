/**
 * 提示词里 @ 提及的语法与纯字符串操作。
 *
 * 从 PromptMentionEditor.tsx 抽出来，是因为 store 拷贝节点时也要重写提及里的
 * nodeId，而 store 是纯逻辑，不该去 import 一个 React 组件文件。正则留一份，
 * 两边共用——各写各的迟早会走样。
 *
 * 形如 `@[图片:n-abc123]`：方括号里是「类型标签:上游节点 id」。绑 nodeId 而不是
 * 序号，因为连线增删时序号会漂移，id 不会。
 *
 * 可选第三段 `|<原文>`：`@[图片:n-abc123|张三]` / `@[图片:n-abc123|[角色-张三]]`。
 * 原文是这个 chip 由哪句文字引用自动转换而来（`@张三` / `@[角色-张三]` 去掉 `@`
 * 后的字面），断线时靠它还原成原来的写法而不是把引用删掉——没有这一段，用户从
 * 脚本粘进来的 `@张三` 一断线就永久丢失。原文允许恰好一层方括号（`[角色-张三]`
 * 这种写法本身带括号），nodeId 段禁 `|` 与 `]`。
 * 用户从 @ 菜单手选的 chip 没有这一段，行为不变（断线即删）。
 */
export const MENTION_RE = /@\[(图片|视频|音频):([^\]|]+)(?:\|((?:\[[^\]]*\]|[^\]])*))?\]/g;

/**
 * 把提及指向的 nodeId 按 idMap 改写，映射表里没有的原样保留。
 *
 * 拷贝节点时用：同一批被复制的节点之间，连线会在副本之间重建，提及也必须跟着
 * 指向副本，否则副本的提示词引用的还是原节点——而副本并没有连着它，断线清理
 * （stripDeadMentions）会把这些标记默默删掉，用户的提示词内容就被改了。
 *
 * 只复制单个节点时映射表里只有它自己，上游连线的 from 仍是原节点，提及照常有效。
 */
export function remapMentionNodeIds(value: string, idMap: Map<string, string>): string {
  if (!value || idMap.size === 0) return value;
  return value.replace(MENTION_RE, (whole, kindLabel: string, nodeId: string, origin?: string) => {
    const next = idMap.get(nodeId);
    return next ? mentionToken(kindLabel, next, origin) : whole;
  });
}

/** 拼一个标记；origin 为空时不带 `|` 段，与存量格式逐字一致。 */
export function mentionToken(kindLabel: string, nodeId: string, origin?: string): string {
  return origin ? `@[${kindLabel}:${nodeId}|${origin}]` : `@[${kindLabel}:${nodeId}]`;
}
