import { useReconcileCanvasTask } from './useReconcileCanvasTask';
import { stallIfUnreachable, usePollStalled } from './pollStall';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  ArrowUp,
  BarChart3,
  ChevronLeft,
  ChevronRight,
  Image as ImageIcon,
  LayoutGrid,
  Maximize2,
  Mic,
  Minimize2,
  Plus,
  Grid2x2Plus,
  SlidersHorizontal,
  Upload,
  X,
} from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { App as AntApp, Tooltip } from 'antd';
import { useCanvasStore, useConnectedInputs } from '../store';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';
import {
  flushNodePatch,
  persistNodeById,
  scheduleNodePatch,
} from '../persistence/canvasMutations';
import { NodeShell } from '../NodeShell';
import {
  fetchGenImagePresets,
  listRuntimeModels,
  submitCanvasTask,
  pollCanvasTaskUntilDone,
  uploadAsset,
  GenImagePresetItem,
  ModelConfig,
  ModelUISchema,
  UISchemaField,
  MULTI_ANGLE_PRESET_KEY,
} from '@/shared/api';
import { calculateResolution, nodeBoxFromDims } from '../utils/resolution';
import { groupPresets, splitPresetColumns, PRESET_COL_W } from '../utils/presetMenu';
import { PresetIcon } from '../utils/presetIcon';
import { pokerStackOffset } from '../utils/pokerStack';
import { useGenerationTimer } from '../hooks/useGenerationTimer';
import { buildDynamicInputs, buildImageGenerationInputs, composePrompt, modelSettingDefaults } from '../utils/generationInputs';
import {
  appendHistoryItem,
  historyPromptAt,
  normalizeHistory,
  readPendingGen,
} from '../utils/genHistory';
import { PromptMentionEditor, serializeMentionPrompt, stripDeadMentions } from '../PromptMentionEditor';
import { useThumbUrl } from '../thumbs';
import { useStoryboardMode } from '../../storyboard/StoryboardContext';
import { getDragNodeId, hasDragNode, useNodeDragActive } from '../../storyboard/dragConnect';
import { useMediaNodeMenu } from './useMediaNodeMenu';
import { ImageActionsBar } from './ImageActionsBar';
import { ThumbImg } from '../ThumbImg';
import { pickRefUploadForNode } from '../utils/uploadRefPicker';
import { renderRejected } from '../utils/rejectedFiles';
import { inputLimitViolation } from '../utils/inputLimits';

export function GenImageNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodesById.get(id));
  // 兜底对账:节点卡在 generating 且丢 taskId 时按 nodeId 找回任务号(useReconcileCanvasTask 顶部注释)。
  useReconcileCanvasTask(id);
  // 轮询连续够不着后端时挂起(P23):不判失败,交给对账;NodeShell 上可点「重新检查」。
  const pollStalled = usePollStalled(node?.taskId);
  const { modal } = AntApp.useApp();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const linked = useConnectedInputs(id);
  const addNode = useCanvasStore((s) => s.addNode);
  const setLightbox = useCanvasStore((s) => s.setLightbox);
  // 选中状态驱动浮动上传 chip 和 prompt 面板的显示/隐藏动画——用户选中节点
  // 之前一直隐藏，聚焦时展开。
  const isSelected = useCanvasStore((s) => s.selectedIds.has(id));
  // 宫格切分模式里选中态浮层（prompt 面板、上传 chip）整体让位：网格叠在节点
  // 上时面板还展开着会盖住格子（用户点名要隐藏）。退出切分即恢复。
  const gridSplitting = useCanvasStore((s) => s.gridSplittingNodeId === id);
  // 下挂面板（多角度/打光）开在本节点上时同理让位——两者叠同一块空间，
  // 提示词面板更宽会从编辑器两侧露边（用户点名）。
  const dockPanelOpen = useCanvasStore((s) => s.dockPanelNodeId === id);
  // 标注模式同理让位：绘制层叠在节点上时下方输入框还展开着（用户点名）。
  const annotating = useCanvasStore((s) => s.annotatingNodeId === id);
  const overlaysVisible = isSelected && !gridSplitting && !dockPanelOpen && !annotating;
  // 故事板详情面板里这张图是**主角**：占据整个预览区、用户就是来看清楚的。
  // 所以那里直接上原图，也不需要再双击开 Lightbox（开出来还是同一张）。
  // 画布上相反——节点只有两三百像素宽，必须用缩略图（一张画布可能挂上百个
  // 节点，全换原图流量翻好几倍），也必须留着双击预览，不然看不清。
  const storyboardMode = useStoryboardMode();
  // 参数面板未选中时只是 opacity:0，参考图 chip 照样在加载（P16）。只在选中时渲染
  // 媒体；故事板里面板常显，不门控。
  const showRefMedia = isSelected || storyboardMode;
  // 故事板里参考区支持连线编辑：
  // 缩略图 hover 出 × 断开、左侧列表卡片拖进参数面板即连上。两个动作都直接
  // 走 store（removeConnection/addConnection 自带 commit 可撤销、去重、落库）。
  const [refDragOver, setRefDragOver] = useState(false);
  // 拖拽全程（不只是悬停到面板上）都要给反馈：灰虚线=拖起来了、这里能放；
  // 紫虚线=已经悬停在面板上。
  const nodeDragActive = useNodeDragActive();
  const disconnectRef = (upstreamId: string) => {
    const st = useCanvasStore.getState();
    // 同一对节点理论上只有一条连线（addConnection 去重），filter 是兜底。
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

  // 提示词预设配置（后台「绘图预设设置」维护）。低频变化，5 分钟内画布上所有
  // gen-image 节点共享一份缓存；未配置时后端返回空数组，菜单入口自然不渲染。
  const presetsQuery = useQuery({
    queryKey: ['gen-image-presets'],
    queryFn: fetchGenImagePresets,
    staleTime: 300_000,
  });

  // 预设菜单弹层：记录触发按钮的屏幕矩形，portal 到 body 后按它定位。
  // null = 关闭。不能像 Popover 那样 absolute 挂在节点子树里——画布 zoom≠1 时
  // 节点子树内的 fixed/absolute 弹层会随祖先 transform 错位（仓内铁律）。
  const presetBtnRef = useRef<HTMLButtonElement>(null);
  const [presetMenuRect, setPresetMenuRect] = useState<DOMRect | null>(null);
  // 预设参数面板弹层（带 params 的预设，chip ⚙ 打开的是它而不是换预设菜单）。
  // 与 presetMenuRect 同一套机制：记录锚点按钮矩形，portal 到 body 按它定位。
  const [presetParamsRect, setPresetParamsRect] = useState<DOMRect | null>(null);

  // 预设 chip 的实际宽度：编辑器 firstLineIndent 要按它算（名称长短不定）。
  // 用 callback ref + ResizeObserver 而不是 effect——chip 的挂载由 activePreset
  // 驱动，而那是 early-return 之后才能算的值，effect 依赖不到。
  // jsdom 没有 ResizeObserver（offsetWidth 也恒 0），测试环境下缩进恒 0，无害。
  const [presetChipW, setPresetChipW] = useState(0);
  const presetChipRO = useRef<ResizeObserver | null>(null);
  const presetChipRefCb = (el: HTMLSpanElement | null) => {
    presetChipRO.current?.disconnect();
    presetChipRO.current = null;
    if (!el) {
      setPresetChipW(0);
      return;
    }
    setPresetChipW(el.offsetWidth);
    if (typeof ResizeObserver !== 'undefined') {
      const ro = new ResizeObserver(() => setPresetChipW(el.offsetWidth));
      ro.observe(el);
      presetChipRO.current = ro;
    }
  };

  // 单一 popover 状态。取值为 'model' | 'ratio' | <schema-key>
  // | null。互斥，打开一个会关掉其它任何一个。
  const [openPopover, setOpenPopover] = useState<string | null>(null);
  const [bigEditOpen, setBigEditOpen] = useState(false);
  const [galleryOpen, setGalleryOpen] = useState(false);
  // 「发送到画布」定义在下面（依赖一堆本地状态），而菜单 hook 要在组件顶层调用，
  // 所以用 ref 转一手：菜单项被点到时才读它，避开 const 的 TDZ。
  const sendToCanvasRef = useRef<() => void>(() => {});
  const mediaMenu = useMediaNodeMenu({
    url: node?.content,
    kind: 'image',
    nodeId: id,
    extraItems: [
      { key: 'send', label: '发送到画布', onPick: () => sendToCanvasRef.current() },
    ],
  });

  if (!node) return null;

  const imageModels = (modelsQuery.data ?? []).filter((m) => m.type === 'image');
  const selectedModelId =
    (node.settings?.modelId as string | undefined) ?? imageModels[0]?.modelId ?? '';
  const selectedModel = imageModels.find((m) => m.modelId === selectedModelId);
  const uiSchema: ModelUISchema = selectedModel?.uiSchema ?? {};

  // ── 提示词预设 ────────────────────────────────────────────────────────────
  // 整套功能绑模型：当前选中模型命中配置的 models 列表才显示菜单入口。
  // settings.promptPreset 只存 key（落库用 key、展示用 name）；
  // key 在配置里查不到（被后台删了）时 chip 不渲染、提交时视为未选——与服务端
  // 「查不到就静默忽略」的规则两侧一致。
  const presetsConfig = presetsQuery.data;
  const presetModelMatched = Boolean(presetsConfig?.models.includes(selectedModelId));
  const promptPresetKey = (node.settings?.promptPreset as string | undefined) ?? '';
  // 多角度编辑器铺的节点用保留 key 走同一条 presetKey 通道——它不在绘图预设
  // 配置里，不能参与 activePreset 查表/chip 渲染，但提交时要原样透传。
  const isMultiAngle = promptPresetKey === MULTI_ANGLE_PRESET_KEY;
  const activePreset =
    presetModelMatched && promptPresetKey && !isMultiAngle
      ? presetsConfig?.presets.find((p) => p.key === promptPresetKey)
      : undefined;
  // 当前预设的可调参数维度。非空时 chip 的 ⚙ 分叉成「打开参数面板」——换预设
  // 只走底部入口按钮；无参数的预设 ⚙ 维持原样打开换预设菜单。
  const presetParamDims = activePreset?.value.params ?? [];
  const hasPresetParams = presetParamDims.length > 0;
  // settings.presetParams 只存用户显式点选过的维度（维度 key → 选项 key）；
  // 未选维度不补全——服务端对缺失维度自动落 default，快照里少存少错。
  const presetParams =
    (node.settings?.presetParams as Record<string, string> | undefined) ?? {};

  const prompt = (node.settings?.prompt as string | undefined) ?? '';
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
  // 改词重生成后翻回旧图，还能看到旧图当时用的那句话。
  // 向后兼容（见 utils/genHistory.ts）：老快照里的 `string[]` 会被归一化成
  // `{url}`，缺失的 prompt 一律回退到节点当前 `settings.prompt`，即存量画布的
  // 表现与改造前逐字一致。有 `node.content` 但没有 `history` 的节点，仍被当作
  // 只有一条、用该 URL 初始化的 history。
  const history = normalizeHistory(node.settings?.history, node.content);
  const settingsIdx = node.settings?.historyIndex as number | undefined;
  const historyIndex =
    typeof settingsIdx === 'number' && settingsIdx >= 0 && settingsIdx < history.length
      ? settingsIdx
      : Math.max(0, history.length - 1);
  const currentUrl = history[historyIndex]?.url ?? '';
  // 节点内预览走缩略图；Lightbox、下游连线读的 content 仍是原图 URL。
  const currentThumb = useThumbUrl(currentUrl);

  // 翻到历史版本时，输入框直接显示那一版当时用的提示词。
  //
  // 只对「非最新版」这么做。停在最新版时必须显示 `settings.prompt`——那是用户
  // 正在写、还没生成的草稿；用最新版已生成的词去盖它，用户改到一半的内容就没了。
  // 老数据里条目不带 prompt，historyPromptAt 回退到 `prompt` 本身，所以两条分支
  // 取值相同，存量节点看不出任何变化。
  const isLatestVersion = historyIndex === history.length - 1;
  const viewingPrompt = historyPromptAt(history, historyIndex, prompt);
  // 在旧版上直接改输入框时，改的内容要立刻显示出来（它同时写进 settings.prompt
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
  // （比如视频模型上的 duration——这里没有，但保持和 GenVideoNode 一致的约定）。
  const otherSchemaEntries = schemaEntries.filter(([k]) => !['ratio', 'resolution', 'n'].includes(k));

  const settingValue = (key: string, defaultValue?: string | number) => {
    const v = node.settings?.[key];
    if (v !== undefined && v !== null) return v as string | number;
    return defaultValue ?? '';
  };

  const ratioValue = String(settingValue('ratio', ratioField?.default ?? '1:1'));
  const resolutionValue = String(settingValue('resolution', resolutionField?.default ?? ''));
  const nValue = settingValue('n', nField?.default ?? 1);

  // 用*真实输出尺寸*（而不只是 ratio 字符串）来驱动节点宽高比——无论
  // `resolution` 是抽象档位（1K/2K/…）还是显式的 "WxH"，也无论模型是否暴露
  // `ratio` 字段，都能工作。手动 resize 被禁用（NodeShell `resizable={false}`）。
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

  // 只要这个 gen-image 节点持有一个进行中的异步任务，就恢复/驱动 polling。
  // 同 PreviewNode 的循环，但锚在 gen-image 节点自身上，这样结果落到
  // `node.content` 而不是下游预览。刷新页面会恢复进行中的 poll。
  useEffect(() => {
    if (node.status === 'generating' && node.taskId && !pollStalled) {
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
              // 存下 assetId：节点靠它做「保存到素材库」「Seedance 合规校验」，
              // 这两件事都需要 asset 身份，光有 URL 办不了。
              updateNodeSettings(id, {
                history: next,
                historyIndex: nextIdx,
                pendingGen: undefined,
                // 0（文件落了本地但素材记录没写成功）也照存，与服务端 sink 一致——
                // 「素材未登记」角标靠它认出来（见 desktop/unsavedLocal）。
                assetId: finalTask.assetIds?.[0] ?? undefined,
                kind: 'image',
                // 新结果属于这次任务：清掉上次下载失败留下的标记，否则重试会拿旧任务覆盖新结果。
                unsaved: undefined,
                genTaskId: undefined,
              });
              updateNode(id, { content: url, status: 'succeeded', error: undefined, taskId: undefined });
            } else {
              updateNode(id, { status: 'failed', error: '上游未返回结果', taskId: undefined });
            }
          } else {
            updateNode(id, {
              status: 'failed',
              error: finalTask.error || '生成失败',
              taskId: undefined,
            });
          }
          // 结果已由后端权威写回快照；前端这次持久化只为补前端独有字段
          //（history/assetId/kind）以及在页面开着时同步终态 status/taskId。
          persistNodeById(id);
        })
        .catch((err: any) => {
          if (cancelled || err?.message === 'aborted') return;
          if (node.taskId && stallIfUnreachable(node.taskId, err)) return;
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
  }, [node.status, node.taskId, pollStalled, id, updateNode, updateNodeSettings]);

  // 为新建节点在首次挂载时初始化 `settings.ratio`。从 localStorage 读
  // `tvmao:lastImageRatio`；如果该值是当前模型 ratio 选项之一就复用它，否则
  // 回退到第一个选项。用户已经选过 ratio 时跳过。
  useEffect(() => {
    if (!ratioField) return;
    const current = node.settings?.ratio;
    if (current !== undefined && current !== null && current !== '') return;
    const validValues = ratioField.options.map((o) => String(o.value));
    if (validValues.length === 0) return;
    let pick = validValues[0];
    try {
      const saved = localStorage.getItem('tvmao:lastImageRatio');
      if (saved && validValues.includes(saved)) pick = saved;
    } catch { /* 忽略 */ }
    updateNodeSettings(id, { ratio: pick });
    persistNodeById(id);
    // 只在 ratio 选项变得可用时运行；我们不想在这里对每次 settings 变化都
    // 做出反应。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ratioField]);

  const connectedImages = linked.images;

  // 切到未绑定预设的模型时自动清掉已选预设——「不命中就完全不显示」的语义下，
  // chip 悬空没有任何入口能删它。配置未加载完（data 还是 undefined）不动手：
  // 网络慢时误清用户已选比晚一拍清理更糟。
  useEffect(() => {
    if (!presetsConfig || !promptPresetKey) return;
    // 多角度保留 key 不归绘图预设的模型绑定管（它有自己的启用列表），这条
    // 「模型不命中就清预设」的清理对它不适用——不特判会把角度参数悄悄抹掉。
    if (promptPresetKey === MULTI_ANGLE_PRESET_KEY) return;
    if (presetsConfig.models.includes(selectedModelId)) return;
    // presetParams 跟着 chip 一起清：参数是预设的从属，预设没了参数没有意义。
    updateNodeSettings(id, { promptPreset: undefined, presetParams: undefined });
    // 预设选择是低频动作，直接整节点落库，不走 1.5s 防抖（防抖是给连续打字的）。
    persistNodeById(id);
  }, [presetsConfig, promptPresetKey, selectedModelId, id, updateNodeSettings]);

  // 预设的图片数约束：对照上游连线图片数，不满足就禁用生成按钮并给出灰字原因。
  // maxImages 可以是 0（纯文生图预设不许连图），所以判定必须用 typeof 而不是真值。
  const presetMinImages =
    typeof activePreset?.value.minImages === 'number' ? activePreset.value.minImages : undefined;
  const presetMaxImages =
    typeof activePreset?.value.maxImages === 'number' ? activePreset.value.maxImages : undefined;
  const presetImagesOk =
    (presetMinImages === undefined || connectedImages.length >= presetMinImages) &&
    (presetMaxImages === undefined || connectedImages.length <= presetMaxImages);
  const presetImagesHint = presetImagesOk
    ? null
    : presetMinImages !== undefined && presetMaxImages !== undefined
      ? presetMinImages === presetMaxImages
        ? `该预设需要恰好 ${presetMinImages} 张图片`
        : `该预设需要连接 ${presetMinImages}-${presetMaxImages} 张图片`
      : presetMinImages !== undefined
        ? `该预设需要至少 ${presetMinImages} 张图片`
        : presetMaxImages === 0
          ? '该预设不支持连接图片'
          : `该预设需要至多 ${presetMaxImages} 张图片`;

  // 模型 gen_control.inputLimits 的图片数上限（如豆包 5.0-pro 最多 5 张）：与预设约束合并成
  // 同一个「图片数」闸门。图片节点只喂图片给上游，视频/音频按 0 计。文案与后端逐字一致。
  const modelImagesHint = inputLimitViolation(selectedModel?.genControl?.inputLimits, {
    image: connectedImages.length,
    video: 0,
    audio: 0,
  });
  const imagesOk = presetImagesOk && !modelImagesHint;
  const imagesHint = presetImagesHint ?? modelImagesHint;

  const pickPreset = (key: string) => {
    // 换预设时清掉旧预设的参数选择——维度 key 是各预设私有的，带过去要么悬空
    // 要么撞名生效，两种都错。
    updateNodeSettings(id, { promptPreset: key, presetParams: undefined });
    persistNodeById(id);
    setPresetMenuRect(null);
  };
  const clearPreset = () => {
    updateNodeSettings(id, { promptPreset: undefined, presetParams: undefined });
    persistNodeById(id);
  };
  // 参数面板点选某维度的档位：merge 该维度并即时落库。面板不关闭——用户常
  // 连调多个维度，点一下关一下要反复重开；关闭只由遮罩点击触发。
  const pickPresetParam = (dimKey: string, optKey: string) => {
    updateNodeSettings(id, { presetParams: { ...presetParams, [dimKey]: optKey } });
    persistNodeById(id);
  };
  // 图片节点只把图片喂给上游(buildImageGenerationInputs 只收 images)——@ 引用
  // 也只开放图片,避免 @视频1 序列化出上游拿不到的引用。
  const mediaRefs = useMemo(() => linked.refs.filter((r) => r.kind === 'image'), [linked.refs]);
  const refsFingerprint = mediaRefs.map((r) => `${r.kind}:${r.nodeId}`).join('|');
  // 断线自动清理：与 GenVideoNode 同机制。
  useEffect(() => {
    const stripped = stripDeadMentions(prompt, mediaRefs);
    if (stripped !== prompt) {
      updateNodeSettings(id, { prompt: stripped });
      persistNodeById(id);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refsFingerprint, prompt]);
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
  // 选中的预设也算一个提示词来源：服务端会按 presetKey 拼上预设正文，预设的
  // placeholder 文案就是「点击生成，直接基于当前图像生成…」——不放行的话这句
  // 引导就是骗人的。
  const hasPrompt = Boolean(prompt.trim() || fallbackPrompt.trim() || activePreset);

  // 用户是否已经在上游连了媒体（图片 / 音频 / 视频，包括能解析成媒体 URL 的
  // PreviewNode 输出）。为 true 时隐藏上传 chip——用户已经在喂参考图了。
  const hasUpstreamMedia =
    linked.images.length + linked.audios.length + linked.videos.length > 0;

  // 点「上传」——把这个节点从 gen-image 变形成一个持有上传图片的通用 preview
  // 节点。preview 是通用的「媒体容器」类型（kind: 'image' | 'video'），AI 生成
  // 结果和用户上传的素材都用它，所以我们不再分叉出一个专门的 input-image 类型。
  // 下游连线保持不变；变形后节点的 content 仍然能被消费图片的 gen 节点通过标准
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
      updateNodeSettings(id, { assetId: asset.id, kind: 'image' });
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
        (n.settings?.sourceGenImageId as string | undefined) === id,
    ).length;
    const { dx, dy } = pokerStackOffset(existingCount);
    const pos = {
      x: node.x + node.width + dx + PREVIEW_HALF.x,
      y: node.y + dy + PREVIEW_HALF.y,
    };
    const newId = addNode('preview', pos);
    if (!newId) return;
    updateNode(newId, { content: currentUrl, status: 'succeeded' });
    updateNodeSettings(newId, { sourceGenImageId: id, kind: 'image' });
    persistNodeById(newId);
  };
  sendToCanvasRef.current = handleSendToCanvas;

  const handleGenerate = async () => {
    if (!selectedModelId) return;
    // 预设图片数约束的兜底：按钮 disabled 是体验层，这里再拦一道（服务端还有
    // 权威校验，三层同一规则）。
    if (!imagesOk) return;
    // 提交给上游的最终提示词：@ 提及已展开成「图片2」这类纯文本，再与上游文本
    // 节点合并（composePrompt，上游在前）。这也是要记进 history 的那一句——记
    // 「实际生成用的」而不是编辑器里的原始标记，这样以后连线变了、上游节点删了，
    // 回看依然读得懂。必须和下面 buildImageGenerationInputs 里那句逐字相同，所以
    // 两边共用 composePrompt，不各写一份。
    const submittedPrompt = composePrompt(serializeMentionPrompt(prompt, mediaRefs), fallbackPrompt);

    // 结果内联渲染在这个节点自身上：每次生成成功都
    // 追加到 `settings.history`，这样用户可以回翻之前的结果。
    //
    // pendingGen：把这次提交的提示词/参数当场落库，等结果回来时再写进 history
    // 条目。不能等到那时候再读输入框——用户边等边改词是常态，那样记下来的就是
    // 下一版的词。每次生成都整个覆盖，所以失败遗留的残值不会串到下一次。
    //
    // 预设只在配置里查得到（activePreset 非空）才带：key 被后台删了就当没选，
    // 与服务端「查不到静默忽略」一致。presetName 是当时的显示名快照——历史条目
    // 回显不按 key 反查配置，预设日后删/改名也不影响旧记录。
    updateNodeSettings(id, {
      pendingGen: {
        prompt: submittedPrompt,
        params: dynamicInputs,
        submittedAt: new Date().toISOString(),
        ...(activePreset
          ? {
              presetKey: activePreset.key,
              presetName: activePreset.value.name,
              // 参数选择同属「提交那一刻的事实」，只在用户真选过时才占字段。
              ...(hasPresetParams && Object.keys(presetParams).length > 0
                ? { presetParams }
                : {}),
            }
          : isMultiAngle
            ? { presetKey: MULTI_ANGLE_PRESET_KEY, presetName: '多角度视角', presetParams }
            : {}),
      },
    });
    updateNode(id, {
      status: 'generating',
      error: undefined,
      taskId: undefined,
      // 故意不清空 content——旧图保持可见（带一层「生成中」浮层），直到
      // 新图 resolve。
    });
    // 先把「生成中」状态落库，再发起任务（点击生成先 PATCH 当前节点参数/状态）。
    persistNodeById(id);
    try {
      const projectId =
        Number(window.location.pathname.split('/canvas/')[1]?.split('/')[0]) || undefined;

      // 方案 B:恒异步,提交画布任务后保持 generating + 持久化画布任务 id;
      // 上面的 polling effect 拿状态(含 assetId)展示,结果由后端权威写回快照。
      const canvasTask = await submitCanvasTask({
        projectId: projectId!,
        nodeId: id,
        modelId: selectedModelId,
        inputs: buildImageGenerationInputs({
          // @[图片:<id>] → 「图片2」纯文本,下标与 images 数组顺序一致。
          prompt: serializeMentionPrompt(prompt, mediaRefs),
          fallbackPrompt,
          dynamicInputs,
          connectedImages,
        }),
        // 预设只传 key，不做任何 prompt 拼接——正文对前端保密，拼接收口在服务端。
        // 带参数的预设再附上用户已选的维度（可为空对象——缺失维度服务端落 default，
        // 前端不做默认值补全，两侧只认一份 default 真源）。
        // 多角度保留 key 同通道透传：presetParams 里是角度数值(rotation/tilt/zoom/preset)，
        // 角度 → 中文描述的翻译在服务端 resolver 完成。
        ...(activePreset
          ? {
              presetKey: activePreset.key,
              ...(hasPresetParams ? { presetParams } : {}),
            }
          : isMultiAngle
            ? { presetKey: MULTI_ANGLE_PRESET_KEY, presetParams }
            : {}),
      });
      updateNode(id, { taskId: canvasTask.taskId });
      persistNodeById(id);
    } catch (err: any) {
      const msg = err?.response?.data?.error || err?.message || '生成失败';
      // 保持当前 content 可见；只把 error 暴露出来。
      updateNode(id, { status: 'failed', error: msg, taskId: undefined });
      persistNodeById(id);
    }
  };

  // pill 上展示选项 label 而不是原始 value——分辨率档位的 value 可能是内部
  // ID/尺寸串，只有 label 才是给用户看的。
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
        titleExtra={mediaMenu.badge}
        minWidth={240}
        minHeight={200}
      >
      {/* 有图可操作时才显示（生成中/空节点没有可操作对象，也避免与下面的
          「上传」chip 抢同一片上方空间）。 */}
      <ImageActionsBar nodeId={id} visible={!!currentUrl && !storyboardMode} />
      {/* 预览上方的浮动「上传」chip。只在该节点还没有上游媒体时显示——一旦
          连了任何东西，上传只会盖住输入，所以我们干脆把这个交互完全隐藏。
          点击 → 把节点变形成 input-image（见 handleUpload）。 */}
      {!hasUpstreamMedia && !currentUrl && (
        <button
          type="button"
          className="absolute z-30 flex items-center gap-1.5 px-3.5 py-1.5 rounded-full bg-popover/90 hover:bg-accent text-sidebar-foreground text-[12px] cursor-pointer border border-border/40 shadow-md backdrop-blur-sm"
          style={{
            top: -42,
            left: '50%',
            transform: `translateX(-50%) translateY(${overlaysVisible ? '0' : '6px'})`,
            opacity: overlaysVisible ? 1 : 0,
            transition: 'opacity 180ms ease-out, transform 180ms ease-out',
            pointerEvents: overlaysVisible ? 'auto' : 'none',
          }}
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => { e.stopPropagation(); fileInputRef.current?.click(); }}
          title="上传图片（节点将转为图片节点）"
        >
          <Upload size={12} />
          上传
        </button>
      )}
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={handleUpload}
      />

      {/* 「Image」标题 chip 现在统一由 NodeShell 渲染在卡片上方，并支持重命名
          ——不再每个节点各放一份。 */}

      {/* ===== 节点 body —— 预览方块 ====================== */}
      <div className="w-full h-full p-2 flex flex-col gap-2">
        {(isGenerating || finalDuration !== null) && (
          <div className="px-2 py-0.5 rounded text-[10px] font-mono text-center bg-blue-500/20 text-blue-400 border border-blue-500/30 shrink-0">
            {isGenerating ? `⏱ ${elapsed.toFixed(1)}s` : `✓ ${finalDuration!.toFixed(1)}s`}
          </div>
        )}

        <div className="relative flex-1 rounded-2xl bg-popover/30 border border-border/60 flex items-center justify-center overflow-hidden min-h-0">
          {/* 预览 = `history[historyIndex]`。重新生成期间，旧图原地保留并
              加一层变暗浮层，直到新图 resolve，这样用户不会丢失上下文。 */}
          {currentUrl ? (
            <img
              src={storyboardMode ? currentUrl : currentThumb}
              crossOrigin={mediaCrossOrigin(storyboardMode ? currentUrl : currentThumb)}
              loading="lazy"
              decoding="async"
              className={`w-full h-full object-contain ${storyboardMode ? '' : 'cursor-zoom-in'}`}
              alt=""
              draggable={false}
              onDoubleClick={storyboardMode ? undefined : (e) => {
                e.stopPropagation();
                setLightbox({ url: currentUrl, kind: 'image', title: 'AI 绘图' });
              }}
              onContextMenu={(e) => {
                e.preventDefault();
                mediaMenu.onContextMenu(e);
              }}
            />
          ) : (
            <ImageIcon size={36} className="text-muted-foreground/60" />
          )}

          {/* 新生成进行中时的加载浮层（只在已有图片显示时可见——空状态时由
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
                title="上一张"
              >
                <ChevronLeft size={14} />
              </button>
              <button
                type="button"
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => { e.stopPropagation(); showHistoryAt(historyIndex + 1); }}
                className="absolute right-1.5 w-7 h-7 rounded-full bg-black/55 hover:bg-black/75 text-white border-none cursor-pointer flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity"
                style={{ top: '50%', transform: 'translateY(-50%)' }}
                title="下一张"
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
                    className="absolute top-9 right-1.5 z-[201] w-[220px] max-h-60 overflow-auto custom-scrollbar p-1.5 rounded-xl"
                    style={{ backgroundColor: '#2a2a2a', boxShadow: '0px 4px 16px rgba(0,0,0,0.45), inset 0px 0.5px 0px rgba(255,255,255,0.08)' }}
                    onMouseDown={(e) => e.stopPropagation()}
                    onClick={(e) => e.stopPropagation()}
                  >
                    <div className="grid grid-cols-3 gap-1">
                      {history.map((item, i) => (
                        <div
                          key={`${item.url}-${i}`}
                          className="relative aspect-square rounded-md overflow-hidden cursor-pointer group/thumb"
                          onClick={() => { showHistoryAt(i); setGalleryOpen(false); }}
                          style={{ outline: i === historyIndex ? '2px solid #1fa2dc' : 'none', outlineOffset: '-2px' }}
                          title={`版本 ${i + 1}\n${historyPromptAt(history, i, prompt)}`}
                        >
                          <ThumbImg src={item.url} className="w-full h-full object-cover" draggable={false} alt="" />
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
                让面板在屏幕上恒定 680px 宽、字号恒定（可读性不随缩放变化）。
                translate 放在 scale 之后，居中与 12px 间隙都按屏幕像素算。
                这层 transform 随 zoom 每帧变化，不能挂 transition（会橡皮筋
                滞后），选中滑入动画下沉到内层 bg-card。====================== */}
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
          pointerEvents: overlaysVisible ? 'auto' : 'none',
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
            transform: `translateY(${overlaysVisible ? '0' : '-6px'})`,
            opacity: overlaysVisible ? 1 : 0,
            transition: 'opacity 180ms ease-out, transform 180ms ease-out',
          }}
          // 故事板里整块参数面板都是连线的 drop 目标——比只给缩略图行一个
          // 40px 高的窄条好瞄得多。dragOver 必须 preventDefault，否则浏览器
          // 不派发 drop。leave/drop 都要清高亮，漏一个就常亮。
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
            // addConnection 自带自环/重复检查与落库，这里不用再验。
            useCanvasStore.getState().addConnection(draggedId, id);
          } : undefined}
        >
          {/* 拖拽期间面板中央的提示。pointer-events-none 是必须的：这层盖在
              面板上，拦了事件的话 dragover/drop 就落不到面板、永远连不上线。 */}
          {storyboardMode && (nodeDragActive || refDragOver) && (
            <div className="absolute inset-0 z-20 flex items-center justify-center pointer-events-none rounded-[20px]">
              <span className="px-3 py-1.5 rounded-lg bg-black/70 text-white text-xs">
                可将图片、视频拖拽到参考区
              </span>
            </div>
          )}
          <div className="px-3 pt-3 pb-2 flex items-center gap-1.5">
            {/* 「添加参考图」——选本地图片，建 input-image 节点并自动连线到
                本节点（参考图是上游，缩略图随连线出现在右侧槽位）。gen-image
                只吃图片，音/视频在这里没有语义。 */}
            <button
              type="button"
              tabIndex={-1}
              className="w-9 h-9 rounded-[10px] border-none bg-white/[0.06] hover:bg-white/[0.10] text-sidebar-foreground flex items-center justify-center cursor-pointer"
              title="上传参考图"
              onMouseDown={(e) => e.stopPropagation()}
              onClick={(e) => {
                e.stopPropagation();
                pickRefUploadForNode({
                  targetId: id,
                  accept: 'image/*',
                  allowed: ['input-image'],
                  rejectHint: 'AI 绘图仅支持图片',
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
                即「[⬚] [ref] [ref] [+]」模式。图片连线本身仍然
                承载权威状态；这里只是个「参考图已挂上」的视觉确认。 */}
            {mediaRefs.slice(0, 4).map((ref, i) => (
              // 外层不做 overflow-hidden：× 是骑在右上角、半出边框的圆徽章，
              // 裁剪层挂外层会把它裁掉半个。圆角裁剪由内层
              // 这个只包媒体的 div 负责。
              <div
                key={`${ref.nodeId}-${i}`}
                className="relative group/ref w-9 h-9 shrink-0"
                title={`参考图 ${i + 1}`}
              >
                <div className="w-full h-full rounded-[10px] overflow-hidden border border-white/10 bg-popover">
                  {showRefMedia && <ThumbImg src={ref.url} alt="" className="w-full h-full object-cover" draggable={false} />}
                </div>
                {/* hover 出 × 断开这条上游连线。只在故事板给：画布上删连线有
                    自己的交互（点线），这里再放一个会两套入口打架。ref 带
                    nodeId，能精确定位是哪条连线——URL 定位不行，两个节点可能
                    持有同一张图。 */}
                {storyboardMode && (
                  <button
                    type="button"
                    aria-label={`断开参考图 ${i + 1}`}
                    onMouseDown={(e) => e.stopPropagation()}
                    onClick={(e) => {
                      e.stopPropagation();
                      disconnectRef(ref.nodeId);
                    }}
                    className="absolute -top-1.5 -right-1.5 w-[18px] h-[18px] rounded-full bg-black/85 hover:bg-black text-white border border-white/60 cursor-pointer items-center justify-center hidden group-hover/ref:flex z-10"
                  >
                    {/* 不用 lucide 的 X：它的笔画只占名义尺寸的一半（24 视图里从 6 画到
                        18），标称调多大视觉都是半截。这里要叉撑满圆，直接画。 */}
                    <svg width={12} height={12} viewBox="0 0 12 12" aria-hidden>
                      <path d="M1.5 1.5 L10.5 10.5 M10.5 1.5 L1.5 10.5" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" />
                    </svg>
                  </button>
                )}
              </div>
            ))}
            {connectedImages.length > 4 && (
              <span className="text-[10px] px-1.5 h-9 rounded-[10px] bg-popover text-sidebar-foreground inline-flex items-center shrink-0">
                +{connectedImages.length - 4}
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

          {/* 单选预设 chip——「视觉内嵌」进输入框首行：chip 是
              编辑器外的独立元素，绝对定位叠在 contenteditable 首行行首，编辑器
              用 firstLineIndent 给它让位——不进 mention 文档流（进了会被序列化
              进提示词，而预设正文本来就不该出现在 prompt 里，前端也拿不到）。
              key 在配置里查不到时 activePreset 为 undefined，chip 自然不渲染。 */}
          <div className="relative">
            {activePreset && (
              <span
                ref={presetChipRefCb}
                className="group/preset absolute left-3 top-0 z-10 inline-flex items-center gap-1.5 max-w-[70%] h-[22px] rounded-lg bg-white/[0.08] border border-white/10 pl-1 pr-1.5 text-[11px] text-foreground select-none"
                onMouseDown={(e) => e.stopPropagation()}
              >
                {/* 左侧：静默态是彩色图标底座，hover 整个 chip 时原位变 × 删除
                    （删除在左侧、图标位复用，不另占宽度）。 */}
                <button
                  type="button"
                  aria-label="移除预设"
                  className="w-4 h-4 rounded-[5px] p-0 border-none cursor-pointer flex items-center justify-center shrink-0 bg-violet-500 text-white group-hover/preset:bg-white/20"
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => { e.stopPropagation(); clearPreset(); }}
                >
                  <span className="flex group-hover/preset:hidden">
                    <PresetIcon icon={activePreset.value.icon} size={11} />
                  </span>
                  <X size={11} className="hidden group-hover/preset:block" />
                </button>
                <span className="truncate">{activePreset.value.name}</span>
                {/* 右侧 ⚙：行为按预设是否带 params 分叉——带参数的预设打开参数
                    面板（换预设只走底部入口按钮）；无参数的预设维持
                    原样就地打开换预设菜单（chip 在输入框顶部，上方放不下时
                    弹层向下弹，见各自弹层定位处）。 */}
                <button
                  type="button"
                  aria-label={hasPresetParams ? '预设参数' : '更换预设'}
                  className="p-0 w-3.5 h-3.5 border-none bg-transparent text-muted-foreground hover:text-foreground cursor-pointer flex items-center justify-center shrink-0"
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => {
                    e.stopPropagation();
                    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
                    if (hasPresetParams) setPresetParamsRect((r) => (r ? null : rect));
                    else setPresetMenuRect((r) => (r ? null : rect));
                  }}
                >
                  <SlidersHorizontal size={11} />
                </button>
              </span>
            )}
          <PromptMentionEditor
            value={editorPrompt}
            refs={mediaRefs}
            // 选中预设后空输入框显示该预设的引导文案（预设通常自带完整正文，
            // 用户可以什么都不写直接点生成）；预设没配 placeholder 则保持默认。
            placeholder={activePreset?.value.placeholder || '描述任何你想要生成的内容...（@ 引用参考图）'}
            // chip 实际宽度 + 8px 间距：首行文字/placeholder 从 chip 右侧接着排。
            firstLineIndent={activePreset && presetChipW > 0 ? presetChipW + 8 : 0}
            onChange={(v) => {
              setPromptDraft(v);
              updateNodeSettings(id, { prompt: v });
              // prompt 输入停止 1.5s 后 PATCH 当前节点 settings；连续输入不重复提交。
              scheduleNodePatch(id, { settings: { prompt: v } });
            }}
            onBlur={() => { void flushNodePatch(id); }}
            className={`px-3 mb-1 bg-transparent text-sm text-foreground transition-[min-height,max-height] duration-150 ${
              bigEditOpen
                ? 'min-h-[360px] max-h-[60vh]'
                : 'min-h-[88px] max-h-[200px]'
            }`}
          />
          </div>

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
                  {imageModels.length === 0 && (
                    <div className="px-3 py-2 text-[11px] text-muted-foreground">未配置图片模型</div>
                  )}
                  {imageModels.map((m) => (
                    <PopoverItem
                      key={m.modelId}
                      active={m.modelId === selectedModelId}
                      multiline
                      onClick={() => {
                        // 换模型时把比例/分辨率/张数等参数重置为新模型的默认值，
                        // 避免旧模型的参数(如 16:9)带到新模型导致非法/非其默认。
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
                              try { localStorage.setItem('tvmao:lastImageRatio', String(v)); } catch { /* 忽略 */ }
                            }}
                          />
                        </div>
                      </div>
                    )}
                  </Popover>
                )}
              </div>
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

            {/* 预设菜单入口——位置在底部参数行，参数 pill 之后、
                竖分隔线隔开（「… 1张 | ✨」）。整套功能绑模型：当前模型不在配置的
                models 里就完全不渲染（不是禁用），语义上此模型根本没有这个功能。 */}
            {presetModelMatched && (
              <>
                <span className="w-px h-5 bg-border/60 shrink-0" aria-hidden />
                {/* group + absolute 自绘 tooltip 替代原生 title——原生浏览器
                    tooltip 是系统浅色样式，和暗色主题不搭（用户点名）。
                    absolute 挂节点子树内没问题：它随节点一起被画布缩放，
                    与 fixed 弹层的 zoom 错位铁律无关。 */}
                <span className="relative group/pbtn inline-flex">
                  <button
                    ref={presetBtnRef}
                    type="button"
                    className={`p-1.5 rounded-lg border-none cursor-pointer transition-colors ${
                      presetMenuRect || activePreset
                        ? 'bg-accent text-foreground'
                        : 'bg-transparent text-muted-foreground hover:text-foreground hover:bg-accent'
                    }`}
                    aria-label="预设"
                    onMouseDown={(e) => e.stopPropagation()}
                    onClick={(e) => {
                      e.stopPropagation();
                      // 开时记录按钮矩形供 portal 弹层定位（开在按钮上方）；再点一次收起。
                      setPresetMenuRect((r) =>
                        r ? null : presetBtnRef.current?.getBoundingClientRect() ?? null,
                      );
                    }}
                  >
                    {/* Grid2x2Plus：预设入口图标（方块组+加号）。 */}
                    <Grid2x2Plus size={14} />
                  </button>
                  <span className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1.5 hidden group-hover/pbtn:block px-2 py-1 rounded-md bg-popover text-foreground text-[11px] border border-white/10 shadow-lg whitespace-nowrap pointer-events-none select-none">
                    预设
                  </span>
                </span>
              </>
            )}

            <div className="flex-1" />

            {/* 装饰性麦克风——语音 prompt 尚未支持，所以这是个虚位，
                占住右侧栏的位置，但不凭空造功能。从 tab 顺序里隐藏。 */}
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

            {/* 预设图片数不满足时的原因灰字——按钮 disabled 后 Tooltip 要 hover
                才看得到，数量约束这种「要去连线才能修好」的原因得常驻可见。 */}
            {imagesHint && (
              <span className="shrink-0 text-[11px] text-muted-foreground select-none">
                {imagesHint}
              </span>
            )}

            {/* 禁用原因即显（mouseEnterDelay=0）——原生 title 要悬停 ~1s 且 disabled 上
                行为不一，用户发现不了原因；disabled 按钮不派发鼠标事件，须包 span 接管 hover */}
            <Tooltip
              title={isGenerating ? '生成中…' : !selectedModelId ? '请先选择模型' : !hasPrompt ? '请先输入提示词' : imagesHint}
              mouseEnterDelay={0}
              placement="top"
            >
              <span className="inline-flex shrink-0">
                <button
                  onClick={(e) => { e.stopPropagation(); handleGenerate(); }}
                  onMouseDown={(e) => e.stopPropagation()}
                  disabled={!selectedModelId || !hasPrompt || isGenerating || !imagesOk}
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
      {/* 预设菜单弹层。portal 到 body + fixed 定位（仓内铁律：节点子树内的
          fixed 弹层在画布 zoom≠1 时会随祖先 transform 错位，先例 NodeContextMenu）。
          遮罩 mousedown stopPropagation：不拦的话会穿透到画布触发框选/取消选中。 */}
      {presetMenuRect && presetsConfig && (() => {
        // 多列排布：一列按高度估算放不下就把整组流到下一列，
        // 不出滚动条；列数封顶后才由 overflow-auto 兜底（splitPresetColumns 注释）。
        const columns = splitPresetColumns(groupPresets(presetsConfig.presets));
        const colCount = Math.max(1, columns.length);
        const panelW = colCount * PRESET_COL_W + (colCount - 1) * 6 + 12;
        const left = Math.max(8, Math.min(presetMenuRect.left, window.innerWidth - panelW - 8));
        // 默认开在锚点上方（底部入口按钮：菜单向上展开）。
        // 用 bottom 锚定而不是算 top：面板高度随条目数变化，bottom 锚定天然
        // 贴着按钮往上长，不用预知实际高度。
        // 从 chip 的 ⚙ 打开时锚点在输入框顶部，上方放不下就翻到锚点下方。
        const pos: React.CSSProperties =
          presetMenuRect.top < PRESET_MENU_MAX_H + 24
            ? { top: presetMenuRect.bottom + 8 }
            : {
                bottom: Math.min(
                  window.innerHeight - 8,
                  Math.max(8, window.innerHeight - presetMenuRect.top + 8),
                ),
              };
        return createPortal(
          <>
            <div
              className="fixed inset-0 z-[300]"
              onClick={() => setPresetMenuRect(null)}
              onMouseDown={(e) => e.stopPropagation()}
            />
            <div
              className="fixed z-[301] overflow-auto custom-scrollbar rounded-2xl border border-solid border-white/25 bg-muted p-1.5 shadow-[0_12px_32px_rgba(0,0,0,0.55)]"
              style={{ left, ...pos, width: panelW, maxHeight: PRESET_MENU_MAX_H }}
              onMouseDown={(e) => e.stopPropagation()}
              onClick={(e) => e.stopPropagation()}
              onWheel={(e) => e.stopPropagation()}
            >
              {presetsConfig.presets.length === 0 && (
                <div className="px-2.5 py-2 text-[11px] text-muted-foreground">未配置预设</div>
              )}
              <div className="flex items-start gap-1.5">
              {columns.map((colGroups, ci) => (
              <div key={ci} className="flex-1 min-w-0">
              {colGroups.map((group) => (
                <div key={group.category}>
                  <div className="px-2.5 pt-2 pb-1 text-[11px] text-muted-foreground select-none">
                    {group.category}
                  </div>
                  {group.items.map((p) => (
                    <button
                      key={p.key}
                      type="button"
                      onMouseDown={(e) => e.stopPropagation()}
                      onClick={(e) => { e.stopPropagation(); pickPreset(p.key); }}
                      className={`w-full text-left px-2.5 py-2 rounded-lg flex items-center gap-2 border-none cursor-pointer transition-colors ${
                        p.key === promptPresetKey
                          ? 'bg-accent text-foreground'
                          : 'bg-transparent hover:bg-accent/60 text-sidebar-foreground'
                      }`}
                    >
                      {/* 深色圆角方块底座 + 线性图标——条目图标形态。 */}
                      <span className="w-8 h-8 rounded-[10px] bg-white/[0.07] border border-white/10 flex items-center justify-center shrink-0 text-foreground">
                        <PresetIcon icon={p.value.icon} size={16} />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block text-[12px] leading-snug">{p.value.name}</span>
                        {p.value.description && (
                          <span className="block text-[10px] leading-snug text-muted-foreground mt-0.5">
                            {p.value.description}
                          </span>
                        )}
                      </span>
                    </button>
                  ))}
                </div>
              ))}
              </div>
              ))}
              </div>
            </div>
          </>,
          document.body,
        );
      })()}
      {/* 预设参数面板弹层。同预设菜单一样 portal 到 body + fixed（zoom≠1 错位
          铁律）+ 遮罩 stopPropagation。锚在 chip 的 ⚙ 上：默认开上方，上方放
          不下翻到下方（同款翻转逻辑）。activePreset 兜底判空：面板开着时预设
          被清（如后台热更配置把 params 删了），弹层直接消失而不是渲染空壳。 */}
      {presetParamsRect && hasPresetParams && (() => {
        const panelW = 264;
        const left = Math.max(8, Math.min(presetParamsRect.left, window.innerWidth - panelW - 8));
        const pos: React.CSSProperties =
          presetParamsRect.top < PRESET_PARAMS_MAX_H + 24
            ? { top: presetParamsRect.bottom + 8 }
            : {
                bottom: Math.min(
                  window.innerHeight - 8,
                  Math.max(8, window.innerHeight - presetParamsRect.top + 8),
                ),
              };
        return createPortal(
          <>
            <div
              className="fixed inset-0 z-[300]"
              onClick={() => setPresetParamsRect(null)}
              onMouseDown={(e) => e.stopPropagation()}
            />
            <div
              className="fixed z-[301] overflow-auto custom-scrollbar rounded-2xl border border-solid border-white/25 bg-muted p-2.5 shadow-[0_12px_32px_rgba(0,0,0,0.55)]"
              style={{ left, ...pos, width: panelW, maxHeight: PRESET_PARAMS_MAX_H }}
              onMouseDown={(e) => e.stopPropagation()}
              onClick={(e) => e.stopPropagation()}
              onWheel={(e) => e.stopPropagation()}
            >
              {presetParamDims.map((dim) => {
                // 选中态判定：用户选过就用选的，没选过高亮该维度的 default 档
                // （配置漏标 default 时退回第一个）——与服务端「缺失落 default」
                // 的口径一致，面板上看到的就是实际会生效的档。
                const selectedKey =
                  presetParams[dim.key] ??
                  (dim.options.find((o) => o.default) ?? dim.options[0])?.key;
                return (
                  <div key={dim.key} className="mb-2.5 last:mb-0">
                    <div className="px-0.5 pb-1 text-[11px] text-muted-foreground select-none">
                      {dim.name}
                    </div>
                    <div
                      className="grid gap-1"
                      style={{ gridTemplateColumns: `repeat(${Math.max(1, dim.options.length)}, 1fr)` }}
                    >
                      {dim.options.map((o) => {
                        const active = o.key === selectedKey;
                        return (
                          <button
                            key={o.key}
                            type="button"
                            aria-pressed={active}
                            onMouseDown={(e) => e.stopPropagation()}
                            onClick={(e) => { e.stopPropagation(); pickPresetParam(dim.key, o.key); }}
                            className={`px-2 py-1.5 rounded-lg text-[12px] border-none cursor-pointer transition-colors ${
                              active
                                ? 'bg-accent text-foreground'
                                : 'bg-transparent hover:bg-accent/60 text-sidebar-foreground'
                            }`}
                          >
                            {o.name}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                );
              })}
            </div>
          </>,
          document.body,
        );
      })()}
      {mediaMenu.overlay}
    </>
  );
}

// 弹层最大高度——超过时由 overflow-auto 兜底滚动（多列排布已尽量避免走到这步，
// 见 utils/presetMenu.ts 的 splitPresetColumns）。定位在渲染前算，只能用常量。
const PRESET_MENU_MAX_H = 400;

// 参数面板同理：翻转判定在渲染前算，只能用常量。比预设菜单矮——维度数通常
// 只有三五个，给到 360 已远超常见配置，超出由 overflow-auto 兜底。
const PRESET_PARAMS_MAX_H = 360;

// ─── 构件块 ────────────────────────────────────────────────────────────────

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

// W:H 宽高比矩形的视觉轮廓，缩放到 `size`-px 的方框内。用在 RatioTile 里，
// 让每个 ratio 选项都有一个*看起来*就是实际比例的图形，而不是一个纯文字 pill。
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
  const w = m ? Number(m[1]) : 1;
  const h = m ? Number(m[2]) : 1;
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

// ratio 网格：1:1 占据左侧一个高方块，其余的在它右边填满一个
// 4 列网格。当模型的选项里不含 1:1 时，回退到一个普通的 4 列网格。
function RatioGrid({
  options,
  activeValue,
  onPick,
}: {
  options: RatioOption[];
  activeValue: string;
  onPick: (v: string | number) => void;
}) {
  const sqIdx = options.findIndex((o) => String(o.value) === '1:1');
  if (sqIdx < 0) {
    return (
      <div className="grid grid-cols-4 gap-1.5">
        {options.map((o) => (
          <RatioTile key={String(o.value)} option={o} active={String(o.value) === activeValue} size={20} onPick={onPick} />
        ))}
      </div>
    );
  }
  const sq = options[sqIdx];
  const others = options.filter((_, i) => i !== sqIdx);
  return (
    <div className="grid grid-cols-5 gap-1.5">
      <button
        type="button"
        onMouseDown={(e) => e.stopPropagation()}
        onClick={(e) => { e.stopPropagation(); onPick(sq.value); }}
        className={`row-span-2 flex flex-col items-center justify-center gap-2 rounded-[10px] border-none cursor-pointer transition-colors ${
          String(sq.value) === activeValue ? 'bg-[#4d4d4d] text-foreground' : 'bg-transparent text-sidebar-foreground hover:bg-[#3d3d3d]'
        }`}
      >
        <RatioGlyph w={1} h={1} size={32} />
        <span className="text-[12px] font-medium">{sq.label}</span>
      </button>
      {others.slice(0, 8).map((o) => (
        <RatioTile key={String(o.value)} option={o} active={String(o.value) === activeValue} size={18} onPick={onPick} />
      ))}
    </div>
  );
}

// 一个小色块，让每个模型在选择器里都有一个可辨识的圆点。系列的判定是基于
// modelId 的启发式——如果以后想手动控制，管理端可以在 ModelConfig 上暴露一个
// 真正的 `iconColor` 字段。
function ModelIcon({ model }: { model: ModelConfig | undefined }) {
  if (!model) return <span className="w-3 h-3 rounded bg-muted shrink-0" />;
  const id = model.modelId.toLowerCase();
  let color = 'bg-blue-500';
  if (id.includes('doubao') || id.includes('seedream') || id.includes('seedance')) color = 'bg-orange-500';
  else if (id.includes('flux')) color = 'bg-purple-500';
  else if (id.includes('gemini')) color = 'bg-emerald-500';
  else if (id.includes('grok')) color = 'bg-rose-500';
  return <span className={`w-3 h-3 rounded ${color} shrink-0`} />;
}
