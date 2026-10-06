// priceNote 分档解析：让「按上游实际用量结算」模型的单价文案跟随当前选中的分辨率。
//
// 约定格式（后台 priceNote 字段直接填）：`480p:70积分/秒|720p:150积分/秒`
// —— 段用 | 分隔，每段 `分辨率:文案`。匹配键是模型 uiSchema 里 resolution 的选项值。
//
// 只有「含 | 且每段都有 :」才按分档表解析——单段或缺冒号一律当普通文案原样返回，
// 存量模型的 priceNote（如「100积分/分钟」）行为逐字不变。这行字不参与任何计费
// 计算（扣费走 billing_config），所以解析失败的最坏结果只是文案没切档，不会错价。
export function resolvePriceNote(note: string | undefined, resolution: string): string | undefined {
  if (!note) return note;
  const segments = note.split('|').map((s) => s.trim()).filter(Boolean);
  if (segments.length < 2) return note;
  const rows: Array<{ key: string; text: string }> = [];
  for (const seg of segments) {
    const idx = seg.indexOf(':');
    // 任一段缺冒号或键/文案为空 → 整串不是分档表，当普通文案。
    if (idx <= 0 || idx === seg.length - 1) return note;
    rows.push({ key: seg.slice(0, idx).trim(), text: seg.slice(idx + 1).trim() });
  }
  // 分辨率值大小写在 uiSchema 与配置间容易不一致（720p vs 720P），忽略大小写匹配。
  const want = resolution.trim().toLowerCase();
  const hit = rows.find((r) => r.key.toLowerCase() === want);
  if (hit) return hit.text;
  // 能解析成表但当前分辨率没配（管理员漏档/模型加了新档位）：把全部档位以可读形式
  // 列出，而不是把带 | 和 : 的原始配置串怼给用户。
  return rows.map((r) => `${r.key} ${r.text}`).join(' · ');
}
