import { useReconcileCanvasTask } from './useReconcileCanvasTask';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  ArrowUp,
  BarChart3,
  ChevronLeft,
  ChevronRight,
  LayoutGrid,
  Maximize2,
  Mic,
  Minimize2,
  PlayCircle,
  Plus,
  Upload,
  Video,
  X,
} from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { App as AntApp, Tooltip, message } from 'antd';
import { useCanvasStore, useConnectedInputs } from '../store';
import { useStoryboardMode } from '../../storyboard/StoryboardContext';
import { getDragNodeId, hasDragNode, useNodeDragActive } from '../../storyboard/dragConnect';
import {
  flushNodePatch,
  persistNodeById,
  scheduleNodePatch,
} from '../persistence/canvasMutations';
import { uploadFileIntoNode } from '../utils/uploadIntoNode';
import { NodeShell } from '../NodeShell';
import { useMediaNodeMenu } from './useMediaNodeMenu';
import { VideoActionsBar } from './VideoActionsBar';
import { VideoPlayer } from './VideoPlayer';
import { useNodeMediaActive } from './useNodeMediaActive';
import {
  listRuntimeModels,
  pollCanvasTaskUntilDone,
  submitCanvasTask,
  uploadAsset,
  ModelConfig,
  ModelUISchema,
  UISchemaField,
} from '@/shared/api';
import { calculateResolution, nodeBoxFromDims } from '../utils/resolution';
import { inputLimitViolation } from '../utils/inputLimits';
import { pokerStackOffset } from '../utils/pokerStack';
import { useGenerationTimer } from '../hooks/useGenerationTimer';
import { ThumbImg } from '../ThumbImg';
import { pickRefUploadForNode } from '../utils/uploadRefPicker';
import { renderRejected } from '../utils/rejectedFiles';
import {
  buildDynamicInputs,
  buildVideoGenerationInputs,
  composePrompt,
  modelSettingDefaults,
} from '../utils/generationInputs';
import { PromptMentionEditor, linkNamedMentions, serializeMentionPrompt, stripDeadMentions } from '../PromptMentionEditor';
import {
  appendHistoryItem,
  historyPromptAt,
  normalizeHistory,
  readPendingGen,
} from '../utils/genHistory';
import { AdvancedEditDialog } from './advanced-edit/AdvancedEditDialog';
import { saveAnnotations } from './advanced-edit/save';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';

export function GenVideoNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodesById.get(id));
  // 兜底对账:节点卡在 generating 且丢 taskId 时按 nodeId 找回任务号(useReconcileCanvasTask 顶部注释)。
  useReconcileCanvasTask(id);
  const { modal } = AntApp.useApp();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const linked = useConnectedInputs(id);
  const addNode = useCanvasStore((s) => s.addNode);
  // 只读画布（分享查看器等）下 store.addNode 直接 no-op 返回 ''——「高级编辑」
  // 保存链路拿着这个空 id 去 updateNode/addConnection 会产出一堆挂不上任何节点
  // 的脏 token，禁用按钮比让保存链路半途失败更直接。
  const readOnly = useCanvasStore((s) => s.readOnly);
  // 选中状态驱动浮动上传 chip 和 prompt 面板的显示/隐藏动画——用户选中节点
  // 之前一直隐藏（「聚焦时展开」）。
  const isSelected = useCanvasStore((s) => s.selectedIds.has(id));
  // 详情面板里参数面板要跟预览盒左右对齐（见下方定位处的注释）。
  const storyboardMode = useStoryboardMode();
  // 参数面板未选中时只是 opacity:0，参考图/参考视频 chip 照样挂着媒体元素在加载（P16）。
  // 只在选中时渲染媒体；故事板里面板常显，不门控。
  const showRefMedia = isSelected || storyboardMode;
  // 剪辑(trim)/裁剪(crop)/片段重拍/智能续写进行中:交互条占节点正下方,
  // prompt 面板同位让路,退出后恢复。
  const trimming = useCanvasStore((s) => s.trimmingNodeId === id);
  const cropping = useCanvasStore((s) => s.croppingNodeId === id);
  const reshooting = useCanvasStore((s) => s.reshootNodeId === id);
  const continuing = useCanvasStore((s) => s.continuationNodeId === id);
  // 故事板里参考区支持连线编辑（同 GenImageNode，机制/理由见那边注释）。
  const [refDragOver, setRefDragOver] = useState(false);
  // 三态拖拽反馈，同 GenImageNode。
  const nodeDragActive = useNodeDragActive();
  const disconnectRef = (upstreamId: string) => {
    const st = useCanvasStore.getState();
    st.connections
      .filter((c) => c.from === upstreamId && c.to === id)
      .forEach((c) => st.removeConnection(c.id));
  };

  const fileInputRef = useRef<HTMLInputElement>(null);

  const modelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 60_000,
  });

  // 单一 popover 状态。取值为 'model' | 'ratio' | <schema-key> | null。
  // 互斥，打开一个会关掉其它任何一个。
  const [openPopover, setOpenPopover] = useState<string | null>(null);
  const [bigEditOpen, setBigEditOpen] = useState(false);
  const [galleryOpen, setGalleryOpen] = useState(false);
  // 高级编辑弹框开关。isVideoEditModel 本身要等 selectedModelId 算出来才能判断
  // （在下面 `if (!node) return null` 之后），但这个 state hook 必须留在任何
  // 提前 return 之前——hooks 规则不允许条件调用。
  const [advEditOpen, setAdvEditOpen] = useState(false);
  // 「发送到画布」定义在下面（依赖一堆本地状态），而菜单 hook 要在组件顶层调用，
  // 所以用 ref 转一手：菜单项被点到时才读它，避开 const 的 TDZ。
  const sendToCanvasRef = useRef<() => void>(() => {});
  const mediaMenu = useMediaNodeMenu({
    url: node?.content,
    kind: 'video',
    nodeId: id,
    extraItems: [
      { key: 'send', label: '发送到画布', onPick: () => sendToCanvasRef.current() },
    ],
  });
  const mediaActive = useNodeMediaActive(id);

  if (!node) return null;

  const videoModels = (modelsQuery.data ?? []).filter((m) => m.type === 'video');
  const selectedModelId =
    (node.settings?.modelId as string | undefined) ?? videoModels[0]?.modelId ?? '';
  const selectedModel = videoModels.find((m) => m.modelId === selectedModelId);
  const uiSchema: ModelUISchema = selectedModel?.uiSchema ?? {};
  // 高级编辑：视频编辑类模型专属（条件用户指定）。将来模型多了再收进 gen_control 配置。
  const isVideoEditModel = selectedModelId.startsWith('doubao-seedance') && selectedModelId.includes('edit');

  const prompt = (node.settings?.videoPrompt as string | undefined) ?? '';
  const isGenerating = node.status === 'generating';
  // 起点 = 提交时刻（持久化在 pendingGen 里），刷新页面后计时条不归零。
  const submittedAt = readPendingGen(node.settings?.pendingGen)?.submittedAt;
  const { elapsed, finalDuration } = useGenerationTimer(
    isGenerating,
    submittedAt ? Date.parse(submittedAt) || undefined : undefined,
  );

  // 多版本 history。每次生成成功都往 `settings.history` 追加一条
  // 记录；`settings.historyIndex` 决定当前显示哪一个。`node.content` 镜像那条
  // 记录的 URL，这样读 `content` 的下游节点会跟随用户当前显示的选择。
  //
  // 条目现在是 `{url, prompt, params, at}`——每一版都自带生成它的提示词，用户
  // 改词重生成后翻回旧片，还能看到旧片当时用的那句话。
  // 向后兼容（见 utils/genHistory.ts）：老快照里的 `string[]` 会被归一化成
  // `{url}`，缺失的 prompt 一律回退到节点当前 `settings.videoPrompt`，即存量画布
  // 的表现与改造前逐字一致。有 `node.content` 但没有 `history` 的节点，仍被当作
  // 只有一条、用该 URL 初始化的 history。
  const history = normalizeHistory(node.settings?.history, node.content);
  const settingsIdx = node.settings?.historyIndex as number | undefined;
  const historyIndex =
    typeof settingsIdx === 'number' && settingsIdx >= 0 && settingsIdx < history.length
      ? settingsIdx
      : Math.max(0, history.length - 1);
  const currentUrl = history[historyIndex]?.url ?? '';

  // 翻到历史版本时，输入框直接显示那一版当时用的提示词。
  //
  // 只对「非最新版」这么做。停在最新版时必须显示 `settings.videoPrompt`——那是
  // 用户正在写、还没生成的草稿；用最新版已生成的词去盖它，用户改到一半的内容
  // 就没了。老数据里条目不带 prompt，historyPromptAt 回退到 `prompt` 本身，
  // 所以两条分支取值相同，存量节点看不出任何变化。
  const isLatestVersion = historyIndex === history.length - 1;
  const viewingPrompt = historyPromptAt(history, historyIndex, prompt);
  // 在旧版上直接改输入框时，改的内容要立刻显示出来（它同时写进 settings.videoPrompt
  // 作为下次生成的词）。没有这个本地 override，value 会被钉在旧版的 prompt 上，
  // 用户打字看不见反应。翻页时清空，回到「跟随所看版本」。
  const [promptDraft, setPromptDraft] = useState<string | null>(null);
  const editorPrompt = promptDraft ?? (isLatestVersion ? prompt : viewingPrompt);

  const showHistoryAt = (idx: number) => {
    const safe = ((idx % history.length) + history.length) % history.length;
    setPromptDraft(null);
    updateNodeSettings(id, { historyIndex: safe });
    // `?.` 而不是 `.`：history 为空时 safe 会是 NaN，老代码在这里取到 undefined
    // 就算了，不能因为改了条目类型就把这条路径变成崩溃。
    updateNode(id, { content: history[safe]?.url });
    persistNodeById(id);
  };

  const deleteHistoryAt = (idx: number) => {
    const next = history.filter((_, i) => i !== idx);
    if (next.length === 0) {
      updateNodeSettings(id, { history: [], historyIndex: undefined });
      updateNode(id, { content: undefined });
      persistNodeById(id);
      setGalleryOpen(false);
      return;
    }
    const newIdx = Math.max(0, Math.min(historyIndex, next.length - 1));
    updateNodeSettings(id, { history: next, historyIndex: newIdx });
    updateNode(id, { content: next[newIdx].url });
    persistNodeById(id);
  };

  const schemaEntries = Object.entries(uiSchema).filter(([, v]) => v != null) as Array<[string, UISchemaField]>;
  const ratioField = uiSchema.ratio;
  const resolutionField = uiSchema.resolution;
  const nField = uiSchema.n;
  // 除 ratio/resolution/n 之外的任何字段都在工具栏右侧渲染成一个普通的 pill
  // （比如视频模型上的 duration）。
  const otherSchemaEntries = schemaEntries.filter(([k]) => !['ratio', 'resolution', 'n'].includes(k));

  const settingValue = (key: string, defaultValue?: string | number) => {
    const v = node.settings?.[key];
    if (v !== undefined && v !== null) return v as string | number;
    return defaultValue ?? '';
  };

  const ratioValue = String(settingValue('ratio', ratioField?.default ?? '16:9'));
  const resolutionValue = String(settingValue('resolution', resolutionField?.default ?? ''));
  const nValue = settingValue('n', nField?.default ?? 1);

  // 用*真实输出尺寸*（而不只是 ratio 字符串）来驱动节点宽高比——无论
  // `resolution` 是抽象档位（720P/1080P/…）还是显式的 "WxH"，也无论模型是否
  // 暴露 `ratio` 字段，都能工作。手动 resize 被禁用（NodeShell `resizable={false}`）。
  //
  // 宽度变化时，我们还会把 `x` 平移半个 delta，让节点的水平中心在画布上保持
  // 不动。下面的 prompt 面板锚在节点的 `left: 50%`，所以这样能让 prompt 面板
  // 视觉上保持静止，只有预览框围着它缩放。
  useEffect(() => {
    const { w, h } = calculateResolution(ratioValue, resolutionValue);
    if (!w || !h) return;
    const { width: targetW, height: targetH } = nodeBoxFromDims(w, h);
    if (node.width !== targetW || node.height !== targetH) {
      const newX = node.x + (node.width - targetW) / 2;
      updateNode(id, { x: newX, width: targetW, height: targetH });
    }
  }, [ratioValue, resolutionValue, id, updateNode, node.x, node.width, node.height]);

  // 只要这个 gen-video 节点持有一个进行中的异步任务，就恢复/驱动 polling。
  // 锚在 gen-video 节点自身上，这样结果落到 `node.content` 和 history，而不是
  // 下游预览。刷新页面会恢复进行中的 poll。
  useEffect(() => {
    if (node.status === 'generating' && node.taskId) {
      const ctrl = new AbortController();
      let cancelled = false;
      // 方案 B:轮询画布任务状态仅为即时展示;结果已由后端权威写回快照节点。
      pollCanvasTaskUntilDone(node.taskId, { signal: ctrl.signal })
        .then((finalTask) => {
          if (cancelled) return;
          if (finalTask.status === 'succeeded') {
            const url = finalTask.outputUrls?.[0];
            if (url) {
              // 从 store 读**最新**的 settings，而不是这个 effect 闭包里的旧值：
              // 生成期间用户可能删过版本，刷新页面恢复轮询时闭包更是从头建的。
              // pendingGen 是点「生成」那一刻落库的提示词/入参快照——用它而不是
              // 当前输入框，否则用户边等边改词，记下来的就是错的那句。
              const latest = useCanvasStore.getState().nodes.find((n) => n.id === id);
              const next = appendHistoryItem(
                normalizeHistory(latest?.settings?.history, latest?.content),
                url,
                readPendingGen(latest?.settings?.pendingGen),
                new Date().toISOString(),
              );
              // 按 URL 定位而不是取末尾：后端 sink 可能已经写过这一条（它才是
              // 用户关页面时的唯一写入方），那时 append 会去重，末尾不是它。
              const nextIdx = Math.max(0, next.findIndex((h) => h.url === url));
              // 存下 assetId：节点靠它做「保存到素材库」，需要 asset 身份。
              updateNodeSettings(id, {
                history: next,
                historyIndex: nextIdx,
                pendingGen: undefined,
                assetId: finalTask.assetIds?.[0] || undefined,
                kind: 'video',
                // 新结果属于这次任务：清掉上次下载失败留下的标记，否则重试会拿旧任务覆盖新结果。
                unsaved: undefined,
                genTaskId: undefined,
              });
              updateNode(id, { content: url, status: 'succeeded', error: undefined, taskId: undefined });
            } else {
              updateNode(id, { status: 'failed', error: '上游未返回视频', taskId: undefined });
            }
          } else {
            updateNode(id, {
              status: 'failed',
              error: finalTask.error || '生成失败',
              taskId: undefined,
            });
          }
          // 结果由后端权威写回；前端这次持久化补 history/assetId/kind 等前端字段。
          persistNodeById(id);
        })
        .catch((err: any) => {
          if (cancelled || err?.message === 'aborted') return;
          updateNode(id, {
            status: 'failed',
            error: err?.message || '轮询失败',
            taskId: undefined,
          });
          persistNodeById(id);
        });
      return () => {
        cancelled = true;
        ctrl.abort();
      };
    }
    return undefined;
    // 闭包里读了 history，但我们不希望用户只是翻 historyIndex 时这个 effect
    // 就重新触发；这个循环只以 status+taskId 为 key。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [node.status, node.taskId, id, updateNode, updateNodeSettings]);

  // 为新建节点在首次挂载时初始化 `settings.ratio`。从 localStorage 读
  // `tvmao:lastVideoRatio`；如果该值是当前模型 ratio 选项之一就复用它，否则
  // 回退到第一个选项。和 image 用各自独立的 key，这样 video / image 不会互相
  // 覆盖。
  useEffect(() => {
    if (!ratioField) return;
    const current = node.settings?.ratio;
    if (current !== undefined && current !== null && current !== '') return;
    const validValues = ratioField.options.map((o) => String(o.value));
    if (validValues.length === 0) return;
    let pick = validValues[0];
    try {
      const saved = localStorage.getItem('tvmao:lastVideoRatio');
      if (saved && validValues.includes(saved)) pick = saved;
    } catch { /* 忽略 */ }
    updateNodeSettings(id, { ratio: pick });
    persistNodeById(id);
    // 只在 ratio 选项变得可用时运行；我们不想在这里对每次 settings 变化都
    // 做出反应。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ratioField]);

  const connectedImages = linked.images;
  const connectedVideos = linked.videos;
  const connectedAudios = linked.audios;
  const mediaRefs = linked.refs;
  // 三排缩略图按 kind 各取各的 refs——refs 与 by-kind 数组在 store 里同一
  // 循环构造，顺序天然一致；带 nodeId 才能定位到具体连线（URL 不行，两个
  // 节点可能持有同一份媒体）。
  const imageRefs = mediaRefs.filter((r) => r.kind === 'image');
  const videoRefs = mediaRefs.filter((r) => r.kind === 'video');
  const audioRefs = mediaRefs.filter((r) => r.kind === 'audio');
  // hover × 的公共渲染，三排共用。
  const refCloseBtn = (upstreamId: string, label: string) =>
    storyboardMode ? (
      <button
        type="button"
        aria-label={label}
        onMouseDown={(e) => e.stopPropagation()}
        onClick={(e) => {
          e.stopPropagation();
          disconnectRef(upstreamId);
        }}
        className="absolute -top-1.5 -right-1.5 w-[18px] h-[18px] rounded-full bg-black/85 hover:bg-black text-white border border-white/60 cursor-pointer items-center justify-center hidden group-hover/ref:flex z-10"
      >
        {/* 自绘叉，理由见 GenImageNode 同位置注释（lucide X 笔画只占名义一半）。 */}
        <svg width={12} height={12} viewBox="0 0 12 12" aria-hidden>
          <path d="M1.5 1.5 L10.5 10.5 M10.5 1.5 L1.5 10.5" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" />
        </svg>
      </button>
    ) : null;
  // 提示词与连线的对账（两步，同一 effect）：
  //  1. 断线清理：@ 引用的上游节点已不在连线里 → 文字引用转来的还原原文，手选的剔除。
  //  2. 命名引用：`@张三` / `@[角色-张三]` 与连线里同名图片节点对上 → 换成 chip。
  // 对派生状态(当前连线集合 + 提示词)反应，编辑 / 连线 / 断线 / 改名 / undo 全路径
  // 统一覆盖。指纹带上节点名：上游节点改名后要重新对账。
  const refsFingerprint = mediaRefs.map((r) => `${r.kind}:${r.nodeId}:${r.name ?? ''}`).join('|');
  useEffect(() => {
    const next = linkNamedMentions(stripDeadMentions(prompt, mediaRefs), mediaRefs);
    if (next !== prompt) {
      updateNodeSettings(id, { videoPrompt: next });
      // 必须同步 promptDraft（同下方「追加」路径的教训）：用户在框里打过字后
      // promptDraft 接管显示，只写 settings 的话界面停在原文、store 已是 chip，
      // 再敲一个字又把原文写回 store——对账永远在 store 里空转，用户看不到 chip。
      // 为 null（没打过字 / 正在翻看旧版本）时不动，显示继续跟随 store。
      setPromptDraft((d) => (d === null ? null : next));
      persistNodeById(id);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refsFingerprint, prompt]);
  // 弹框工具栏里三类参考各自独立编号、各有显示上限（图 4 / 视频 2 / 音频 2）。
  // shown 是实际画出的缩略图数，total 是连线总数——两者之差就是 "+N" 溢出提示，
  // 这样音频连线也会被计入溢出，不会像旧版那样只统计图+视频。
  const shownRefCount =
    Math.min(connectedImages.length, 4) +
    Math.min(connectedVideos.length, 2) +
    Math.min(connectedAudios.length, 2);
  const totalRefCount = connectedImages.length + connectedVideos.length + connectedAudios.length;
  // 违约时禁用生成并把原因放 tooltip。这里只是提前反馈——服务端 generation 层
  // 有权威同款校验，绕过前端提交会收到同一句 400。
  const inputLimitError = inputLimitViolation(selectedModel?.genControl?.inputLimits, {
    video: connectedVideos.length,
    image: connectedImages.length,
    audio: connectedAudios.length,
  });
  const schemaFingerprint = JSON.stringify(
    schemaEntries.map(([key, field]) => [key, field.default]),
  );
  const settingsFingerprint = JSON.stringify(node.settings ?? {});
  const dynamicInputs = useMemo(
    () => buildDynamicInputs(schemaEntries, node.settings),
    // schemaEntries 每次 render 都会重建；这里用 fingerprint 绑定真正影响
    // 计费的默认值，而不是数组对象身份。
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [schemaFingerprint, settingsFingerprint],
  );
  const fallbackPrompt = linked.texts.filter(Boolean).join('\n');
  // 「有词可生成」的口径必须和 handleGenerate 取词的口径一致：真正提交的是
  // 「输入框的词 || 上游文本节点的词」，两个都空才是真的没东西可生成。只看
  // 输入框会把「连一个文本节点、节点内不写词、直接点生成」这条既有路径废掉。
  const hasPrompt = Boolean(prompt.trim() || fallbackPrompt.trim());
  // 禁用原因走 AntD Tooltip 即显（mouseEnterDelay=0）而不是原生 title——原生 tooltip
  // 固定悬停 ~1s 才出现且在 disabled 按钮上各浏览器行为不一，用户根本发现不了原因。
  // 就绪态为 null 不弹气泡；按钮的无障碍名固定用 aria-label="生成"（测试也靠它定位）。
  const submitTip = isGenerating
    ? '生成中…'
    : inputLimitError ?? (!selectedModelId ? '请先选择模型' : !hasPrompt ? '请先输入提示词' : null);

  // 用户是否已经在上游连了媒体（图片 / 音频 / 视频，包括能解析成媒体 URL 的
  // PreviewNode 输出）。为 true 时隐藏上传 chip——用户已经在喂参考图了。
  const hasUpstreamMedia =
    connectedImages.length + connectedAudios.length + connectedVideos.length > 0;

  // 点「上传」——把这个节点从 gen-video 变形成一个持有上传视频的通用 preview
  // 节点。preview 是通用的「媒体容器」类型（kind: 'image' | 'video'），AI 生成
  // 结果和用户上传的素材都用它，所以我们不再分叉出一个专门的 video-input 类型。
  // 下游连线保持不变；变形后节点的 content 仍然能被消费视频的 gen 节点通过标准
  // 的 `getConnectedInputs` 流程读到。
  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    const projectId =
      Number(window.location.pathname.split('/canvas/')[1]?.split('/')[0]) || undefined;
    try {
      const asset = await uploadAsset(file, projectId);
      updateNode(id, {
        type: 'preview',
        content: asset.url,
        status: 'succeeded',
        error: undefined,
      });
      updateNodeSettings(id, { assetId: asset.id, kind: 'video' });
      persistNodeById(id);
    } catch (err) {
      updateNode(id, { error: '上传失败' });
      persistNodeById(id);
    }
  };

  // preview 默认尺寸来自 store.ts:32——addNode 以给定点为中心，所以加上半个
  // 尺寸，把「左上角」的意图转换成「中心点」的输入。
  const PREVIEW_HALF = { x: 160, y: 130 };

  const handleSendToCanvas = () => {
    if (!currentUrl) return;
    // 点击时现取：订阅整个 nodes 数组会让本节点随画布上任意节点的每帧拖动重渲染。
    const existingCount = useCanvasStore.getState().nodes.filter(
      (n) =>
        n.type === 'preview' &&
        (n.settings?.sourceGenVideoId as string | undefined) === id,
    ).length;
    const { dx, dy } = pokerStackOffset(existingCount);
    const pos = {
      x: node.x + node.width + dx + PREVIEW_HALF.x,
      y: node.y + dy + PREVIEW_HALF.y,
    };
    const newId = addNode('preview', pos);
    if (!newId) return;
    updateNode(newId, { content: currentUrl, status: 'succeeded' });
    updateNodeSettings(newId, { sourceGenVideoId: id, kind: 'video' });
    persistNodeById(newId);
  };
  sendToCanvasRef.current = handleSendToCanvas;

  const handleGenerate = async () => {
    if (!selectedModelId) return;
    if (inputLimitError) return;
    // 上游文本节点的词与本地 videoPrompt 合并，上游在前（composePrompt）。这里
    // 曾用 "videoPrompt || description.prompt" 二选一，
    // 但那样输入框一有内容就把上游那句整个丢掉，两者其实是基底 + 修正的互补关系。
    //
    // 提交给上游的最终提示词：@ 提及已展开成「图片2」这类纯文本。这也是要记进
    // history 的那一句——记「实际生成用的」而不是编辑器里的原始标记，这样以后
    // 连线变了、上游节点删了，回看依然读得懂。必须和下面 buildVideoGenerationInputs
    // 里那句逐字相同，所以两边共用 composePrompt，不各写一份。
    const submittedPrompt = composePrompt(serializeMentionPrompt(prompt, mediaRefs), fallbackPrompt);
    // pendingGen：把这次提交的提示词/参数当场落库，等结果回来时再写进 history
    // 条目。不能等到那时候再读输入框——用户边等边改词是常态，那样记下来的就是
    // 下一版的词。每次生成都整个覆盖，所以失败遗留的残值不会串到下一次。
    updateNodeSettings(id, {
      pendingGen: { prompt: submittedPrompt, params: dynamicInputs, submittedAt: new Date().toISOString() },
    });
    updateNode(id, {
      status: 'generating',
      error: undefined,
      taskId: undefined,
      // 故意不清空 content——旧视频保持可见（带一层「生成中」浮层），直到
      // 新视频 resolve。
    });
    // 先把「生成中」状态落库，再发起任务。
    persistNodeById(id);
    try {
      const projectId =
        Number(window.location.pathname.split('/canvas/')[1]?.split('/')[0]) || undefined;

      // 方案 B:恒异步,提交画布任务后保持 generating + 持久化画布任务 id;
      // 上面的 polling effect 拿状态展示,结果由后端权威写回快照。
      const result = await submitCanvasTask({
        projectId: projectId!,
        nodeId: id,
        modelId: selectedModelId,
        inputs: buildVideoGenerationInputs({
          // @[图片:<id>] → 「图片2」纯文本,下标与 images/videos/audios 数组顺序一致。
          prompt: serializeMentionPrompt(prompt, mediaRefs),
          fallbackPrompt,
          dynamicInputs,
          connectedImages,
          connectedVideos,
          connectedAudios,
        }),
      });
      updateNode(id, { taskId: result.taskId });
      persistNodeById(id);
    } catch (err: any) {
      const msg = err?.response?.data?.error || err?.message || '生成失败';
      // 保持当前 content 可见；只把 error 暴露出来。
      updateNode(id, { status: 'failed', error: msg, taskId: undefined });
      persistNodeById(id);
    }
  };

  // pill 上展示选项 label 而不是原始 value——分辨率档位的 value 可能是内部
  // 子模型 ID（如 cm-seedance-2.0-mini-480-mg），只有 label 才是给用户看的。
  const optionLabel = (field: UISchemaField | undefined, value: string) =>
    field?.options.find((o) => String(o.value) === value)?.label ?? value;
  const ratioPillLabel = [
    ratioField ? optionLabel(ratioField, ratioValue) : '',
    optionLabel(resolutionField, resolutionValue),
  ]
    .filter((v) => v !== '' && v != null)
    .join(' · ');
  const variationPillLabel = `${nValue}×`;

  return (
    <>
      <NodeShell
        node={node}
        inputs={1}
        bodyOverflow="visible"
        radiusPx={16}
        resizable={false}
        minWidth={240}
        minHeight={200}
        titleExtra={mediaMenu.badge}
      >
        {/* 有视频可操作时才显示（生成中/空节点没有可操作对象，也避免与下面的
            「上传」chip 抢同一片上方空间）。 */}
        <VideoActionsBar nodeId={id} visible={!!currentUrl && !storyboardMode} url={currentUrl} />
        {/* 预览上方的浮动「上传」chip。只在该节点还没有上游媒体时显示——一旦
            连了任何东西，上传只会盖住输入，所以我们干脆把这个交互完全隐藏。
            点击 → 把节点变形成 video-input（见 handleUpload）。 */}
        {!hasUpstreamMedia && !currentUrl && (
          <button
            type="button"
            className="absolute z-30 flex items-center gap-1.5 px-3.5 py-1.5 rounded-full bg-popover/90 hover:bg-accent text-sidebar-foreground text-[12px] cursor-pointer border border-border/40 shadow-md backdrop-blur-sm"
            style={{
              top: -42,
              left: '50%',
              transform: `translateX(-50%) translateY(${isSelected ? '0' : '6px'})`,
              opacity: isSelected ? 1 : 0,
              transition: 'opacity 180ms ease-out, transform 180ms ease-out',
              pointerEvents: isSelected ? 'auto' : 'none',
            }}
            onMouseDown={(e) => e.stopPropagation()}
            onClick={(e) => { e.stopPropagation(); fileInputRef.current?.click(); }}
            title="上传视频（节点将转为视频节点）"
          >
            <Upload size={12} />
            上传
          </button>
        )}
        <input
          ref={fileInputRef}
          type="file"
          accept="video/*"
          className="hidden"
          onChange={handleUpload}
        />

        {/* 「Video」标题 chip 现在统一由 NodeShell 渲染在卡片上方，并支持
            重命名——不再每个节点各放一份。 */}

        {/* ===== 节点 body —— 预览矩形 ===================================== */}
        <div className="w-full h-full p-2 flex flex-col gap-2">
          {(isGenerating || finalDuration !== null) && (
            <div className="px-2 py-0.5 rounded text-[10px] font-mono text-center bg-blue-500/20 text-blue-400 border border-blue-500/30 shrink-0">
              {isGenerating ? `⏱ ${elapsed.toFixed(1)}s` : `✓ ${finalDuration!.toFixed(1)}s`}
            </div>
          )}

          <div className="relative flex-1 rounded-2xl bg-popover/30 border border-border/60 flex items-center justify-center overflow-hidden min-h-0 group">
            {/* 预览 = `history[historyIndex]`。重新生成期间，旧视频原地保留
                并加一层变暗浮层，直到新视频 resolve，这样用户不会丢失上下文。 */}
            {currentUrl ? (
              <VideoPlayer
                active={mediaActive}
                {...mediaMenu.mediaProps}
                src={currentUrl} crossOrigin={mediaCrossOrigin(currentUrl)}
                className="w-full h-full object-contain bg-black"
                playsInline
                preload="metadata"
                onContextMenu={(e) => {
                  e.preventDefault();
                  mediaMenu.onContextMenu(e);
                }}
              />
            ) : (
              <PlayCircle size={48} className="text-muted-foreground/60" />
            )}

            {/* 新生成进行中时的加载浮层（只在已有视频显示时可见——空状态时由
                上面的计时器 pill 来承担提示）。 */}
            {isGenerating && currentUrl && (
              <div
                className="absolute inset-0 bg-black/35 flex items-center justify-center pointer-events-none"
                style={{ backdropFilter: 'blur(1px)' }}
              >
                <div className="text-white/85 text-[11px] font-mono px-2 py-1 rounded bg-black/40">
                  生成中…
                </div>
              </div>
            )}

            {/* 轮播控件——只在 history 超过一条时显示。 */}
            {history.length > 1 && (
              <>
                <button
                  type="button"
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => { e.stopPropagation(); showHistoryAt(historyIndex - 1); }}
                  className="absolute left-1.5 w-7 h-7 rounded-full bg-black/55 hover:bg-black/75 text-white border-none cursor-pointer flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity"
                  style={{ top: '50%', transform: 'translateY(-50%)' }}
                  title="上一个"
                >
                  <ChevronLeft size={14} />
                </button>
                <button
                  type="button"
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => { e.stopPropagation(); showHistoryAt(historyIndex + 1); }}
                  className="absolute right-1.5 w-7 h-7 rounded-full bg-black/55 hover:bg-black/75 text-white border-none cursor-pointer flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity"
                  style={{ top: '50%', transform: 'translateY(-50%)' }}
                  title="下一个"
                >
                  <ChevronRight size={14} />
                </button>
                <div className="absolute bottom-1.5 right-1.5 px-1.5 py-0.5 rounded bg-black/60 text-white text-[10px] font-mono pointer-events-none select-none">
                  {historyIndex + 1}/{history.length}
                </div>
                <button
                  type="button"
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => { e.stopPropagation(); setGalleryOpen((v) => !v); }}
                  className="absolute top-1.5 right-1.5 w-7 h-7 rounded-md bg-black/55 hover:bg-black/75 text-white border-none cursor-pointer flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity"
                  title="所有版本"
                >
                  <LayoutGrid size={12} />
                </button>
                {galleryOpen && (
                  <>
                    <div
                      className="fixed inset-0 z-[200]"
                      onClick={() => setGalleryOpen(false)}
                      onMouseDown={(e) => e.stopPropagation()}
                    />
                    <div
                      className="absolute top-9 right-1.5 z-[201] w-[260px] max-h-60 overflow-auto custom-scrollbar p-1.5 rounded-xl"
                      style={{ backgroundColor: '#2a2a2a', boxShadow: '0px 4px 16px rgba(0,0,0,0.45), inset 0px 0.5px 0px rgba(255,255,255,0.08)' }}
                      onMouseDown={(e) => e.stopPropagation()}
                      onClick={(e) => e.stopPropagation()}
                    >
                      <div className="grid grid-cols-3 gap-1">
                        {history.map((item, i) => (
                          <div
                            key={`${item.url}-${i}`}
                            className="relative aspect-video rounded-md overflow-hidden cursor-pointer group/thumb bg-black"
                            onClick={() => { showHistoryAt(i); setGalleryOpen(false); }}
                            style={{ outline: i === historyIndex ? '2px solid #1fa2dc' : 'none', outlineOffset: '-2px' }}
                            title={`版本 ${i + 1}\n${historyPromptAt(history, i, prompt)}`}
                          >
                            {/* 用 <video preload="metadata">，这样首帧会作为
                                缩略图显示，而不会强制完整下载。 */}
                            <video src={item.url} crossOrigin={mediaCrossOrigin(item.url)} className="w-full h-full object-cover" muted preload="metadata" />
                            <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
                              <PlayCircle size={20} className="text-white/80" />
                            </div>
                            <span
                              role="button"
                              className="absolute top-0.5 right-0.5 w-4 h-4 rounded-full bg-black/70 text-white opacity-0 group-hover/thumb:opacity-100 flex items-center justify-center cursor-pointer transition-opacity"
                              onClick={(e) => { e.stopPropagation(); deleteHistoryAt(i); }}
                              title="删除该版本"
                            >
                              <X size={9} />
                            </span>
                          </div>
                        ))}
                      </div>
                    </div>
                  </>
                )}
              </>
            )}
          </div>
        </div>

        {/* ===== 浮动 Prompt 面板——借助 NodeShell body 上的 overflow:visible
                  溢出节点边界。水平居中在节点上；scale(1/zoom) 抵消画布缩放，
                  屏幕上恒定 680px 宽、字号恒定。这层 transform 随 zoom 每帧
                  变化，不能挂 transition，选中滑入动画下沉到内层 bg-card。==== */}
        <div
          className="absolute z-20"
          /* 故事板详情面板里参数面板要跟上方的预览盒**左右对齐**：那里宽度由容器
             决定，固定 680 会明显比图窄一截、居中飘着。画布上相反——节点自身可能
             只有两三百宽，参数面板必须挣脱节点宽度、按固定 680 展开并居中，
             而且要用 --inv-zoom 抵消画布缩放。 */
          style={{
            top: '100%',
            ...(storyboardMode
              ? { left: 0, right: 0, transform: 'translateY(12px)', width: 'auto' }
              : {
                  left: '50%',
                  transform: 'scale(var(--inv-zoom, 1)) translate(-50%, 12px)',
                  transformOrigin: '0 0',
                  width: 680,
                }),
            pointerEvents: isSelected ? 'auto' : 'none',
            // trim/crop 期间裁剪条/确认条占同一块空间,display:none 而不是
            // 不渲染——面板内部有输入草稿等本地 state,卸载重挂会丢。
            ...(trimming || cropping || reshooting || continuing ? { display: 'none' } : null),
          }}
          onMouseDown={(e) => e.stopPropagation()}
        >
          <div
            className={`bg-card rounded-[20px] shadow-xl flex flex-col relative ${
              storyboardMode && refDragOver
                ? 'border border-dashed border-violet-500'
                : storyboardMode && nodeDragActive
                  ? 'border border-dashed border-white/30'
                  : 'border border-border'
            }`}
            style={{
              transform: `translateY(${isSelected ? '0' : '-6px'})`,
              opacity: isSelected ? 1 : 0,
              transition: 'opacity 180ms ease-out, transform 180ms ease-out',
            }}
            // 故事板里整块参数面板都是连线的 drop 目标（同 GenImageNode）。
            onDragOver={storyboardMode ? (e) => {
              if (!hasDragNode(e.dataTransfer)) return;
              e.preventDefault();
              setRefDragOver(true);
            } : undefined}
            onDragLeave={storyboardMode ? () => setRefDragOver(false) : undefined}
            onDrop={storyboardMode ? (e) => {
              setRefDragOver(false);
              const draggedId = getDragNodeId(e.dataTransfer);
              if (!draggedId || draggedId === id) return;
              e.preventDefault();
              useCanvasStore.getState().addConnection(draggedId, id);
            } : undefined}
          >
            {/* 拖拽期间的居中提示（pointer-events-none，理由同 GenImageNode）。 */}
            {storyboardMode && (nodeDragActive || refDragOver) && (
              <div className="absolute inset-0 z-20 flex items-center justify-center pointer-events-none rounded-[20px]">
                <span className="px-3 py-1.5 rounded-lg bg-black/70 text-white text-xs">
                  可将图片、视频拖拽到参考区
                </span>
              </div>
            )}
            <div className="px-3 pt-3 pb-2 flex items-center gap-1.5">
              {/* 「添加参考素材」——选本地文件，按类型建 input-image /
                  input-audio / video-input 节点并自动连线到本节点（参考素材
                  是上游，缩略图随连线出现在右侧槽位）。gen-video 三类都吃。 */}
              <button
                type="button"
                tabIndex={-1}
                className="w-9 h-9 rounded-[10px] border-none bg-white/[0.06] hover:bg-white/[0.10] text-sidebar-foreground flex items-center justify-center cursor-pointer"
                title="上传参考素材（图片/音频/视频）"
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => {
                  e.stopPropagation();
                  pickRefUploadForNode({
                    targetId: id,
                    accept: 'image/*,audio/*,video/*',
                    allowed: ['input-image', 'input-audio', 'video-input'],
                    rejectHint: 'AI 视频仅支持图片/音频/视频',
                    onRejected: (name, reason) => {
                      modal.error({
                        title: '这个文件不支持',
                        content: renderRejected([{ name, reason }]),
                      });
                    },
                  });
                }}
              >
                <Plus size={14} />
              </button>
              {/* 已连接的上游图片缩略图——渲染在『添加』占位钮和尾部控件之间，
                  即「[⬚] [ref] [ref] [+]」排布。 */}
              {/* 外层不做 overflow-hidden：× 是骑角圆徽章会被裁半，裁剪层
                  内移到只包媒体的 div（三排同款处理）。 */}
              {imageRefs.slice(0, 4).map((ref, i) => (
                <div
                  key={`img-${ref.nodeId}-${i}`}
                  className="relative group/ref w-9 h-9 shrink-0"
                  title={`参考图 ${i + 1}`}
                >
                  <div className="w-full h-full rounded-[10px] overflow-hidden border border-white/10 bg-popover relative">
                    {showRefMedia && <ThumbImg src={ref.url} alt="" className="w-full h-full object-cover" draggable={false} />}
                    <span className="cv-ref-badge">图{i + 1}</span>
                  </div>
                  {refCloseBtn(ref.nodeId, `断开参考图 ${i + 1}`)}
                </div>
              ))}
              {videoRefs.slice(0, 2).map(({ url, nodeId: refNodeId }, i) => (
                <div
                  key={`vid-${refNodeId}-${i}`}
                  className="relative group/ref w-9 h-9 shrink-0"
                  title={`参考视频 ${i + 1}`}
                >
                  <div className="w-full h-full rounded-[10px] overflow-hidden border border-white/10 bg-black relative">
                  {/* preload=metadata 让我们拿到首帧作为缩略图，而不会强制
                      完整下载。 */}
                  {showRefMedia && <video src={url} crossOrigin={mediaCrossOrigin(url)} className="w-full h-full object-cover" muted preload="metadata" />}
                  <div className="absolute inset-0 flex items-center justify-center pointer-events-none bg-black/20">
                    <Video size={11} className="text-white/85" />
                  </div>
                  <span className="cv-ref-badge">视{i + 1}</span>
                  </div>
                  {refCloseBtn(refNodeId, `断开参考视频 ${i + 1}`)}
                </div>
              ))}
              {/* 音频没有可视缩略图，用麦克风图标做占位 chip；下标用 connectedAudios
                  自己的 i，与图片/视频各自独立编号（@音频1 @音频2…）。 */}
              {audioRefs.slice(0, 2).map(({ nodeId: refNodeId }, i) => (
                <div
                  key={`aud-${refNodeId}-${i}`}
                  className="relative group/ref w-9 h-9 shrink-0"
                  title={`参考音频 ${i + 1}`}
                >
                  <div className="w-full h-full rounded-[10px] overflow-hidden border border-white/10 bg-popover relative flex items-center justify-center">
                    <Mic size={13} className="text-white/85" />
                    <span className="cv-ref-badge">音{i + 1}</span>
                  </div>
                  {refCloseBtn(refNodeId, `断开参考音频 ${i + 1}`)}
                </div>
              ))}
              {totalRefCount > shownRefCount && (
                <span className="text-[10px] px-1.5 h-9 rounded-[10px] bg-popover text-sidebar-foreground inline-flex items-center shrink-0">
                  +{totalRefCount - shownRefCount}
                </span>
              )}
              <div className="flex-1" />
              <button
                type="button"
                className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent border-none bg-transparent cursor-pointer"
                title={bigEditOpen ? '收起编辑框' : '放大编辑框'}
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => { e.stopPropagation(); setBigEditOpen((v) => !v); }}
              >
                {bigEditOpen ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
              </button>
            </div>

            <PromptMentionEditor
              value={editorPrompt}
              refs={mediaRefs}
              placeholder="描述任何你想要生成的内容...（@ 引用参考图/视频/音频）"
              onChange={(v) => {
                setPromptDraft(v);
                updateNodeSettings(id, { videoPrompt: v });
                scheduleNodePatch(id, { settings: { videoPrompt: v } });
              }}
              onBlur={() => { void flushNodePatch(id); }}
              className={`px-3 mb-1 bg-transparent text-sm text-foreground transition-[min-height,max-height] duration-150 ${
                bigEditOpen
                  ? 'min-h-[360px] max-h-[60vh]'
                  : 'min-h-[88px] max-h-[200px]'
              }`}
            />

            <div className="px-3 pb-3 pt-2 flex items-center gap-1.5 flex-wrap">
              <div className="relative">
                <PillButton
                  active={openPopover === 'model'}
                  onClick={() => setOpenPopover((p) => (p === 'model' ? null : 'model'))}
                >
                  <BarChart3 size={12} className="text-muted-foreground" />
                  <span className="truncate max-w-[120px]">{selectedModel?.name ?? '选择模型'}</span>
                </PillButton>
                {openPopover === 'model' && (
                  <Popover onClose={() => setOpenPopover(null)} width={420}>
                    {videoModels.length === 0 && (
                      <div className="px-3 py-2 text-[11px] text-muted-foreground">未配置视频模型</div>
                    )}
                    {videoModels.map((m) => (
                      <PopoverItem
                        key={m.modelId}
                        active={m.modelId === selectedModelId}
                        multiline
                        onClick={() => {
                          // 换模型时把比例/分辨率/时长等参数重置为新模型的默认值，
                          // 避免旧模型的参数带到新模型导致非法/非其默认。
                          if (m.modelId !== selectedModelId) {
                            updateNodeSettings(id, { modelId: m.modelId, ...modelSettingDefaults(m.uiSchema) });
                            persistNodeById(id);
                          }
                          setOpenPopover(null);
                        }}
                      >
                        <ModelIcon model={m} />
                        <span className="min-w-0 flex-1 whitespace-normal break-words">{m.name}</span>
                      </PopoverItem>
                    ))}
                  </Popover>
                )}
              </div>

              {(ratioField || resolutionField) && (
                <span className="w-px h-5 bg-border/60 shrink-0" aria-hidden />
              )}
              {(ratioField || resolutionField) && (
                <div className="relative">
                  <PillButton
                    active={openPopover === 'ratio'}
                    onClick={() => setOpenPopover((p) => (p === 'ratio' ? null : 'ratio'))}
                  >
                    <span className="opacity-60 inline-block w-2.5 h-2.5 border border-current rounded-[2px]" />
                    <span>{ratioPillLabel || '比例'}</span>
                  </PillButton>
                  {openPopover === 'ratio' && (
                    <Popover onClose={() => setOpenPopover(null)} width={380}>
                      {resolutionField && (
                        <div className="p-3 pb-1.5">
                          <div className="text-[12px] text-muted-foreground mb-2">{resolutionField.label || '画质'}</div>
                          <div className="bg-[#333333] rounded-xl p-1">
                            <div
                              className="grid gap-1"
                              style={{ gridTemplateColumns: `repeat(${Math.max(2, Math.min(4, resolutionField.options.length))}, 1fr)` }}
                            >
                              {resolutionField.options.map((o) => {
                                const active = String(o.value) === resolutionValue;
                                return (
                                  <button
                                    key={String(o.value)}
                                    type="button"
                                    onMouseDown={(e) => e.stopPropagation()}
                                    onClick={(e) => { e.stopPropagation(); updateNodeSettings(id, { resolution: o.value }); persistNodeById(id); }}
                                    className={`px-3 py-2 rounded-[10px] text-[13px] font-medium border-none cursor-pointer transition-colors ${
                                      active ? 'bg-[#4d4d4d] text-foreground' : 'bg-transparent text-sidebar-foreground hover:bg-[#3d3d3d]'
                                    }`}
                                  >
                                    {o.label}
                                  </button>
                                );
                              })}
                            </div>
                          </div>
                        </div>
                      )}
                      {ratioField && (
                        <div className="p-3 pt-1.5">
                          <div className="text-[12px] text-muted-foreground mb-2">{ratioField.label || '比例'}</div>
                          <div className="bg-[#333333] rounded-xl p-1">
                            <RatioGrid
                              options={ratioField.options}
                              activeValue={ratioValue}
                              onPick={(v) => {
                                updateNodeSettings(id, { ratio: v });
                                persistNodeById(id);
                                try { localStorage.setItem('tvmao:lastVideoRatio', String(v)); } catch { /* 忽略 */ }
                              }}
                            />
                          </div>
                        </div>
                      )}
                    </Popover>
                  )}
                </div>
              )}

              {isVideoEditModel && (
                <Tooltip
                  title={readOnly ? '只读模式下不可用' : (connectedVideos.length === 0 ? '请先连接视频输入' : null)}
                  mouseEnterDelay={0}
                  placement="top"
                >
                  <span className="inline-flex shrink-0">
                    <button
                      onClick={(e) => { e.stopPropagation(); setAdvEditOpen(true); }}
                      onMouseDown={(e) => e.stopPropagation()}
                      disabled={connectedVideos.length === 0 || readOnly}
                      className="px-2 py-1 rounded-md text-[11px] text-sidebar-foreground hover:bg-accent border-none bg-transparent cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed shrink-0"
                    >
                      高级编辑
                    </button>
                  </span>
                </Tooltip>
              )}

              {otherSchemaEntries.map(([key, field]) => {
                const cur = settingValue(key, field.default);
                return (
                  <div key={key} className="relative">
                    <PillButton
                      active={openPopover === key}
                      onClick={() => setOpenPopover((p) => (p === key ? null : key))}
                    >
                      <span>
                        {field.options.find((o) => String(o.value) === String(cur))?.label ?? cur}
                      </span>
                    </PillButton>
                    {openPopover === key && (
                      <Popover onClose={() => setOpenPopover(null)}>
                        {field.options.map((o) => (
                          <PopoverItem
                            key={String(o.value)}
                            active={String(o.value) === String(cur)}
                            onClick={() => {
                              updateNodeSettings(id, { [key]: o.value });
                              persistNodeById(id);
                              setOpenPopover(null);
                            }}
                          >
                            {o.label}
                          </PopoverItem>
                        ))}
                      </Popover>
                    )}
                  </div>
                );
              })}

              <div className="flex-1" />

              {/* 装饰性麦克风——语音 prompt 不在本次范围内，所以这是个虚位，
                  只占右侧栏的位置，不凭空造功能。从 tab 顺序里隐藏。 */}
              <button
                type="button"
                tabIndex={-1}
                className="p-1.5 rounded-md text-muted-foreground/60 hover:text-muted-foreground border-none bg-transparent cursor-default"
                title="语音输入（暂未启用）"
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => e.stopPropagation()}
              >
                <Mic size={14} />
              </button>

              {nField && (
                <button
                  type="button"
                  title={`变体次数 (${nField.label || 'n'}) — 点击循环`}
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => {
                    e.stopPropagation();
                    const opts = nField.options;
                    const idx = opts.findIndex((o) => String(o.value) === String(nValue));
                    const next = opts[(idx + 1) % opts.length];
                    updateNodeSettings(id, { n: next.value });
                    persistNodeById(id);
                  }}
                  className="px-2 py-1 rounded-md text-[11px] text-sidebar-foreground hover:bg-accent border-none bg-transparent cursor-pointer min-w-[28px]"
                >
                  {variationPillLabel}
                </button>
              )}

              {/* disabled 按钮不派发鼠标事件，Tooltip 必须包一层 span 接管 hover（AntD 惯用法） */}
              <Tooltip title={submitTip} mouseEnterDelay={0} placement="top">
                <span className="inline-flex shrink-0">
                  <button
                    onClick={(e) => { e.stopPropagation(); handleGenerate(); }}
                    onMouseDown={(e) => e.stopPropagation()}
                    disabled={!selectedModelId || !hasPrompt || isGenerating || Boolean(inputLimitError)}
                    className="w-8 h-8 rounded-full bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer flex items-center justify-center border-none shrink-0"
                    aria-label="生成"
                  >
                    {isGenerating ? <span className="text-[11px]">…</span> : <ArrowUp size={14} />}
                  </button>
                </span>
              </Tooltip>
            </div>
          </div>
        </div>
      </NodeShell>
      {mediaMenu.overlay}
      {advEditOpen && createPortal(
        // portal 到 document.body：GenVideoNode 挂在 CanvasEditor 的
        // transform:translate/scale 容器里，transform 祖先会成为内部 `fixed`
        // 定位后代的包含块——不 portal 出去的话，弹框的 `fixed inset-0` 在画布
        // 缩放（zoom≠1）时不再等于视口，遮罩和面板整体错位。参照
        // NodeContextMenu.tsx / VideoInputNode.tsx 同款处理。
        <AdvancedEditDialog
          videoUrl={connectedVideos[0]}
          onClose={() => setAdvEditOpen(false)}
          onSave={async (entries, captureFrame) => {
            const st = useCanvasStore.getState();
            const gen = st.nodes.find((n) => n.id === id);
            // 同一次保存的多条标注拼在同一行、中文逗号分隔：
            // 每条独占一行会把输入框撑得很长。闭包每次 onSave 新建，这个标记天然
            // 就是批次边界——部分失败后重试是新一次 onSave，仍从新行开始。
            let batchFirst = true;
            const res = await saveAnnotations(entries, {
              captureFrame,
              materialize: async (blob, filename, i) => {
                // 放 gen 节点左侧纵向排开（addNode 按中心点放置，store.ts:184）
                const nid = st.addNode('input-image', { x: (gen?.x ?? 0) - 380, y: (gen?.y ?? 0) + i * 320 });
                if (!nid) throw new Error('画布只读，无法新建节点');
                // 上传只走 uploadFileIntoNode 这一个入口（见 utils/uploadIntoNode.ts
                // 的 WHY 注释）——它负责把 content/assetId/mimeType/status 一次性
                // 落库，不能自己另起一套 uploadAsset+updateNode 绕开它。
                const file = new File([blob], filename, { type: 'image/png' });
                const asset = await uploadFileIntoNode(nid, file);
                if (!asset) {
                  const failed = useCanvasStore.getState().nodes.find((n) => n.id === nid);
                  const msg = failed?.error || '上传失败';
                  // 上传失败要把刚建的空节点连带删掉，不能留着——弹框把这条记录
                  // 留在记录条里等用户重试（entries.slice(res.ok)，见 I3），重试
                  // 会再走一遍 materialize、再建一个新节点；不删的话每重试一次
                  // 就多一个挂着失败状态、连不上任何有效产物的孤儿节点。
                  useCanvasStore.getState().deleteNodes([nid]);
                  throw new Error(msg);
                }
                return nid;
              },
              connect: (nid) => st.addConnection(nid, id),
              appendPrompt: (line) => {
                const cur = (useCanvasStore.getState().nodes.find((n) => n.id === id)?.settings?.videoPrompt as string) ?? '';
                // 批次首条：已有内容时另起一行（不跟用户手写的提示词挤一行）；
                // 后续条：中文逗号接在同一行末尾。
                const next = batchFirst ? (cur ? `${cur}\n${line}` : line) : `${cur}，${line}`;
                batchFirst = false;
                updateNodeSettings(id, { videoPrompt: next });
                // 必须同步 promptDraft：用户在输入框打过字后 promptDraft 接管显示
                // （editorPrompt = promptDraft ?? settings），只写 settings 的话追加
                // 的提及在输入框里看不见——数据在、显示丢，用户以为保存没生效。
                setPromptDraft(next);
                persistNodeById(id);
              },
            });
            // 是否关框、以及部分失败时的错误文案/记录条剔除，全交给弹框自己按
            // SaveResult 处理——这里只负责成功提示，不再自己 setAdvEditOpen(false)。
            if (res.failedAt === undefined) {
              message.success(`已保存 ${res.ok} 条标注`);
            }
            return res;
          }}
        />,
        document.body,
      )}
    </>
  );
}

// ─── 构件块（GenImageNode 内部实现的镜像） ───────────────────────────────

function PillButton({
  active,
  children,
  onClick,
}: {
  active?: boolean;
  children: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onMouseDown={(e) => e.stopPropagation()}
      onClick={(e) => { e.stopPropagation(); onClick(); }}
      className={`px-3 py-1.5 rounded-[10px] text-[12px] flex items-center gap-1.5 transition-colors cursor-pointer border-none ${
        active
          ? 'bg-accent text-foreground'
          : 'bg-popover hover:bg-accent text-sidebar-foreground'
      }`}
    >
      {children}
    </button>
  );
}

function Popover({
  children,
  onClose,
  width = 200,
}: {
  children: React.ReactNode;
  onClose: () => void;
  width?: number;
}) {
  return (
    <>
      <div
        className="fixed inset-0 z-[200]"
        onClick={onClose}
        onMouseDown={(e) => e.stopPropagation()}
      />
      <div
        className="absolute bottom-full mb-2 left-0 z-[201] rounded-[20px] max-h-[440px] overflow-auto custom-scrollbar"
        style={{
          width,
          maxWidth: 'calc(100vw - 32px)',
          backgroundColor: '#2a2a2a',
          boxShadow:
            '0px 4px 16px rgba(0, 0, 0, 0.45), inset 0px 0.5px 0px rgba(255, 255, 255, 0.08)',
        }}
        onMouseDown={(e) => e.stopPropagation()}
        onClick={(e) => e.stopPropagation()}
      >
        {children}
      </div>
    </>
  );
}

function PopoverItem({
  active,
  children,
  multiline = false,
  onClick,
}: {
  active?: boolean;
  children: React.ReactNode;
  multiline?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onMouseDown={(e) => e.stopPropagation()}
      onClick={(e) => { e.stopPropagation(); onClick(); }}
      className={`w-full text-left px-2.5 py-1.5 text-[11px] flex gap-1.5 cursor-pointer transition-colors border-none ${
        multiline ? 'items-start' : 'items-center'
      } ${
        active ? 'bg-accent text-foreground' : 'bg-transparent hover:bg-accent/60 text-sidebar-foreground'
      }`}
    >
      {children}
    </button>
  );
}

function RatioGlyph({ w, h, size }: { w: number; h: number; size: number }) {
  let rectW: number;
  let rectH: number;
  if (w >= h) {
    rectW = size;
    rectH = (size * h) / w;
  } else {
    rectH = size;
    rectW = (size * w) / h;
  }
  return (
    <div
      style={{
        width: rectW,
        height: rectH,
        borderWidth: 1.5,
        borderStyle: 'solid',
        borderColor: 'currentColor',
        borderRadius: 3,
      }}
    />
  );
}

interface RatioOption { value: string | number; label: string }

function RatioTile({
  option,
  active,
  size,
  onPick,
}: {
  option: RatioOption;
  active: boolean;
  size: number;
  onPick: (v: string | number) => void;
}) {
  const m = String(option.value).match(/^(\d+):(\d+)$/) || String(option.label).match(/^(\d+):(\d+)$/);
  const w = m ? Number(m[1]) : 16;
  const h = m ? Number(m[2]) : 9;
  return (
    <button
      type="button"
      onMouseDown={(e) => e.stopPropagation()}
      onClick={(e) => { e.stopPropagation(); onPick(option.value); }}
      className={`flex flex-col items-center justify-center gap-1.5 py-2 rounded-[10px] border-none cursor-pointer transition-colors ${
        active ? 'bg-[#4d4d4d] text-foreground' : 'bg-transparent text-sidebar-foreground hover:bg-[#3d3d3d]'
      }`}
    >
      <div className="h-[24px] flex items-center justify-center">
        <RatioGlyph w={w} h={h} size={size} />
      </div>
      <span className="text-[11px]">{option.label}</span>
    </button>
  );
}

// 视频默认用宽方块布局：16:9 占据顶部一个宽方块，其余的在它下面填满一个 4 列
// 网格。当模型的选项里不含 16:9 时，回退到一个普通的 4 列网格。
function RatioGrid({
  options,
  activeValue,
  onPick,
}: {
  options: RatioOption[];
  activeValue: string;
  onPick: (v: string | number) => void;
}) {
  const wideIdx = options.findIndex((o) => String(o.value) === '16:9');
  if (wideIdx < 0) {
    return (
      <div className="grid grid-cols-4 gap-1.5">
        {options.map((o) => (
          <RatioTile key={String(o.value)} option={o} active={String(o.value) === activeValue} size={20} onPick={onPick} />
        ))}
      </div>
    );
  }
  const wide = options[wideIdx];
  const others = options.filter((_, i) => i !== wideIdx);
  return (
    <div className="grid grid-cols-5 gap-1.5">
      <button
        type="button"
        onMouseDown={(e) => e.stopPropagation()}
        onClick={(e) => { e.stopPropagation(); onPick(wide.value); }}
        className={`row-span-2 flex flex-col items-center justify-center gap-2 rounded-[10px] border-none cursor-pointer transition-colors ${
          String(wide.value) === activeValue ? 'bg-[#4d4d4d] text-foreground' : 'bg-transparent text-sidebar-foreground hover:bg-[#3d3d3d]'
        }`}
      >
        <RatioGlyph w={16} h={9} size={36} />
        <span className="text-[12px] font-medium">{wide.label}</span>
      </button>
      {others.slice(0, 8).map((o) => (
        <RatioTile key={String(o.value)} option={o} active={String(o.value) === activeValue} size={18} onPick={onPick} />
      ))}
    </div>
  );
}

function ModelIcon({ model }: { model: ModelConfig | undefined }) {
  if (!model) return <span className="w-3 h-3 rounded bg-muted shrink-0" />;
  const id = model.modelId.toLowerCase();
  let color = 'bg-purple-500';
  if (id.includes('doubao') || id.includes('seedance')) color = 'bg-orange-500';
  else if (id.includes('wan')) color = 'bg-blue-500';
  else if (id.includes('kling')) color = 'bg-pink-500';
  else if (id.includes('hailuo') || id.includes('minimax')) color = 'bg-emerald-500';
  else if (id.includes('runway') || id.includes('luma')) color = 'bg-cyan-500';
  return <span className={`w-3 h-3 rounded ${color} shrink-0`} />;
}

