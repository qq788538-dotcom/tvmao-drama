// 画布节点 patch 的**应用**语义，单一实现，store 的远端合并与持久化层的基线推进
// 共用——两处各写一份的话，同一条 patch 在「本地显示」和「基线」上算出不同结果，
// diff 就会开始发出莫名其妙的字段。
//
// 规则 = RFC 7386 JSON Merge Patch，与后端 `apps/api/internal/canvasstate/state.go`
// 的 `mergeRawValue` / `applyMergePatch` 一一对应（两侧同批上线，各有金测试钉住
// `taskId: null` 与嵌套 `settings.error: null` 的删键行为）：
//   - 两边都是普通对象 → 按 key 递归合并；
//   - patch 值为 `null` → **删除该 key**（不是把值写成 null）；
//   - 其余（数组、标量、Date 之类非普通对象）→ 整体替换。
//
// 为什么要 null=删键：节点上有一批「阶段性字段」——taskId、settings.pendingGen
// 这类，生成结束就该消失。没有删键语义时，前端只能整节点 upsert 才能表达「这个
// 字段没了」，而整节点 upsert 正是并发编辑里最伤的一击（把队友刚改的字段一起写回
// 旧值）。生成回写是最热的一条路径，不能让它退化。
export function applyMergePatch(target: unknown, patch: unknown): unknown {
  if (!isPlainRecord(target) || !isPlainRecord(patch)) return patch;
  const merged: Record<string, unknown> = { ...target };
  for (const [key, value] of Object.entries(patch)) {
    if (value === null) {
      delete merged[key];
      continue;
    }
    merged[key] = applyMergePatch(merged[key], value);
  }
  return merged;
}

function isPlainRecord(value: unknown): value is Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}
