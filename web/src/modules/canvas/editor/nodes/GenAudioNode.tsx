import { useReconcileCanvasTask } from './useReconcileCanvasTask';
import { useEffect, useMemo, useRef, useState } from 'react';
import {
  ArrowUp,
  BarChart3,
  ChevronLeft,
  ChevronRight,
  LayoutGrid,
  Maximize2,
  Minimize2,
  Music,
  Upload,
  X,
} from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { Tooltip } from 'antd';
import { useCanvasStore, useConnectedInputs } from '../store';
import { useStoryboardMode } from '../../storyboard/StoryboardContext';
import {
  flushNodePatch,
  persistNodeById,
  scheduleNodePatch,
} from '../persistence/canvasMutations';
import { NodeShell } from '../NodeShell';
import {
  listRuntimeModels,
  submitCanvasTask,
  pollCanvasTaskUntilDone,
  uploadAsset,
  ModelConfig,
  ModelUISchema,
  UISchemaField,
} from '@/shared/api';
import { pokerStackOffset } from '../utils/pokerStack';
import { useGenerationTimer } from '../hooks/useGenerationTimer';
import { buildDynamicInputs, buildAudioGenerationInputs, composePrompt, modelSettingDefaults } from '../utils/generationInputs';
import {
  appendHistoryItem,
  historyPromptAt,
  normalizeHistory,
  readPendingGen,
} from '../utils/genHistory';
import { PromptMentionEditor, serializeMentionPrompt, stripDeadMentions } from '../PromptMentionEditor';
import { useMediaNodeMenu } from './useMediaNodeMenu';
import { WaveformPlayer } from './WaveformPlayer';
import { AudioActionsBar } from './AudioActionsBar';

// GenAudioNode 是 GenImageNode 的音频版复制适配：同一套「模型选择 + @提及提示词 +
// 估价 + 异步提交/轮询 + 多版本 history」流程，砍掉图片特有的比例/分辨率/尺寸联动
// （音频没有画面），预览区换成 <audio controls> 播放器。上游连线支持文本节点
// （提示词基底）与音频节点（@音频N 指定音色，走 inputs.audios）。
export function GenAudioNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodesById.get(id));
  // 兜底对账:节点卡在 generating 且丢 taskId 时按 nodeId 找回任务号(useReconcileCanvasTask 顶部注释)。
  useReconcileCanvasTask(id);
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const linked = useConnectedInputs(id);
  const addNode = useCanvasStore((s) => s.addNode);
  const isSelected = useCanvasStore((s) => s.selectedIds.has(id));
  // 截取/变速模式条挂节点正下方，与提示词面板抢同一块
  // 空间——编辑会话期间面板让位隐藏，退出后随选中态自然回来。
  const audioEditing = useCanvasStore((s) => s.audioEdit?.nodeId === id);
  const promptPanelVisible = isSelected && !audioEditing;
  // 详情面板里参数面板要跟预览盒左右对齐（见下方定位处的注释）。
  const storyboardMode = useStoryboardMode();

  const fileInputRef = useRef<HTMLInputElement>(null);

  const modelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 60_000,
  });

  const [openPopover, setOpenPopover] = useState<string | null>(null);
  const [bigEditOpen, setBigEditOpen] = useState(false);
  const [galleryOpen, setGalleryOpen] = useState(false);
  const sendToCanvasRef = useRef<() => void>(() => {});
  const mediaMenu = useMediaNodeMenu({
    url: node?.content,
    kind: 'audio',
    nodeId: id,
    extraItems: [
      { key: 'send', label: '发送到画布', onPick: () => sendToCanvasRef.current() },
    ],
  });

  if (!node) return null;

  const audioModels = (modelsQuery.data ?? []).filter((m) => m.type === 'audio');
  const selectedModelId =
    (node.settings?.modelId as string | undefined) ?? audioModels[0]?.modelId ?? '';
  const selectedModel = audioModels.find((m) => m.modelId === selectedModelId);
  const uiSchema: ModelUISchema = selectedModel?.uiSchema ?? {};

  const prompt = (node.settings?.prompt as string | undefined) ?? '';
  const isGenerating = node.status === 'generating';
  // 起点 = 提交时刻（持久化在 pendingGen 里），刷新页面后计时条不归零。
  const submittedAt = readPendingGen(node.settings?.pendingGen)?.submittedAt;
  const { elapsed, finalDuration } = useGenerationTimer(
    isGenerating,
    submittedAt ? Date.parse(submittedAt) || undefined : undefined,
  );

  // 多版本 history，与 GenImageNode 同机制（见其注释）；条目是音频 URL。
  const history = normalizeHistory(node.settings?.history, node.content);
  const settingsIdx = node.settings?.historyIndex as number | undefined;
  const historyIndex =
    typeof settingsIdx === 'number' && settingsIdx >= 0 && settingsIdx < history.length
      ? settingsIdx
      : Math.max(0, history.length - 1);
  const currentUrl = history[historyIndex]?.url ?? '';

  const isLatestVersion = historyIndex === history.length - 1;
  const viewingPrompt = historyPromptAt(history, historyIndex, prompt);
  const [promptDraft, setPromptDraft] = useState<string | null>(null);
  const editorPrompt = promptDraft ?? (isLatestVersion ? prompt : viewingPrompt);

  const showHistoryAt = (idx: number) => {
    const safe = ((idx % history.length) + history.length) % history.length;
    setPromptDraft(null);
    updateNodeSettings(id, { historyIndex: safe });
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

  // 音频没有比例/分辨率的特殊地位——uiSchema 的所有字段一律渲染成通用参数 pill。
  const schemaEntries = Object.entries(uiSchema).filter(([, v]) => v != null) as Array<[string, UISchemaField]>;

  const settingValue = (key: string, defaultValue?: string | number) => {
    const v = node.settings?.[key];
    if (v !== undefined && v !== null) return v as string | number;
    return defaultValue ?? '';
  };

  // 只要这个 gen-audio 节点持有一个进行中的异步任务，就恢复/驱动 polling。
  // 与 GenImageNode 同机制：结果由后端权威写回快照，这里轮询仅为即时展示。
  useEffect(() => {
    if (node.status === 'generating' && node.taskId) {
      const ctrl = new AbortController();
      let cancelled = false;
      pollCanvasTaskUntilDone(node.taskId, { signal: ctrl.signal })
        .then((finalTask) => {
          if (cancelled) return;
          if (finalTask.status === 'succeeded') {
            const url = finalTask.outputUrls?.[0];
            if (url) {
              const latest = useCanvasStore.getState().nodes.find((n) => n.id === id);
              const next = appendHistoryItem(
                normalizeHistory(latest?.settings?.history, latest?.content),
                url,
                readPendingGen(latest?.settings?.pendingGen),
                new Date().toISOString(),
              );
              const nextIdx = Math.max(0, next.findIndex((h) => h.url === url));
              updateNodeSettings(id, {
                history: next,
                historyIndex: nextIdx,
                pendingGen: undefined,
                assetId: finalTask.assetIds?.[0] || undefined,
                kind: 'audio',
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [node.status, node.taskId, id, updateNode, updateNodeSettings]);

  const connectedAudios = linked.audios;
  // 音频节点只把音频（音色参考）喂给上游——@ 引用也只开放音频，避免 @图片1
  // 序列化出上游拿不到的引用。
  const mediaRefs = useMemo(() => linked.refs.filter((r) => r.kind === 'audio'), [linked.refs]);
  const refsFingerprint = mediaRefs.map((r) => `${r.kind}:${r.nodeId}`).join('|');
  // 断线自动清理：与 GenImageNode 同机制。
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [schemaFingerprint, settingsFingerprint],
  );
  const fallbackPrompt = linked.texts.filter(Boolean).join('\n');
  const hasPrompt = Boolean(prompt.trim() || fallbackPrompt.trim());

  const hasUpstreamMedia =
    linked.images.length + linked.audios.length + linked.videos.length > 0;

  // 点「上传」——把这个节点变形成 input-audio 节点。不像 gen-image 转 preview：
  // preview 只渲染 image/video，而 input-audio 自带播放器，且两侧分类器
  // （store.getConnectedInputs / canvasinputs.Connected）天然认这个类型。
  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    const projectId =
      Number(window.location.pathname.split('/canvas/')[1]?.split('/')[0]) || undefined;
    try {
      const asset = await uploadAsset(file, projectId);
      updateNode(id, {
        type: 'input-audio',
        content: asset.url,
        status: 'succeeded',
        error: undefined,
      });
      updateNodeSettings(id, { assetId: asset.id, kind: 'audio' });
      persistNodeById(id);
    } catch (err) {
      updateNode(id, { error: '上传失败' });
      persistNodeById(id);
    }
  };

  // input-audio 默认尺寸 300x200（store.ts）——addNode 以给定点为中心。
  const AUDIO_HALF = { x: 150, y: 100 };

  const handleSendToCanvas = () => {
    if (!currentUrl) return;
    // 点击时现取：订阅整个 nodes 数组会让本节点随画布上任意节点的每帧拖动重渲染。
    const existingCount = useCanvasStore.getState().nodes.filter(
      (n) =>
        n.type === 'input-audio' &&
        (n.settings?.sourceGenAudioId as string | undefined) === id,
    ).length;
    const { dx, dy } = pokerStackOffset(existingCount);
    const pos = {
      x: node.x + node.width + dx + AUDIO_HALF.x,
      y: node.y + dy + AUDIO_HALF.y,
    };
    const newId = addNode('input-audio', pos);
    if (!newId) return;
    updateNode(newId, { content: currentUrl, status: 'succeeded' });
    updateNodeSettings(newId, { sourceGenAudioId: id, kind: 'audio' });
    persistNodeById(newId);
  };
  sendToCanvasRef.current = handleSendToCanvas;

  const handleGenerate = async () => {
    if (!selectedModelId) return;
    const submittedPrompt = composePrompt(serializeMentionPrompt(prompt, mediaRefs), fallbackPrompt);

    updateNodeSettings(id, {
      pendingGen: { prompt: submittedPrompt, params: dynamicInputs, submittedAt: new Date().toISOString() },
    });
    updateNode(id, {
      status: 'generating',
      error: undefined,
      taskId: undefined,
    });
    persistNodeById(id);
    try {
      const projectId =
        Number(window.location.pathname.split('/canvas/')[1]?.split('/')[0]) || undefined;

      const canvasTask = await submitCanvasTask({
        projectId: projectId!,
        nodeId: id,
        modelId: selectedModelId,
        inputs: buildAudioGenerationInputs({
          // @[音频:<id>] → 「音频2」纯文本,下标与 audios 数组顺序一致。
          prompt: serializeMentionPrompt(prompt, mediaRefs),
          fallbackPrompt,
          dynamicInputs,
          connectedAudios,
        }),
      });
      updateNode(id, { taskId: canvasTask.taskId });
      persistNodeById(id);
    } catch (err: any) {
      const msg = err?.response?.data?.error || err?.message || '生成失败';
      updateNode(id, { status: 'failed', error: msg, taskId: undefined });
      persistNodeById(id);
    }
  };

  return (
    <>
      <NodeShell
        node={node}
        inputs={1}
        bodyOverflow="visible"
        radiusPx={16}
        resizable={false}
        titleExtra={mediaMenu.badge}
        minWidth={300}
        minHeight={160}
      >
      {/* 音频工具条：有结果才有可操作对象。上传 chip 与它互斥出现（chip 只在
          无结果时显示），不会在节点上方叠罗汉。 */}
      <AudioActionsBar nodeId={id} visible={!!currentUrl && !storyboardMode} url={currentUrl} />
      {/* 浮动「上传」chip——与 GenImageNode 同交互；点击 → 节点变形为 input-audio。 */}
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
          title="上传音频（节点将转为音频输入节点）"
        >
          <Upload size={12} />
          上传
        </button>
      )}
      <input
        ref={fileInputRef}
        type="file"
        accept="audio/*"
        className="hidden"
        onChange={handleUpload}
      />

      {/* ===== 节点 body —— 播放器区 ====================== */}
      <div className="w-full h-full p-2 flex flex-col gap-2">
        {(isGenerating || finalDuration !== null) && (
          <div className="px-2 py-0.5 rounded text-[10px] font-mono text-center bg-blue-500/20 text-blue-400 border border-blue-500/30 shrink-0">
            {isGenerating ? `⏱ ${elapsed.toFixed(1)}s` : `✓ ${finalDuration!.toFixed(1)}s`}
          </div>
        )}

        <div className="relative flex-1 rounded-2xl bg-popover/30 border border-border/60 flex flex-col items-center justify-center overflow-hidden min-h-0 gap-2 px-3">
          {currentUrl ? (
            /* key=url：切 history 版本 / 新生成结果落地时整体重挂载，播放态与波形
               不会串到别的版本上。 */
            <div
              className="w-full h-full py-2"
              onContextMenu={(e) => {
                e.preventDefault();
                mediaMenu.onContextMenu(e);
              }}
            >
              <WaveformPlayer key={currentUrl} url={currentUrl} nodeId={id} className="w-full h-full" />
            </div>
          ) : (
            <Music size={32} className="text-muted-foreground/60" />
          )}

          {/* 重新生成进行中时的浮层（已有音频显示时可见）。 */}
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
                title="上一条"
              >
                <ChevronLeft size={14} />
              </button>
              <button
                type="button"
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => { e.stopPropagation(); showHistoryAt(historyIndex + 1); }}
                className="absolute right-1.5 w-7 h-7 rounded-full bg-black/55 hover:bg-black/75 text-white border-none cursor-pointer flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity"
                style={{ top: '50%', transform: 'translateY(-50%)' }}
                title="下一条"
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
                  {/* 音频没有缩略图——版本列表用「♪ 版本 N + 当时的提示词」文字行。 */}
                  <div
                    className="absolute top-9 right-1.5 z-[201] w-[240px] max-h-60 overflow-auto custom-scrollbar p-1.5 rounded-xl"
                    style={{ backgroundColor: '#2a2a2a', boxShadow: '0px 4px 16px rgba(0,0,0,0.45), inset 0px 0.5px 0px rgba(255,255,255,0.08)' }}
                    onMouseDown={(e) => e.stopPropagation()}
                    onClick={(e) => e.stopPropagation()}
                  >
                    {history.map((item, i) => (
                      <div
                        key={`${item.url}-${i}`}
                        className="relative flex items-center gap-1.5 px-2 py-1.5 rounded-md cursor-pointer group/row hover:bg-white/[0.06]"
                        onClick={() => { showHistoryAt(i); setGalleryOpen(false); }}
                        style={{ outline: i === historyIndex ? '1.5px solid #1fa2dc' : 'none', outlineOffset: '-1.5px' }}
                        title={`版本 ${i + 1}\n${historyPromptAt(history, i, prompt)}`}
                      >
                        <span className="text-[11px] shrink-0">♪ 版本 {i + 1}</span>
                        <span className="text-[10px] text-muted-foreground truncate flex-1">
                          {historyPromptAt(history, i, prompt)}
                        </span>
                        <span
                          role="button"
                          className="w-4 h-4 rounded-full bg-black/70 text-white opacity-0 group-hover/row:opacity-100 flex items-center justify-center cursor-pointer transition-opacity shrink-0"
                          onClick={(e) => { e.stopPropagation(); deleteHistoryAt(i); }}
                          title="删除该版本"
                        >
                          <X size={9} />
                        </span>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </>
          )}
        </div>

      </div>

      {/* ===== 浮动 Prompt 面板——与 GenImageNode 同结构（含反缩放：屏幕上
                恒定 680px 宽，选中动画在内层）。 ===== */}
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
          pointerEvents: promptPanelVisible ? 'auto' : 'none',
        }}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div
          className="bg-card rounded-[20px] border border-border shadow-xl flex flex-col"
          style={{
            transform: `translateY(${promptPanelVisible ? '0' : '-6px'})`,
            opacity: promptPanelVisible ? 1 : 0,
            transition: 'opacity 180ms ease-out, transform 180ms ease-out',
          }}
        >
          <div className="px-3 pt-3 pb-2 flex items-center gap-1.5">
            {/* 已连接的上游音频（音色参考）——「参考已挂上」的视觉确认，连线承载权威状态。 */}
            {connectedAudios.slice(0, 4).map((url, i) => (
              <div
                key={`${url}-${i}`}
                className="h-9 px-2 rounded-[10px] border border-white/10 bg-popover shrink-0 flex items-center gap-1 text-[11px] text-sidebar-foreground"
                title={`音色参考 ${i + 1}`}
              >
                <Music size={12} className="text-orange-400" />
                <span>音频{i + 1}</span>
              </div>
            ))}
            {connectedAudios.length > 4 && (
              <span className="text-[10px] px-1.5 h-9 rounded-[10px] bg-popover text-sidebar-foreground inline-flex items-center shrink-0">
                +{connectedAudios.length - 4}
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
            placeholder="描述你想要生成的音频...（@ 引用音色参考）"
            onChange={(v) => {
              setPromptDraft(v);
              updateNodeSettings(id, { prompt: v });
              scheduleNodePatch(id, { settings: { prompt: v } });
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
                  {audioModels.length === 0 && (
                    <div className="px-3 py-2 text-[11px] text-muted-foreground">未配置音频模型</div>
                  )}
                  {audioModels.map((m) => (
                    <PopoverItem
                      key={m.modelId}
                      active={m.modelId === selectedModelId}
                      multiline
                      onClick={() => {
                        // 换模型时把参数重置为新模型的默认值，与 GenImageNode 同理。
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

            {schemaEntries.map(([key, field]) => {
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

            {/* 禁用原因即显（mouseEnterDelay=0）——原生 title 要悬停 ~1s 且 disabled 上
                行为不一，用户发现不了原因；disabled 按钮不派发鼠标事件，须包 span 接管 hover */}
            <Tooltip
              title={isGenerating ? '生成中…' : !selectedModelId ? '请先选择模型' : !hasPrompt ? '请先输入提示词' : null}
              mouseEnterDelay={0}
              placement="top"
            >
              <span className="inline-flex shrink-0">
                <button
                  onClick={(e) => { e.stopPropagation(); handleGenerate(); }}
                  onMouseDown={(e) => e.stopPropagation()}
                  disabled={!selectedModelId || !hasPrompt || isGenerating}
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
    </>
  );
}

// ─── 构件块（与 GenImageNode 同款；它们是文件内私有组件，按仓库惯例各文件一份） ───

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
