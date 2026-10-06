import { useMemo, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { currentProjectId, listRuntimeModels, submitCanvasTask, type UISchemaField } from '@/shared/api';
import { useCanvasStore } from '../../store';
import { persistNodeById } from '../../persistence/canvasMutations';
import { uploadFileIntoNode } from '../../utils/uploadIntoNode';
import { buildImageGenerationInputs } from '../../utils/generationInputs';
import { useScriptSettings } from './useScriptSettings';
import { createGenAssetNode, createUploadAssetNode, linkExistingAssetNode } from './assetNodeOps';
import { ScriptSelect } from './ScriptSelect';
import type { ScriptAsset, ScriptAssets } from './scriptData';
import { SAVE_BTN } from './cellEditors';
import { nanoid } from 'nanoid';
import { ThumbImg } from '../../ThumbImg';

type SectionKey = keyof ScriptAssets;
type Tab = 'ai' | 'canvas' | 'upload';

// 新增资产弹窗（对标上游）：三条配图路一次收口——AI生成 / 从当前画布选择 / 本地上传。
// 三条路殊途同归：最终都是「一个画布图片节点 + 一条到脚本节点的连线 + 一条带
// linkedNodeId 的资产记录」。名称自动带出（prompt 截断/节点名/文件名），同类型
// 内自动去重加序号；描述留给资产编辑弹窗后改。
//
// 「确认生成」会直接提交生成任务——这是用户对单张图的显式生成动作（等同在
// gen-image 节点上点生成），不属于「批量入口不得自动提交」铁律的范围。
export function AddAssetDialog({
  nodeId,
  section,
  sectionLabel,
  replaceAsset,
  onBind,
  onClose,
  initialTab,
}: {
  nodeId: string;
  section: SectionKey;
  sectionLabel: string;
  /** 传入即为「替换图片」模式：标题变「选择图片（资产名）」，三条路不建新资产，
   *  产生的新绑定经 onBind 回给父组件（父组件负责摘旧连线 + 换绑 linkedNodeId）。 */
  replaceAsset?: ScriptAsset;
  onBind?: (newNodeId: string) => void;
  onClose: () => void;
  /** 打开时定位到哪个 tab。「AI 生角色/场景/物品」入口传 'ai'（预填资产描述词），
   *  其余入口默认「从当前画布选择」（用户拍板的默认项）。 */
  initialTab?: Tab;
}) {
  const { settings, patch } = useScriptSettings(nodeId);
  const nodes = useCanvasStore((s) => s.nodes);
  // 默认停在「从当前画布选择」——对标上游：画布上现成的图是最常用的配图来源。
  const [tab, setTab] = useState<Tab>(initialTab ?? 'canvas');
  const isReplace = !!replaceAsset;

  // ---- 通用：建资产记录（名称同类型去重：重名自动加 2/3/… 序号后缀） ----
  const appendAsset = (name: string, description: string, linkedNodeId: string) => {
    const base = (name || sectionLabel).replace(/[[\]]/g, '').trim() || sectionLabel;
    const existing = new Set(settings.assets[section].map((a) => a.name));
    let unique = base;
    for (let i = 2; existing.has(unique); i++) unique = `${base}${i}`;
    const created: ScriptAsset = { id: nanoid(8), name: unique, description, linkedNodeId };
    patch({ assets: { ...settings.assets, [section]: [...settings.assets[section], created] } });
  };

  // ---- Tab 1：AI 生成 ----
  const [prompt, setPrompt] = useState(replaceAsset?.description ?? '');
  const [modelId, setModelId] = useState('');
  const [dyn, setDyn] = useState<Record<string, unknown>>({});
  const modelsQuery = useQuery({ queryKey: ['runtime-models'], queryFn: listRuntimeModels, staleTime: 60_000 });
  const imageModels = useMemo(() => (modelsQuery.data ?? []).filter((m) => m.type === 'image'), [modelsQuery.data]);
  const selectedModel = imageModels.find((m) => m.modelId === modelId) ?? imageModels[0];
  const effectiveModelId = selectedModel?.modelId ?? '';
  // uiSchema 的枚举字段全部渲染成底栏下拉（画质/分辨率/比例……不 hardcode 字段名，
  // 跟 GenImageNode 一样由模型 schema 驱动），默认值取 field.default。
  const schemaEntries = useMemo(
    () => Object.entries(selectedModel?.uiSchema ?? {}).filter(([, v]) => v != null) as Array<[string, UISchemaField]>,
    [selectedModel],
  );
  const dynamicInputs = useMemo(() => {
    const out: Record<string, unknown> = {};
    for (const [key, field] of schemaEntries) out[key] = dyn[key] ?? field.default;
    return out;
  }, [schemaEntries, dyn]);

  const [submitting, setSubmitting] = useState(false);
  const handleGenerate = async () => {
    if (!prompt.trim() || !effectiveModelId || submitting) return;
    setSubmitting(true);
    const index = settings.assets.characters.length + settings.assets.scenes.length + settings.assets.props.length;
    const newId = createGenAssetNode(nodeId, index, prompt, { modelId: effectiveModelId, ...dynamicInputs });
    if (!newId) { setSubmitting(false); return; }
    if (isReplace) onBind?.(newId);
    else appendAsset(prompt.slice(0, 12), prompt, newId);
    const store = useCanvasStore.getState();
    store.updateNode(newId, { status: 'generating', error: undefined });
    persistNodeById(newId);
    try {
      const result = await submitCanvasTask({
        projectId: currentProjectId()!,
        nodeId: newId,
        modelId: effectiveModelId,
        inputs: buildImageGenerationInputs({ prompt, fallbackPrompt: '', dynamicInputs, connectedImages: [] }),
      });
      // taskId 落库后 gen-image 节点自己的 polling effect 接管进度与回写。
      useCanvasStore.getState().updateNode(newId, { taskId: result.taskId });
      persistNodeById(newId);
    } catch (err) {
      const msg = (err as { response?: { data?: { error?: string } }; message?: string })?.response?.data?.error
        ?? (err as Error)?.message ?? '提交生成失败';
      useCanvasStore.getState().updateNode(newId, { status: 'failed', error: msg });
      persistNodeById(newId);
    }
    onClose();
  };

  // ---- Tab 2：从当前画布选择（有图的图片类节点） ----
  const pickableNodes = useMemo(
    () => nodes.filter((n) => (n.type === 'input-image' || n.type === 'gen-image') && typeof n.content === 'string' && n.content),
    [nodes],
  );
  const nodeLabel = (n: (typeof nodes)[number], i: number): string =>
    ((n.settings?.name as string | undefined)?.trim())
    || (n.type === 'gen-image' ? `图片${i + 1}` : `图片${i + 1}`);

  const handlePick = (imageNodeId: string, label: string) => {
    if (isReplace && imageNodeId === replaceAsset?.linkedNodeId) return; // 当前图不可重选
    if (!linkExistingAssetNode(nodeId, imageNodeId)) return;
    if (isReplace) onBind?.(imageNodeId);
    else appendAsset(label, '', imageNodeId);
    onClose();
  };

  // ---- Tab 3：本地上传 ----
  const fileInputRef = useRef<HTMLInputElement>(null);
  const handleFiles = async (files: FileList | null) => {
    const file = files?.[0];
    if (!file) return;
    const index = settings.assets.characters.length + settings.assets.scenes.length + settings.assets.props.length;
    const newId = createUploadAssetNode(nodeId, index);
    if (!newId) return;
    if (isReplace) onBind?.(newId);
    else appendAsset(file.name.replace(/\.[^.]+$/, ''), '', newId);
    onClose();
    await uploadFileIntoNode(newId, file);
  };

  return (
    <div className="script-borders fixed inset-0 z-40 flex items-center justify-center bg-black/60" onMouseDown={(e) => e.stopPropagation()}>
      <div
        data-testid="add-asset-dialog"
        className="flex h-[min(760px,90vh)] w-[min(1000px,94vw)] flex-col overflow-hidden rounded-2xl border border-white/10 bg-[#171717] shadow-2xl"
      >
        <div className="flex items-center justify-between border-b border-white/10 px-5 py-4">
          <h2 className="text-base font-medium text-foreground">{isReplace ? `选择图片（${replaceAsset!.name}）` : `新增${sectionLabel}`}</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="关闭"
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground bg-transparent border-none cursor-pointer"
          >
            <X size={18} />
          </button>
        </div>

        <div className="flex items-center gap-6 px-5 pt-4">
          {([['ai', 'AI生成'], ['canvas', '从当前画布选择'], ['upload', '本地上传']] as [Tab, string][]).map(([t, label]) => (
            <button
              key={t}
              type="button"
              data-testid={`add-asset-tab-${t}`}
              onClick={() => setTab(t)}
              className={
                'bg-transparent border-none cursor-pointer p-0 text-sm transition-colors '
                + (tab === t ? 'font-medium text-foreground' : 'text-muted-foreground hover:text-foreground')
              }
            >
              {label}
            </button>
          ))}
        </div>

        {tab === 'ai' && (
          <div className="flex min-h-0 flex-1 flex-col gap-3 px-5 py-4">
            <textarea
              data-testid="asset-gen-prompt"
              value={prompt}
              placeholder="开始你的设计"
              onChange={(e) => setPrompt(e.target.value)}
              className="min-h-0 w-full flex-1 resize-none rounded-xl border border-white/10 bg-black/40 p-4 text-sm leading-6 text-foreground outline-none focus:border-white/25"
            />
            <div className="flex items-center justify-between gap-3">
              {/* ScriptSelect 而非原生 <select>：系统白色菜单在暗色弹框里主题撕裂
                  （用户点名），且 PopoverShell 自带越底上翻——底栏下拉自动向上弹。
                  与批量生视频弹框的参数下拉同款。 */}
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <ScriptSelect
                  ariaLabel="生图模型"
                  value={effectiveModelId}
                  options={
                    imageModels.length > 0
                      ? imageModels.map((m) => ({ value: m.modelId, label: m.name }))
                      : [{ value: '', label: '暂无可用图片模型' }]
                  }
                  onChange={(v) => { setModelId(v); setDyn({}); }}
                  className="min-w-[140px]"
                />
                {schemaEntries.map(([key, field]) =>
                  (field.options?.length ?? 0) > 0 ? (
                    <ScriptSelect
                      key={key}
                      ariaLabel={field.label || key}
                      value={String(dynamicInputs[key] ?? '')}
                      options={(field.options ?? []).map((o) => ({ value: String(o.value), label: o.label }))}
                      onChange={(v) => setDyn((prev) => ({ ...prev, [key]: v }))}
                      className="min-w-[104px]"
                    />
                  ) : null,
                )}
              </div>
              <div className="flex shrink-0 items-center gap-3">
                <button
                  type="button"
                  data-testid="asset-gen-confirm"
                  disabled={!prompt.trim() || !effectiveModelId || submitting}
                  onClick={handleGenerate}
                  className={`${SAVE_BTN} disabled:cursor-not-allowed disabled:opacity-50`}
                >
                  确认生成
                </button>
              </div>
            </div>
          </div>
        )}

        {tab === 'canvas' && (
          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
            {pickableNodes.length === 0 ? (
              <div className="py-10 text-center text-sm text-muted-foreground">画布上还没有带图的图片节点</div>
            ) : (
              <div className="flex flex-wrap gap-4">
                {pickableNodes.map((n, i) => {
                  const label = nodeLabel(n, i);
                  const isCurrent = isReplace && n.id === replaceAsset?.linkedNodeId;
                  return (
                    <button
                      key={n.id}
                      type="button"
                      data-testid={`pick-node-${n.id}`}
                      disabled={isCurrent}
                      onClick={() => handlePick(n.id, label)}
                      className={
                        'flex w-56 flex-col gap-1.5 bg-transparent border-none p-0 text-left '
                        + (isCurrent ? 'cursor-default' : 'cursor-pointer')
                      }
                    >
                      <span className="relative flex h-40 w-full items-center justify-center overflow-hidden rounded-xl border border-white/10 bg-white/[0.03] hover:border-white/40">
                        <ThumbImg src={n.content} alt={label} className="h-full w-full object-cover" />
                        {isCurrent && (
                          <span className="absolute inset-0 flex items-center justify-center bg-black/60 text-sm text-muted-foreground">当前</span>
                        )}
                      </span>
                      <span className={'truncate text-center text-sm ' + (isCurrent ? 'text-muted-foreground' : 'text-foreground')}>{label}</span>
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        )}

        {tab === 'upload' && (
          <div className="min-h-0 flex-1 px-5 py-4">
            <input
              ref={fileInputRef}
              data-testid="asset-upload-input"
              type="file"
              accept="image/*"
              className="hidden"
              onChange={(e) => { void handleFiles(e.target.files); e.target.value = ''; }}
            />
            <button
              type="button"
              data-testid="asset-upload-zone"
              onClick={() => fileInputRef.current?.click()}
              onDragOver={(e) => e.preventDefault()}
              onDrop={(e) => { e.preventDefault(); void handleFiles(e.dataTransfer.files); }}
              className="flex h-full w-full flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-white/20 bg-transparent text-sm cursor-pointer hover:border-white/40"
            >
              <span>
                <span className="text-blue-400 underline">点击上传</span>
                <span className="text-foreground"> 或 拖拽本地图片至此上传</span>
              </span>
              <span className="text-xs text-muted-foreground">上传后画布将新建一个图片节点并自动连线到脚本节点</span>
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
