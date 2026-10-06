import { useEffect, useRef, useState } from 'react';
import { Check, X } from 'lucide-react';
import { useCanvasStore } from '../../store';
import { scriptStepStats } from './scriptData';
import { useScriptSettings } from './useScriptSettings';
import { ShotTable } from './ShotTable';
import { AssetsPanel } from './AssetsPanel';
import { PromptEditorDialog } from './PromptEditorDialog';

type ScriptStep = 1 | 2;

// 步骤条 2 项元信息（skill 对齐版两步制：确认分镜 → 准备资产；原第三步「合成
// 提示词」已随 prompt 单真源化移除）。与 ScriptNode 卡片共用 scriptStepStats。
function stepMeta(stats: ReturnType<typeof scriptStepStats>) {
  return [
    {
      step: 1 as const,
      label: '确认分镜',
      sub: stats.shots > 0 ? `${stats.shots}个分镜待核对` : '暂无分镜',
      done: stats.shots > 0,
    },
    {
      step: 2 as const,
      label: '准备资产',
      sub: stats.assetsTotal > 0 ? `${stats.assetsReady}/${stats.assetsTotal} 已配图` : '暂无资产',
      done: stats.assetsTotal > 0 && stats.assetsReady === stats.assetsTotal,
    },
  ];
}

export function ScriptPanel({ nodeId, onClose }: { nodeId: string; onClose: () => void }) {
  const { settings, addRow } = useScriptSettings(nodeId);
  const stats = scriptStepStats(settings);
  const readOnly = useCanvasStore((s) => s.readOnly);
  const [step, setStep] = useState<ScriptStep>(1);
  // 提示词大编辑器在面板层挂一份，ShotTable 只上报 rowId。
  const [promptRowId, setPromptRowId] = useState<string | null>(null);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  // 打开面板时空表自动补一行：进来就有第 1 镜可编辑。只补一次（用户删光是明确意图）。
  const seededRef = useRef(false);
  useEffect(() => {
    if (seededRef.current || readOnly) return;
    seededRef.current = true;
    if (settings.rows.length === 0) addRow();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅挂载时判定一次
  }, []);

  const meta = stepMeta(stats);
  const doneSteps = meta.filter((m) => m.done).length;

  return (
    <div data-testid="script-panel" className="script-borders fixed inset-0 z-50 flex flex-col overflow-hidden bg-[#171717] text-foreground">
      <div className="grid grid-cols-[1fr_auto_1fr] items-center px-4 py-2.5">
        <div />
        <div className="flex items-center">
          {meta.map(({ step: s, label, sub, done }, i) => (
            <div key={s} className="flex items-center">
              {i > 0 && <div className="mx-3 h-px w-24 bg-border" />}
              <button
                type="button"
                data-testid={`script-step-${s}`}
                onClick={() => setStep(s)}
                className={
                  'flex items-center gap-2.5 rounded-xl px-3 py-1.5 text-left transition-colors border-none cursor-pointer '
                  + (step === s ? 'bg-white/[0.08]' : 'bg-transparent hover:bg-white/[0.05]')
                }
              >
                <span
                  className={
                    done
                      ? 'flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-foreground text-background'
                      : 'flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-muted-foreground/50 text-sm text-foreground'
                  }
                >
                  {done ? <Check size={15} strokeWidth={3} /> : s}
                </span>
                <span className="flex flex-col">
                  <span className={'text-sm font-medium ' + (step === s ? 'text-foreground' : 'text-muted-foreground')}>{label}</span>
                  <span className="text-xs text-muted-foreground">{sub}</span>
                </span>
              </button>
            </div>
          ))}
        </div>
        <div className="flex items-center justify-end gap-3">
          <span className="text-xs text-muted-foreground">{doneSteps}/2 完成后可批量生视频</span>
          <button
            type="button"
            data-testid="script-panel-close"
            onClick={onClose}
            className="rounded p-1.5 hover:bg-accent bg-transparent border-none cursor-pointer text-foreground"
            aria-label="关闭脚本面板"
          >
            <X size={16} />
          </button>
        </div>
      </div>
      <div className="flex-1 min-h-0 overflow-auto">
        {step === 1 && (
          <div data-testid="script-step-shots" className="h-full">
            <ShotTable nodeId={nodeId} onOpenPrompt={setPromptRowId} onNextStep={() => setStep(2)} readOnly={readOnly} />
          </div>
        )}
        {step === 2 && (
          <div data-testid="script-step-assets" className="h-full">
            <AssetsPanel nodeId={nodeId} onClose={onClose} readOnly={readOnly} />
          </div>
        )}
      </div>

      {promptRowId && (
        <PromptEditorDialog nodeId={nodeId} rowId={promptRowId} onClose={() => setPromptRowId(null)} readOnly={readOnly} />
      )}
    </div>
  );
}
