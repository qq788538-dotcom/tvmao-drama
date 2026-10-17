import { useEffect, useMemo, useState } from 'react';
import { AlertTriangle, X } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { listRuntimeModels, type UISchemaField } from '@/shared/api';
import { useScriptSettings } from './useScriptSettings';
import { MIN_SHOT_SECONDS, type ScriptRow } from './scriptData';
import { pickClosestDurationOption, spawnVideoNodes, spawnedCountForRow, translateRow, type BatchVideoItem } from './batchVideo';
import { LABEL_BY_KIND } from './scriptTokens';
import { SAVE_BTN } from './cellEditors';
import { ScriptSelect } from './ScriptSelect';

// 批量生视频弹层：顶部提示条 + 副说明 + 每镜大行卡（勾选/提示词
// 截断/右侧时长控件）+ 底栏（全选计数 | 模型 | uiSchema 参数下拉 | 确认按钮）。
// 只铺节点不提交生成（铁律）；底栏参数按所选模型 uiSchema 动态渲染，选择值
// 随 spawn 写进各 gen-video 节点 settings。
export function BatchVideoDialog({ nodeId, onClose }: { nodeId: string; onClose: () => void }) {
  const { settings } = useScriptSettings(nodeId);
  const rows = settings.rows;

  const modelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 60_000,
  });
  const videoModels = useMemo(
    () => (modelsQuery.data ?? []).filter((m) => m.type === 'video'),
    [modelsQuery.data],
  );

  const rowInfo = useMemo(
    () =>
      new Map(
        rows.map((r) => [
          r.id,
          {
            translated: translateRow(r, settings.assets),
            spawned: spawnedCountForRow(r.id),
            spawnable: r.prompt.trim() !== '',
          },
        ]),
      ),
    [rows, settings.assets],
  );

  const selectableRowIds = useMemo(
    () => new Set(rows.filter((r) => rowInfo.get(r.id)?.spawnable).map((r) => r.id)),
    [rows, rowInfo],
  );
  const [selected, setSelected] = useState<Set<string>>(
    () => new Set(rows.filter((r) => {
      const info = rowInfo.get(r.id);
      return info?.spawnable && info.translated.missing.length === 0;
    }).map((r) => r.id)),
  );
  const [durations, setDurations] = useState<Record<string, string | number>>({});
  const [modelId, setModelId] = useState('');
  // 底栏 uiSchema 参数（比例/分辨率等枚举字段）的用户选择；换模型清空回默认。
  const [dyn, setDyn] = useState<Record<string, unknown>>({});

  const selectedModel = videoModels.find((m) => m.modelId === modelId);
  const uiSchema = selectedModel?.uiSchema ?? {};
  const durationField = uiSchema.duration;
  // duration 单独走右侧每镜控件，其余枚举字段进底栏下拉。
  const paramEntries = useMemo(
    () =>
      (Object.entries(uiSchema).filter(([k, v]) => k !== 'duration' && v != null && ((v as UISchemaField).options?.length ?? 0) > 0)) as Array<[string, UISchemaField]>,
    [uiSchema],
  );
  const paramValues = useMemo(() => {
    const out: Record<string, unknown> = {};
    for (const [key, field] of paramEntries) out[key] = dyn[key] ?? field.default;
    return out;
  }, [paramEntries, dyn]);

  const effectiveDuration = (row: ScriptRow): string | number | undefined => {
    if (durations[row.id] !== undefined) return durations[row.id];
    if (!durationField) return undefined;
    if ((durationField.options?.length ?? 0) > 0) return pickClosestDurationOption(durationField, row.duration);
    return row.duration;
  };

  useEffect(() => {
    if (!modelId && videoModels.length > 0) setModelId(videoModels[0].modelId);
  }, [modelId, videoModels]);

  const toggleRow = (rowId: string) => {
    if (!selectableRowIds.has(rowId)) return;
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(rowId)) next.delete(rowId);
      else next.add(rowId);
      return next;
    });
  };

  const allSelected = selectableRowIds.size > 0 && [...selectableRowIds].every((id) => selected.has(id));
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(selectableRowIds));

  const handleConfirm = () => {
    const items: BatchVideoItem[] = rows
      .filter((r) => selected.has(r.id) && selectableRowIds.has(r.id))
      .map((r) => ({ rowId: r.id, duration: effectiveDuration(r) }));
    if (items.length === 0 || !modelId) return;
    spawnVideoNodes(nodeId, items, modelId, paramValues);
    onClose();
  };

  return (
    <div className="script-borders fixed inset-0 z-40 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div
        className="flex max-h-[88vh] w-[min(1100px,94vw)] flex-col overflow-hidden rounded-2xl border border-white/10 bg-[#171717] shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-white/10 px-5 py-4">
          <h2 className="text-base font-medium text-foreground">批量生视频</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="关闭"
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground bg-transparent border-none cursor-pointer"
          >
            <X size={18} />
          </button>
        </div>

        <div className="flex min-h-0 flex-1 flex-col gap-2.5 px-5 py-4">
          <div className="rounded-lg border border-white/10 bg-white/[0.03] px-4 py-3 text-sm text-foreground">
            会优先使用已生成的角色、场景和物品参考图，让视频更贴合分镜内容
          </div>
          <p className="text-xs text-muted-foreground">
            此处只创建视频节点并连好参考图，不会提交生成。每镜时长为本次创建临时值，不写回脚本表格。
          </p>

          <div className="min-h-0 flex-1 space-y-2 overflow-y-auto">
            {rows.map((row) => {
              const info = rowInfo.get(row.id)!;
              const missing = info.translated.missing;
              const dur = effectiveDuration(row);
              return (
                <label
                  key={row.id}
                  className="flex flex-col gap-1 rounded-xl border border-white/10 px-3.5 py-3"
                >
                  <div className="flex items-center gap-3">
                    <input
                      type="checkbox" style={{ accentColor: "#fff" }}
                      checked={selected.has(row.id)}
                      disabled={!info.spawnable}
                      onChange={() => toggleRow(row.id)}
                    />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                        镜头 {row.shotNumber}
                        {row.name && <span className="text-xs font-normal text-muted-foreground">{row.name}</span>}
                        {info.spawned > 0 && (
                          <span className="rounded bg-white/10 px-1.5 py-0.5 text-[11px] font-normal text-muted-foreground">
                            已创建过 {info.spawned} 个
                          </span>
                        )}
                      </div>
                      <div className="mt-0.5 truncate text-xs text-muted-foreground" title={row.prompt}>
                        {info.spawnable ? row.prompt.replace(/\n/g, ' ') : '提示词为空，无法创建'}
                      </div>
                      {info.spawnable && missing.length > 0 && (
                        <div data-testid={`row-missing-${row.id}`} className="mt-1 flex items-center gap-1.5 text-[11px] text-yellow-400">
                          <AlertTriangle size={12} className="shrink-0" />
                          缺设定图：{missing.map((m) => `${LABEL_BY_KIND[m.kind]}-${m.name}`).join('、')}（强选则按纯文字处理）
                        </div>
                      )}
                    </div>
                    {durationField && ((durationField.options?.length ?? 0) > 0 ? (
                      <ScriptSelect
                        ariaLabel={`第${row.shotNumber}镜时长`}
                        value={String(dur ?? '')}
                        disabled={!info.spawnable}
                        onChange={(v) => setDurations((prev) => ({ ...prev, [row.id]: v }))}
                        options={(durationField.options ?? []).map((o) => ({ value: String(o.value), label: o.label }))}
                        className="w-24 shrink-0"
                      />
                    ) : (
                      <input
                        type="number"
                        aria-label={`第${row.shotNumber}镜时长`}
                        min={MIN_SHOT_SECONDS}
                        max={settings.durationSec}
                        value={Number(dur ?? row.duration)}
                        disabled={!info.spawnable}
                        onChange={(e) => setDurations((prev) => ({ ...prev, [row.id]: Number(e.target.value) }))}
                        className="w-20 shrink-0 rounded-lg border border-white/10 bg-black/40 px-2 py-1.5 text-sm text-foreground [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                      />
                    ))}
                  </div>
                </label>
              );
            })}
          </div>
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-white/10 px-5 py-3">
          <div className="flex min-w-0 flex-wrap items-center gap-3">
            <label className="flex shrink-0 items-center gap-1.5 text-sm text-foreground">
              <input type="checkbox" style={{ accentColor: "#fff" }} checked={allSelected} onChange={toggleAll} />
              已选 {selected.size}/{selectableRowIds.size}
            </label>
            <ScriptSelect
              ariaLabel="视频模型"
              value={modelId}
              onChange={(v) => { setModelId(v); setDyn({}); setDurations({}); }}
              options={videoModels.length === 0
                ? [{ value: '', label: '暂无可用视频模型' }]
                : videoModels.map((m) => ({ value: m.modelId, label: m.name }))}
              className="max-w-56"
            />
            {/* 所选模型的 uiSchema 枚举参数（比例/分辨率等）——字段名不硬编码，
                schema 驱动渲染；选择值随 spawn 写进各节点 settings。 */}
            {paramEntries.map(([key, field]) => (
              <ScriptSelect
                key={key}
                ariaLabel={field.label || key}
                value={String(paramValues[key] ?? '')}
                onChange={(v) => setDyn((prev) => ({ ...prev, [key]: v }))}
                options={(field.options ?? []).map((o) => ({ value: String(o.value), label: o.label }))}
              />
            ))}
          </div>
          <button
            type="button"
            data-testid="batch-video-confirm"
            disabled={selected.size === 0 || !modelId}
            onClick={handleConfirm}
            className={`${SAVE_BTN} shrink-0 disabled:cursor-not-allowed disabled:opacity-50`}
          >
            确认并创建视频生成器组 ({selected.size})
          </button>
        </div>
      </div>
    </div>
  );
}
