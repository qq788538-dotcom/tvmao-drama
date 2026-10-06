import { useMemo, useRef, useState } from 'react';
import { App as AntApp, ColorPicker, InputNumber, Slider, Switch, Tooltip } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { ArrowUp, Plus, RotateCcw, X, Zap } from 'lucide-react';
import {
  LIGHT_CONTROL_PRESET_KEY,
  LightControlPresetPublic,
  currentProjectId,
  fetchLightControlConfig,
  listRuntimeModels,
  submitCanvasTask,
  uploadAsset,
  type UISchemaField,
} from '@/shared/api';
import { DEFAULT_SIZES, useCanvasStore } from '../store';
import { persistNodeById } from '../persistence/canvasMutations';
import { buildDynamicInputs, buildImageGenerationInputs } from '../utils/generationInputs';
import { loadEstimate } from '../batchQueries';
import { useDebouncedValue } from '../hooks/useDebouncedValue';
import { LightSphere } from './LightSphere';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';
import { buildLightPresetParams, hasLightDirtyState } from './lightParams';

interface Props {
  /** 源图片节点：新的 gen-image 节点铺在它右侧并连线 */
  sourceNodeId: string;
  onClose: () => void;
}

/** 主光源六个快捷按钮（liblib 同款两行三列），值是 26 档枚举里的六个正方向。 */
const KEY_LIGHT_BUTTONS: Array<Array<{ key: string; label: string }>> = [
  [
    { key: 'left', label: '左侧' },
    { key: 'top', label: '顶部' },
    { key: 'right', label: '右侧' },
  ],
  [
    { key: 'front', label: '前方' },
    { key: 'bottom', label: '底部' },
    { key: 'back', label: '后方' },
  ],
];

function HelpDot({ tip }: { tip: string }) {
  return (
    <Tooltip title={tip}>
      <span className="flex h-4 w-4 cursor-help items-center justify-center rounded-full border border-solid border-border text-[10px] text-muted-foreground">
        ?
      </span>
    </Tooltip>
  );
}

// 打光效果面板（对标 liblib light-editor：挂在选中图片节点下方的浮动面板；
// 定位由 ImageActionsBar 的锚点容器负责——与 MultiAnglePanel 同一套宿主）。
// 点 ↑ 直接提交生成（用户拍板，对标 liblib）：铺 gen-image 节点 + 连线 + 立即
// 走 /canvas-tasks——就是替用户按了一次 AI 绘图节点的生成键，不是新生成通道；
// 轮询/写回由新节点自己的 GenImageNode 逻辑接管（status=generating + taskId）。
// 参数 → 中文打光文案的拼接在服务端执行层（保留 key __light_control__），
// 前端只见参数 key；脏标记语义（没碰过的组不进 presetParams）在 lightParams.ts。
export function LightPanel({ sourceNodeId, onClose }: Props) {
  const { message } = AntApp.useApp();
  const addNode = useCanvasStore((s) => s.addNode);
  const addConnection = useCanvasStore((s) => s.addConnection);
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);

  const configQuery = useQuery({
    queryKey: ['light-control-config'],
    queryFn: fetchLightControlConfig,
    staleTime: 300_000,
  });
  // 模型 uiSchema/priceNote 从模型目录反查（与 GenImageNode 同 queryKey 共享缓存）。
  const modelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 60_000,
  });

  const cfg = configQuery.data;
  // 模型取后台配置第一个（对标站面板没有模型选择；要换模型可在生成后的节点上换）。
  const effectiveModelId = cfg?.models[0] || '';
  const selectedModel = (modelsQuery.data ?? []).find((m) => m.modelId === effectiveModelId);

  const [view, setView] = useState<'persp' | 'front'>('persp');
  const [direction, setDirection] = useState('front');
  const [sceneTouched, setSceneTouched] = useState(false);
  const [rimEnabled, setRimEnabled] = useState(false);
  const [brightness, setBrightness] = useState(50);
  const [brightnessTouched, setBrightnessTouched] = useState(false);
  const [lightColor, setLightColor] = useState('#ffffff');
  const [colorTouched, setColorTouched] = useState(false);
  const [smartMode, setSmartMode] = useState(false);
  const [prompt, setPrompt] = useState('');
  const [refImage, setRefImage] = useState<string | null>(null);
  const [refUploading, setRefUploading] = useState(false);
  const [presetKey, setPresetKey] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const sourceImageUrl = useCanvasStore((s) => {
    const n = s.nodes.find((x) => x.id === sourceNodeId);
    return typeof n?.content === 'string' ? n.content : undefined;
  });

  const formState = {
    direction,
    sceneTouched,
    rimEnabled,
    brightness,
    brightnessTouched,
    lightColor,
    colorTouched,
    presetKey,
    // 参考图是智能模式的一部分：开关关掉即不随提交（liblib 同款语义），
    // 但面板状态保留——再打开还在。
    hasReferenceImage: smartMode && Boolean(refImage),
  };
  const dirty = hasLightDirtyState(formState, smartMode ? prompt : '');

  // ── 预扣积分：与 AI 绘图节点同一套预估（buildDynamicInputs 默认值 +
  //   loadEstimate 合批 + priceNote 口径），不另造规则。────────────────────
  const schemaEntries = useMemo(
    () =>
      Object.entries(selectedModel?.uiSchema ?? {}).filter(([, v]) => v != null) as Array<
        [string, UISchemaField]
      >,
    [selectedModel],
  );
  const dynamicInputs = useMemo(() => buildDynamicInputs(schemaEntries, undefined), [schemaEntries]);
  const estimateInputs = useMemo(
    () =>
      buildImageGenerationInputs({
        prompt: '',
        fallbackPrompt: '',
        dynamicInputs,
        connectedImages: sourceImageUrl ? [sourceImageUrl] : [],
      }),
    [dynamicInputs, sourceImageUrl],
  );
  const debouncedEstimateInputs = useDebouncedValue(estimateInputs, 400);
  const estimateQuery = useQuery({
    queryKey: [
      'canvas-cost-estimate',
      'image',
      effectiveModelId,
      JSON.stringify(debouncedEstimateInputs),
    ],
    queryFn: () => loadEstimate({ modelId: effectiveModelId, inputs: debouncedEstimateInputs }),
    enabled: Boolean(selectedModel),
    staleTime: 30_000,
    retry: false,
  });
  const priceNote = selectedModel?.priceNote;
  const creditsLabel = priceNote
    ? priceNote
    : estimateQuery.isFetching
      ? '…'
      : String(estimateQuery.data ?? 0);

  const touchScene = (dir: string) => {
    setDirection(dir);
    setSceneTouched(true);
  };

  // 点预设 = 先清参数再套预设字段（liblib 同款覆盖式：伦勃朗光点下去不残留上一个
  // 预设的亮度/颜色）；保密 prompt 不下发，靠 preset key 在服务端追加。
  const applyPreset = (p: LightControlPresetPublic) => {
    setDirection(p.direction || 'front');
    setSceneTouched(Boolean(p.direction));
    setRimEnabled(false);
    setBrightness(p.brightness ?? 50);
    setBrightnessTouched(p.brightness != null);
    setLightColor(p.lightColor || '#ffffff');
    setColorTouched(Boolean(p.lightColor));
    setRefImage(p.referenceImage || null);
    setPresetKey(p.key);
  };

  const reset = () => {
    setDirection('front');
    setSceneTouched(false);
    setRimEnabled(false);
    setBrightness(50);
    setBrightnessTouched(false);
    setLightColor('#ffffff');
    setColorTouched(false);
    setPrompt('');
    setRefImage(null);
    setPresetKey('');
  };

  const handleRefPick = async (file: File) => {
    setRefUploading(true);
    try {
      const uploaded = await uploadAsset(file, currentProjectId());
      setRefImage(uploaded.url);
    } catch (err: any) {
      message.error(err?.response?.data?.error || '参考图上传失败');
    } finally {
      setRefUploading(false);
    }
  };

  // ↑ 直接提交生成：铺节点 → 置 generating → /canvas-tasks（与 GenImageNode
  // handleGenerate 同契约），新节点的轮询 effect 按 taskId 自动接管展示与写回。
  const handleSubmit = async () => {
    if (submitting || !dirty) return;
    if (!effectiveModelId) {
      message.warning('后台未配置打光模型');
      return;
    }
    const st = useCanvasStore.getState();
    const source = st.nodes.find((n) => n.id === sourceNodeId);
    if (!source) return;
    if (!sourceImageUrl) {
      message.warning('源图片还没有内容，无法生成');
      return;
    }
    setSubmitting(true);

    const size = DEFAULT_SIZES['gen-image'];
    const count = st.connections.filter((c) => {
      if (c.from !== sourceNodeId) return false;
      const t = st.nodes.find((n) => n.id === c.to);
      return t?.type === 'gen-image';
    }).length;
    const presetParams = buildLightPresetParams(formState);
    const submittedPrompt = smartMode ? prompt.trim() : '';
    // 参考图作为第二张图片输入随生成下发；拼接文案里的迁移指令由 hasRef 参数触发。
    const connectedImages = [
      sourceImageUrl,
      ...(formState.hasReferenceImage && refImage ? [refImage] : []),
    ];
    const newId = addNode('gen-image', {
      x: source.x + source.width + 160 + size.width / 2,
      y: source.y + count * (size.height + 60) + size.height / 2,
    });
    if (!newId) {
      setSubmitting(false);
      return;
    }
    updateNodeSettings(newId, {
      modelId: effectiveModelId,
      promptPreset: LIGHT_CONTROL_PRESET_KEY,
      presetParams,
      ...(submittedPrompt ? { prompt: submittedPrompt } : {}),
      // pendingGen 快照与 GenImageNode 提交同形态：历史条目回显要用。
      pendingGen: {
        prompt: submittedPrompt,
        params: dynamicInputs,
        presetKey: LIGHT_CONTROL_PRESET_KEY,
        presetName: '打光效果',
        presetParams,
      },
    });
    updateNode(newId, { status: 'generating' });
    addConnection(sourceNodeId, newId);
    persistNodeById(newId);
    try {
      const canvasTask = await submitCanvasTask({
        projectId: currentProjectId()!,
        nodeId: newId,
        modelId: effectiveModelId,
        inputs: buildImageGenerationInputs({
          prompt: submittedPrompt,
          fallbackPrompt: '',
          dynamicInputs,
          connectedImages,
        }),
        presetKey: LIGHT_CONTROL_PRESET_KEY,
        presetParams,
      });
      updateNode(newId, { taskId: canvasTask.taskId });
      persistNodeById(newId);
      onClose();
    } catch (err: any) {
      const msg = err?.response?.data?.error || err?.message || '生成失败';
      updateNode(newId, { status: 'failed', error: msg, taskId: undefined });
      persistNodeById(newId);
      message.error(msg);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="rounded-2xl border border-solid border-border bg-card p-4 shadow-2xl">
      {/* 头部：标题 + 关闭 */}
      <div className="mb-3 flex items-center justify-between">
        <span className="text-base text-foreground">打光效果</span>
        <button
          type="button"
          aria-label="关闭"
          onClick={onClose}
          className="flex h-7 w-7 cursor-pointer items-center justify-center rounded-md border-none bg-transparent text-muted-foreground hover:text-foreground"
        >
          <X size={18} />
        </button>
      </div>

      <div className="flex gap-5">
        {/* 光球卡片：透视/正面切换 + 球 */}
        <div className="flex w-[240px] shrink-0 flex-col items-center gap-3 rounded-xl bg-white/[0.04] p-3">
          <div className="flex rounded-lg bg-white/[0.06] p-0.5">
            {(
              [
                { key: 'persp', label: '透视' },
                { key: 'front', label: '正面' },
              ] as const
            ).map((t) => (
              <button
                key={t.key}
                type="button"
                onClick={() => setView(t.key)}
                className={`cursor-pointer rounded-md border-none px-4 py-1 text-sm transition-colors ${
                  view === t.key
                    ? 'bg-accent text-foreground'
                    : 'bg-transparent text-muted-foreground hover:text-foreground'
                }`}
              >
                {t.label}
              </button>
            ))}
          </div>
          <LightSphere
            direction={direction}
            imageUrl={sourceImageUrl}
            view={view}
            // 光锥/光晕跟随主光颜色（对标 liblib：选蓝出蓝锥）；未激活颜色时白光。
            lightColor={colorTouched ? lightColor : '#ffffff'}
            // 亮度实时驱动光球观感（滑条不用等提交）；脏标记语义不变——没碰过
            // 滑条时这里恒为 50，也不会进生成参数。
            brightness={brightness}
            onChange={touchScene}
          />
        </div>

        {/* 参数列 */}
        <div className="flex w-[250px] shrink-0 flex-col gap-3 pt-1">
          <div className="flex items-center justify-between">
            <span className="text-base text-foreground">全局</span>
            <span className="flex items-center gap-2 text-sm text-muted-foreground">
              智能模式
              <Switch size="small" checked={smartMode} onChange={setSmartMode} />
            </span>
          </div>
          <div className="flex items-center gap-2">
            <span className="w-8 shrink-0 text-sm text-foreground">亮度</span>
            <HelpDot tip="整体光照亮度；不调整则交给模型自由发挥" />
            <Slider
              className="flex-1"
              min={0}
              max={100}
              value={brightness}
              onChange={(v) => {
                setBrightness(v);
                setBrightnessTouched(true);
              }}
            />
            <InputNumber
              size="small"
              min={0}
              max={100}
              value={brightness}
              style={{ width: 64 }}
              onChange={(v) => {
                if (v == null) return;
                setBrightness(v);
                setBrightnessTouched(true);
              }}
            />
            <span className="text-sm text-muted-foreground">%</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="w-8 shrink-0 text-sm text-foreground">颜色</span>
            <HelpDot tip="主光源颜色；不调整则保持画面原色调" />
            <ColorPicker
              value={lightColor}
              disabledAlpha
              onChange={(c) => {
                setLightColor(c.toHexString());
                setColorTouched(true);
              }}
            >
              {/* 未激活时显示「白底红斜杠」（liblib 同款的「未设置」态） */}
              <button
                type="button"
                aria-label="选择主光颜色"
                className="relative h-7 w-14 cursor-pointer overflow-hidden rounded-md border border-solid border-border"
                style={{ backgroundColor: colorTouched ? lightColor : '#ffffff' }}
              >
                {!colorTouched && (
                  <span
                    className="absolute inset-0"
                    style={{
                      background:
                        'linear-gradient(to top right, transparent 46%, #e33 46%, #e33 54%, transparent 54%)',
                    }}
                  />
                )}
              </button>
            </ColorPicker>
          </div>
          <div className="text-sm text-foreground">主光源</div>
          <div className="flex flex-col gap-2">
            {KEY_LIGHT_BUTTONS.map((row, i) => (
              <div key={i} className="grid grid-cols-3 gap-2">
                {row.map((b) => (
                  <button
                    key={b.key}
                    type="button"
                    onClick={() => touchScene(b.key)}
                    className={`cursor-pointer rounded-lg border border-solid px-2 py-2 text-sm transition-colors ${
                      direction === b.key
                        ? 'border-white/30 bg-accent text-foreground'
                        : 'border-border bg-transparent text-muted-foreground hover:text-foreground'
                    }`}
                  >
                    {b.label}
                  </button>
                ))}
              </div>
            ))}
          </div>
          <div className="flex items-center gap-2">
            <span className="shrink-0 text-sm text-foreground">轮廓光</span>
            <HelpDot tip="从背面为主体勾勒边缘光；主光源在后方时无需开启" />
            <div className="flex-1" />
            <Switch
              size="small"
              checked={rimEnabled}
              onChange={(v) => {
                setRimEnabled(v);
                // 开轮廓光即视为动过光位组（liblib 同款：payload 会同时带主光方向）。
                if (v) setSceneTouched(true);
              }}
            />
          </div>
        </div>

        {/* 智能模式列：prompt + 参考图 + 预设网格（开关打开才出现，liblib 同款） */}
        {smartMode && (
          <div className="flex w-[300px] shrink-0 flex-col gap-3 pt-1">
            <span className="text-base text-foreground">智能模式</span>
            <div className="flex gap-2">
              <textarea
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                placeholder="简单描述你想实现的打光效果、或者情绪风格"
                className="h-24 flex-1 resize-none rounded-lg border border-solid border-border bg-white/[0.04] p-2 text-sm text-foreground outline-none focus:border-white/30"
              />
              <input
                ref={fileInputRef}
                type="file"
                accept="image/*"
                className="hidden"
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  e.target.value = '';
                  if (f) void handleRefPick(f);
                }}
              />
              {refImage ? (
                <div className="relative h-24 w-20 shrink-0 overflow-hidden rounded-lg border border-solid border-border">
                  <img src={refImage} crossOrigin={mediaCrossOrigin(refImage)} alt="" className="h-full w-full object-cover" />
                  <button
                    type="button"
                    aria-label="移除参考图"
                    onClick={() => setRefImage(null)}
                    className="absolute right-1 top-1 flex h-5 w-5 cursor-pointer items-center justify-center rounded-full border-none bg-black/60 text-white"
                  >
                    <X size={12} />
                  </button>
                </div>
              ) : (
                <button
                  type="button"
                  disabled={refUploading}
                  onClick={() => fileInputRef.current?.click()}
                  className="flex h-24 w-20 shrink-0 cursor-pointer flex-col items-center justify-center gap-1 rounded-lg border border-dashed border-border bg-transparent text-xs text-muted-foreground hover:text-foreground disabled:cursor-wait"
                >
                  <Plus size={16} />
                  {refUploading ? '上传中…' : '打光参考图'}
                </button>
              )}
            </div>
            <div className="text-sm text-foreground">预设</div>
            <div className="grid grid-cols-4 gap-2">
              {(cfg?.presets ?? []).map((p) =>
                p.thumbnail ? (
                  <button
                    key={p.key}
                    type="button"
                    title={p.name}
                    onClick={() => applyPreset(p)}
                    className={`relative h-16 cursor-pointer overflow-hidden rounded-lg border border-solid p-0 transition-colors ${
                      presetKey === p.key ? 'border-white/70' : 'border-transparent'
                    }`}
                  >
                    <img src={p.thumbnail} crossOrigin={mediaCrossOrigin(p.thumbnail)} alt="" className="h-full w-full object-cover" />
                    <span className="absolute inset-x-0 bottom-0 bg-black/50 px-1 py-0.5 text-center text-[11px] text-white">
                      {p.name}
                    </span>
                  </button>
                ) : (
                  <button
                    key={p.key}
                    type="button"
                    onClick={() => applyPreset(p)}
                    className={`cursor-pointer rounded-lg border border-solid px-1 py-2 text-xs transition-colors ${
                      presetKey === p.key
                        ? 'border-white/30 bg-accent text-foreground'
                        : 'border-border bg-transparent text-muted-foreground hover:text-foreground'
                    }`}
                  >
                    {p.name}
                  </button>
                ),
              )}
            </div>
          </div>
        )}
      </div>

      {/* 底部：重置（左）；⚡预扣积分 + 圆形↑提交（右，对标 liblib） */}
      <div className="mt-4 flex items-center gap-3">
        <button
          type="button"
          onClick={reset}
          className="flex cursor-pointer items-center gap-1.5 border-none bg-transparent p-0 text-sm text-muted-foreground hover:text-foreground"
        >
          <RotateCcw size={14} />
          重置参数
        </button>
        <div className="flex-1" />
        <span
          className="flex items-center gap-1 text-sm text-muted-foreground"
          title={priceNote ? '按实际结算，显示为计费说明' : '本次生成预扣的积分'}
        >
          <Zap size={14} />
          {creditsLabel}
        </span>
        <button
          type="button"
          aria-label="生成"
          disabled={submitting || !dirty}
          onClick={handleSubmit}
          className="flex h-11 w-11 cursor-pointer items-center justify-center rounded-2xl border-none bg-white text-black shadow hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
        >
          <ArrowUp size={20} />
        </button>
      </div>
    </div>
  );
}
