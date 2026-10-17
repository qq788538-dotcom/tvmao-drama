import { useMemo, useRef, useState } from 'react';
import { GripVertical, MoreHorizontal, Plus } from 'lucide-react';
import { useScriptSettings } from './useScriptSettings';
import { DurationEditor, RowMenu, TextCellEditor } from './cellEditors';
import { parseTokens } from './scriptTokens';
import { ScriptPromptInline, type ScriptAssetOption } from './ScriptPromptEditor';
import { useCanvasStore } from '../../store';
import { MAX_NAME_LEN, MIN_SHOT_SECONDS, type ColorMark, type ScriptRow, type ScriptAssets } from './scriptData';

// 分镜表（skill 对齐版）：5 列——镜号 / 分镜简述(name≤15字) / 时长(4~durationSec) /
// 提示词(prompt 唯一真源，点开大编辑器) / 操作。景别/运镜/对白/音效等散字段列已随
// skill 对齐移除（全部活在 prompt 的镜头N行里）。
const HEADERS = ['镜号', '分镜简述', '时长', '提示词', '操作'];

type CellField = 'name' | 'duration';

const ROW_COLOR_BAR: Record<ColorMark, string | undefined> = {
  '': undefined,
  red: '#ef4444',
  yellow: '#eab308',
  green: '#22c55e',
  blue: '#3b82f6',
  gray: '#6b7280',
};

// 行级红点校验（不阻塞保存）：skill 的硬约束在前端只做提醒——手写模式要宽松，
// skill 产物本来就过了 validate_storyboard.py。
export function rowIssues(row: ScriptRow, assets: ScriptAssets, durationSec: number): string[] {
  const issues: string[] = [];
  if (row.name.length > MAX_NAME_LEN) issues.push(`分镜简述超 ${MAX_NAME_LEN} 字`);
  if (row.duration < MIN_SHOT_SECONDS || row.duration > durationSec) {
    issues.push(`时长需在 ${MIN_SHOT_SECONDS}~${durationSec} 秒`);
  }
  const broken = parseTokens(row.prompt, assets).filter((r) => !r.asset);
  if (broken.length > 0) {
    const names = [...new Set(broken.map((b) => b.name))];
    issues.push(`引用断链：${names.join('、')}`);
  }
  return issues;
}

export function ShotTable({
  nodeId,
  onOpenPrompt,
  onNextStep,
  readOnly = false,
}: {
  nodeId: string;
  /** 点提示词列 → 打开 PromptEditorDialog（由 ScriptPanel 统一挂载） */
  onOpenPrompt: (rowId: string) => void;
  /** 底栏「→ 下一步」；不传则不渲染该按钮 */
  onNextStep?: () => void;
  readOnly?: boolean;
}) {
  const { settings, updateRow, addRow, removeRow, moveRow } = useScriptSettings(nodeId);
  const nodes = useCanvasStore((s) => s.nodes);
  // 提示词列 chip 渲染用的资产选项（url = linkedNode 的图，节点已删按无图处理）。
  const assetOptions = useMemo<ScriptAssetOption[]>(() => {
    const nodeUrl = (id: string) => (nodes.find((n) => n.id === id)?.content as string | undefined) || undefined;
    return [
      ...settings.assets.characters.map((a) => ({ kind: 'character' as const, name: a.name, url: nodeUrl(a.linkedNodeId) })),
      ...settings.assets.scenes.map((a) => ({ kind: 'scene' as const, name: a.name, url: nodeUrl(a.linkedNodeId) })),
      ...settings.assets.props.map((a) => ({ kind: 'prop' as const, name: a.name, url: nodeUrl(a.linkedNodeId) })),
    ];
  }, [settings.assets, nodes]);
  const [openCell, setOpenCell] = useState<{ rowId: string; field: CellField } | null>(null);
  const [openMenuRowId, setOpenMenuRowId] = useState<string | null>(null);
  const draggingRowId = useRef<string | null>(null);

  const toggleCell = (rowId: string, field: CellField) => {
    if (readOnly) return;
    setOpenCell((prev) => (prev && prev.rowId === rowId && prev.field === field ? null : { rowId, field }));
  };

  const toggleRowMenu = (rowId: string) => {
    if (readOnly) return;
    setOpenMenuRowId((id) => (id === rowId ? null : rowId));
  };

  const handleDrop = (targetRowId: string) => {
    const fromId = draggingRowId.current;
    draggingRowId.current = null;
    if (!fromId || fromId === targetRowId) return;
    const toIndex = settings.rows.findIndex((r) => r.id === targetRowId);
    if (toIndex === -1) return;
    moveRow(fromId, toIndex);
  };

  const activeRowId = openCell?.rowId ?? openMenuRowId;
  const cellActive = (rowId: string, field: CellField) =>
    openCell?.rowId === rowId && openCell.field === field ? ' outline outline-2 outline-white' : '';

  // 每行的校验问题——rows/assets/durationSec 变了才重算，别逐帧跑正则。
  const issuesByRow = useMemo(() => {
    const m = new Map<string, string[]>();
    for (const r of settings.rows) m.set(r.id, rowIssues(r, settings.assets, settings.durationSec));
    return m;
  }, [settings.rows, settings.assets, settings.durationSec]);

  return (
    <div className="flex h-full flex-col">
      <div className="min-h-0 flex-1 overflow-x-auto overflow-y-auto p-4">
        <div className="min-w-[900px] overflow-hidden rounded-lg border border-white/10">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="text-left text-xs text-muted-foreground">
              {HEADERS.map((h) => (
                <th key={h} className="whitespace-nowrap border border-white/10 bg-white/[0.04] px-3 py-2.5 font-medium">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {settings.rows.map((row) => {
              const issues = issuesByRow.get(row.id) ?? [];
              return (
                <tr
                  key={row.id}
                  data-testid={`shot-row-${row.shotNumber}`}
                  onDragOver={(e) => e.preventDefault()}
                  onDrop={() => handleDrop(row.id)}
                  className={'align-top' + (activeRowId === row.id ? ' bg-white/[0.04]' : '')}
                  style={row.colorMark ? { boxShadow: `inset 3px 0 0 ${ROW_COLOR_BAR[row.colorMark]}` } : undefined}
                >
                  {/* 镜号 + 拖拽手柄 */}
                  <td className="w-16 border border-white/10 px-3 py-2">
                    <div className="flex items-center gap-1.5">
                      <span
                        draggable={!readOnly}
                        onDragStart={() => { if (!readOnly) draggingRowId.current = row.id; }}
                        className={
                          'text-muted-foreground '
                          + (readOnly ? 'cursor-default opacity-50' : 'cursor-grab hover:text-foreground')
                        }
                      >
                        <GripVertical size={14} />
                      </span>
                      <span className="tabular-nums text-foreground">{row.shotNumber}</span>
                    </div>
                  </td>

                  {/* 分镜简述 name（≤15 字红点） */}
                  <td
                    data-testid={`cell-name-${row.id}`}
                    className={'relative w-52 cursor-pointer border border-white/10 px-3 py-2' + cellActive(row.id, 'name')}
                    onClick={() => toggleCell(row.id, 'name')}
                  >
                    <span className={row.name.length > MAX_NAME_LEN ? 'text-red-400' : 'text-foreground'}>
                      {row.name || <span className="text-muted-foreground/50">+</span>}
                    </span>
                    {openCell?.rowId === row.id && openCell.field === 'name' && (
                      <TextCellEditor
                        value={row.name}
                        rows={1}
                        widthClass="w-[360px]"
                        placeholder={`分镜简述（≤${MAX_NAME_LEN}字）`}
                        hint={`≤${MAX_NAME_LEN} 字；失焦自动保存`}
                        onSave={(v) => updateRow(row.id, { name: v })}
                        onClose={() => setOpenCell(null)}
                      />
                    )}
                  </td>

                  {/* 时长 duration */}
                  <td
                    data-testid={`cell-duration-${row.id}`}
                    className={'relative w-20 cursor-pointer border border-white/10 px-3 py-2' + cellActive(row.id, 'duration')}
                    onClick={() => toggleCell(row.id, 'duration')}
                  >
                    <span className="tabular-nums text-foreground">{row.duration}s</span>
                    {openCell?.rowId === row.id && openCell.field === 'duration' && (
                      <DurationEditor
                        value={row.duration}
                        max={settings.durationSec}
                        onSave={(v) => updateRow(row.id, { duration: v })}
                        onClose={() => setOpenCell(null)}
                      />
                    )}
                  </td>

                  {/* 提示词：首行截断展示 + 红点问题，点开大编辑器 */}
                  <td
                    data-testid={`cell-prompt-${row.id}`}
                    className="cursor-pointer border border-white/10 px-3 py-2"
                    onClick={() => onOpenPrompt(row.id)}
                  >
                    <div className="flex items-start gap-2">
                      <span className="line-clamp-2 min-w-0 flex-1 text-foreground">
                        {row.prompt.trim() ? (
                          <ScriptPromptInline prompt={row.prompt} options={assetOptions} />
                        ) : (
                          <span className="text-muted-foreground/50">+</span>
                        )}
                      </span>
                      {issues.length > 0 && (
                        <span
                          data-testid={`row-issues-${row.id}`}
                          title={issues.join('\n')}
                          className="mt-1 h-2 w-2 shrink-0 rounded-full bg-red-500"
                        />
                      )}
                    </div>
                  </td>

                  {/* 操作：行菜单 */}
                  <td className="relative w-16 border border-white/10 px-3 py-2">
                    <button
                      type="button"
                      data-testid={`row-menu-${row.id}`}
                      disabled={readOnly}
                      onClick={() => toggleRowMenu(row.id)}
                      className="rounded bg-transparent border-none cursor-pointer p-1 text-muted-foreground hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:bg-transparent"
                      aria-label="行菜单"
                    >
                      <MoreHorizontal size={16} />
                    </button>
                    {openMenuRowId === row.id && (
                      <RowMenu
                        onMark={(c) => updateRow(row.id, { colorMark: c })}
                        onClear={() => updateRow(row.id, { colorMark: '' })}
                        onDelete={() => removeRow(row.id)}
                        onClose={() => setOpenMenuRowId(null)}
                      />
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        </div>
      </div>

      <div className="flex items-center justify-between border-t border-border px-4 py-2.5">
        <button
          type="button"
          data-testid="script-add-shot"
          disabled={readOnly}
          onClick={() => addRow()}
          className="flex items-center gap-1 rounded bg-transparent border-none cursor-pointer px-2 py-1.5 text-sm text-foreground hover:text-blue-400 transition-colors disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Plus size={15} /> 添加分镜
        </button>
        {onNextStep && (
          <button
            type="button"
            onClick={onNextStep}
            className="rounded-lg bg-foreground px-4 py-2 text-sm font-medium text-background hover:opacity-90 border-none cursor-pointer transition-opacity"
          >
            → 下一步：准备资产
          </button>
        )}
      </div>
    </div>
  );
}
