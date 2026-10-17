import { useMemo, useState } from 'react';
import { App as AntApp, ConfigProvider, Select, Slider, Switch } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { ArrowUp, RotateCcw, X } from 'lucide-react';
import {
  MULTI_ANGLE_PRESET_KEY,
  MultiAngleZoom,
  fetchMultiAngleConfig,
  listRuntimeModels,
  submitCanvasTask,
  type UISchemaField,
} from '@/shared/api';
import { DEFAULT_SIZES, useCanvasStore } from '../store';
import { persistNodeById } from '../persistence/canvasMutations';
import { buildDynamicInputs, buildImageGenerationInputs } from '../utils/generationInputs';
import { MultiAngleSphere } from './MultiAngleSphere';

// 左=全景、右=特写（往右拉近景别，球体中央图随之变大）。
const ZOOM_ORDER: MultiAngleZoom[] = ['panorama', 'medium', 'closeup'];
const ZOOM_LABELS: Record<MultiAngleZoom, string> = {
  closeup: '特写',
  medium: '中景',
  panorama: '全景',
};

interface Props {
  /** 源图片节点：新的 gen-image 节点铺在它右侧并连线 */
  sourceNodeId: string;
  onClose: () => void;
}

// 多角度编辑器面板（挂在选中图片节点下方的浮动
// 面板，不是居中弹框；定位由 ImageActionsBar 的锚点容器负责）。
// 点 ↑ 直接提交生成：铺 gen-image 节点 + 连线 + 立即
// 走 /canvas-tasks——与 GenImageNode 的生成按钮同一套提交契约，轮询/写回由
// 新节点自己的 GenImageNode 逻辑接管（status=generating + taskId 即自动轮询）。
// 角度 → 中文提示词拼接在服务端执行层（保留 key __multi_angle__），前端只见数值。
export function MultiAnglePanel({ sourceNodeId, onClose }: Props) {
  const { message } = AntApp.useApp();
  const addNode = useCanvasStore((s) => s.addNode);
  const addConnection = useCanvasStore((s) => s.addConnection);
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);

  const configQuery = useQuery({
    queryKey: ['multi-angle-config'],
    queryFn: fetchMultiAngleConfig,
    staleTime: 300_000,
  });
  // 模型显示名/uiSchema 从模型目录反查（与 GenImageNode 同 queryKey 共享缓存）。
  const modelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 60_000,
  });

  const cfg = configQuery.data;
  const [presetKey, setPresetKey] = useState(''); // ''=自定义
  const [rotation, setRotation] = useState(0);
  const [tilt, setTilt] = useState(0);
  const [zoom, setZoom] = useState<MultiAngleZoom>('medium');
  const [promptEnabled, setPromptEnabled] = useState(false);
  const [userPrompt, setUserPrompt] = useState('');
  const [modelId, setModelId] = useState('');
  const [submitting, setSubmitting] = useState(false);

  // 默认选启用列表第一个（用户拍板）。
  const effectiveModelId = modelId || cfg?.models[0] || '';
  const selectedModel = (modelsQuery.data ?? []).find((m) => m.modelId === effectiveModelId);

  const modelOptions = useMemo(() => {
    const nameById = new Map((modelsQuery.data ?? []).map((m) => [m.modelId, m.name]));
    return (cfg?.models ?? []).map((id) => ({ value: id, label: nameById.get(id) ?? id }));
  }, [cfg?.models, modelsQuery.data]);

  const sourceImageUrl = useCanvasStore((s) => {
    const n = s.nodes.find((x) => x.id === sourceNodeId);
    return typeof n?.content === 'string' ? n.content : undefined;
  });

  // ── 动态入参：与 AI 绘图节点同一套默认值（buildDynamicInputs），提交时用。──
  const schemaEntries = useMemo(
    () =>
      Object.entries(selectedModel?.uiSchema ?? {}).filter(([, v]) => v != null) as Array<
        [string, UISchemaField]
      >,
    [selectedModel],
  );
  const dynamicInputs = useMemo(() => buildDynamicInputs(schemaEntries, undefined), [schemaEntries]);

  const applyPreset = (key: string) => {
    setPresetKey(key);
    const p = cfg?.presets.find((x) => x.key === key);
    if (p) {
      setRotation(p.rotation);
      setTilt(p.tilt);
      setZoom(p.zoom);
    }
  };

  const reset = () => {
    setPresetKey('');
    setRotation(0);
    setTilt(0);
    setZoom('medium');
    setPromptEnabled(false);
    setUserPrompt('');
  };

  // ↑ 直接提交生成：铺节点 → 置 generating → /canvas-tasks（与 GenImageNode
  // handleGenerate 同契约），新节点的轮询 effect 按 taskId 自动接管展示与写回。
  const handleSubmit = async () => {
    if (submitting) return;
    if (!effectiveModelId) {
      message.warning('请在设置中配置多角度模型');
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
    const presetParams: Record<string, string> = {
      rotation: String(rotation),
      tilt: String(tilt),
      zoom,
      ...(presetKey ? { preset: presetKey } : {}),
    };
    const submittedPrompt = promptEnabled ? userPrompt.trim() : '';
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
      promptPreset: MULTI_ANGLE_PRESET_KEY,
      presetParams,
      ...(submittedPrompt ? { prompt: submittedPrompt } : {}),
      // pendingGen 快照与 GenImageNode 提交同形态：历史条目回显要用。
      pendingGen: {
        prompt: submittedPrompt,
        params: dynamicInputs,
        presetKey: MULTI_ANGLE_PRESET_KEY,
        presetName: '多角度视角',
        presetParams,
      },
    });
    updateNode(newId, { status: 'generating' });
    addConnection(sourceNodeId, newId);
    persistNodeById(newId);
    try {
      const projectId =
        Number(window.location.pathname.split('/canvas/')[1]?.split('/')[0]) || undefined;
      const canvasTask = await submitCanvasTask({
        projectId: projectId!,
        nodeId: newId,
        modelId: effectiveModelId,
        inputs: buildImageGenerationInputs({
          prompt: submittedPrompt,
          fallbackPrompt: '',
          dynamicInputs,
          connectedImages: [sourceImageUrl],
        }),
        presetKey: MULTI_ANGLE_PRESET_KEY,
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
    <div className="w-[620px] rounded-2xl border border-solid border-border bg-card p-4 shadow-2xl">
      {/* 头部：标题 + 关闭 */}
      <div className="mb-3 flex items-center justify-between">
        <span className="text-base text-foreground">多角度编辑器</span>
        <button
          type="button"
          aria-label="关闭"
          onClick={onClose}
          className="flex h-7 w-7 cursor-pointer items-center justify-center rounded-md border-none bg-transparent text-muted-foreground hover:text-foreground"
        >
          <X size={18} />
        </button>
      </div>

      {/* 预设 tab：自定义 + 后台配置的预设。一行放完：
          字号/内边距压小 + 不换行；条目再多时横向滚动兜底，不折行。 */}
      <div className="mb-4 flex flex-nowrap gap-1.5 overflow-x-auto">
        {[{ key: '', name: '自定义' }, ...(cfg?.presets ?? [])].map((p) => (
          <button
            key={p.key || '__custom__'}
            type="button"
            onClick={() => (p.key ? applyPreset(p.key) : setPresetKey(''))}
            className={`shrink-0 cursor-pointer whitespace-nowrap rounded-lg border border-solid px-2.5 py-1 text-[13px] transition-colors ${
              presetKey === p.key
                ? 'border-white/30 bg-accent text-foreground'
                : 'border-border bg-transparent text-muted-foreground hover:text-foreground'
            }`}
          >
            {p.name}
          </button>
        ))}
      </div>

      <div className="flex gap-5">
        <MultiAngleSphere
          rotation={rotation}
          tilt={tilt}
          zoom={zoom}
          imageUrl={sourceImageUrl}
          onChange={({ rotation: r, tilt: t }) => {
            setRotation(r);
            setTilt(t);
          }}
        />

        <div className="flex flex-1 flex-col gap-3 pt-1">
          {/* 滑杆配色：已滑过轨道青色填充 + 白色手柄（默认暗色轨道
              看不出进度）。角度值显示保证整型：state 只可能被整数写入
              （滑杆步进 1、球体 emit 已取整），Math.round 是防御性兜底。 */}
          <ConfigProvider
            theme={{
              components: {
                Slider: {
                  trackBg: '#22d3ee',
                  trackHoverBg: '#22d3ee',
                  railBg: 'rgba(255,255,255,0.16)',
                  railHoverBg: 'rgba(255,255,255,0.24)',
                  handleColor: '#ffffff',
                  handleActiveColor: '#ffffff',
                },
              },
            }}
          >
            <div className="flex items-center gap-3">
              <span className="w-16 shrink-0 text-sm text-foreground">水平环绕</span>
              <Slider className="flex-1" min={0} max={359} value={rotation} onChange={setRotation} />
              <span className="w-10 text-right text-sm text-muted-foreground">{Math.round(rotation)}°</span>
            </div>
            <div className="flex items-center gap-3">
              <span className="w-16 shrink-0 text-sm text-foreground">垂直俯仰</span>
              <Slider className="flex-1" min={-90} max={90} value={tilt} onChange={setTilt} />
              <span className="w-10 text-right text-sm text-muted-foreground">{Math.round(tilt)}°</span>
            </div>
            <div className="flex items-center gap-3">
              <span className="w-16 shrink-0 text-sm text-foreground">景别缩放</span>
              {/* 景别滑杆是「中点原点」双向填充：中景=中位无填充，
                  往左到全景/往右到特写时青色从中点填向手柄。AntD 不支持中点
                  原点，track 置透明、把填充画在 rail 的渐变上。 */}
              <Slider
                className="flex-1"
                min={0}
                max={2}
                step={1}
                tooltip={{ open: false }}
                value={ZOOM_ORDER.indexOf(zoom)}
                onChange={(v) => setZoom(ZOOM_ORDER[v as number] ?? 'medium')}
                styles={{
                  track: { background: 'transparent' },
                  rail: {
                    background:
                      zoom === 'panorama'
                        ? 'linear-gradient(to right, #22d3ee 0%, #22d3ee 50%, rgba(255,255,255,0.16) 50%)'
                        : zoom === 'closeup'
                          ? 'linear-gradient(to right, rgba(255,255,255,0.16) 50%, #22d3ee 50%, #22d3ee 100%)'
                          : 'rgba(255,255,255,0.16)',
                  },
                }}
              />
              <span className="w-10 text-right text-sm text-muted-foreground">{ZOOM_LABELS[zoom]}</span>
            </div>
          </ConfigProvider>
          <div className="flex items-center gap-3">
            <span className="w-16 shrink-0 text-sm text-foreground">提示词</span>
            <Switch size="small" checked={promptEnabled} onChange={setPromptEnabled} />
          </div>
          {promptEnabled && (
            <textarea
              value={userPrompt}
              onChange={(e) => setUserPrompt(e.target.value)}
              placeholder="输入提示词...（追加在角度描述之后）"
              className="h-16 w-full resize-none rounded-lg border border-solid border-border bg-white/[0.04] p-2 text-sm text-foreground outline-none focus:border-white/30"
            />
          )}
        </div>
      </div>

      {/* 底部：重置 + 模型下拉（左）；圆形↑提交（右） */}
      <div className="mt-4 flex items-center gap-3">
        <button
          type="button"
          onClick={reset}
          className="flex cursor-pointer items-center gap-1.5 border-none bg-transparent p-0 text-sm text-muted-foreground hover:text-foreground"
        >
          <RotateCcw size={14} />
          重置参数
        </button>
        <Select
          size="small"
          style={{ minWidth: 180 }}
          value={effectiveModelId || undefined}
          placeholder="选择绘图模型"
          options={modelOptions}
          onChange={setModelId}
        />
        <div className="flex-1" />
        <button
          type="button"
          aria-label="生成"
          disabled={submitting}
          onClick={handleSubmit}
          className="flex h-11 w-11 cursor-pointer items-center justify-center rounded-2xl border-none bg-white text-black shadow hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <ArrowUp size={20} />
        </button>
      </div>
    </div>
  );
}
