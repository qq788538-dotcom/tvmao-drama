// 团队项目的 ownerToken：**不再是编辑权凭据，而是本标签页的来源指纹**。
//
// 二期后端对团队身份（actor>0）跳过 owner.Require——并发写由 MySQL 行锁串行化，
// 不需要独占锁。但 ownerToken 还有一个不能丢的用途：服务端把它末段的前 8 位
// （canvasowner.SourceMark）放进 canvas_changed 帧的 source_mark，接收端据此
// 认出「这一帧是我自己刚写的」，跳过本地重放。
//
// **所以它必须每标签页唯一**。计划原文写的是所有团队客户端统一传常量 "team"，
// 那样全体成员的 source_mark 会撞成同一个值 → 每个人都把队友的 op 当成自己的
// 回声跳过 → 协作静默失效，一条报错都没有（T1 报告第二节实锤了这一条）。
//
// 形状 `team:<8位hex>`：前缀让服务端日志/排查一眼看出这是团队直写路径，末段
// 才是参与指纹计算的随机值（ownerTokenMark 取的是 split(':').pop() 的前 8 位）。

/** 生成一个本标签页专属的团队 ownerToken。每次调用都是新值——同一个页面实例
 *  只该调一次并存进 ref，重复生成会让「自己的回声」认不出来。 */
export function newTeamOwnerToken(): string {
  return `team:${randomHex8()}`;
}

function randomHex8(): string {
  const cryptoObj = globalThis.crypto;
  if (cryptoObj?.getRandomValues) {
    const bytes = new Uint8Array(4);
    cryptoObj.getRandomValues(bytes);
    return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  }
  // 兜底：老 WebView / 非安全上下文没有 WebCrypto。指纹撞车的后果只是漏掉一次
  // 回声检测（多合并一次自己的改动，结果幂等），不值得为它把功能整个停掉。
  return Math.floor(Math.random() * 0x1_0000_0000).toString(16).padStart(8, '0');
}
