import { useEffect, useMemo, useState } from 'react';
import { App as AntApp } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import JSZip from 'jszip';
import {
  Check,
  CheckCircle2,
  ChevronLeft,
  Circle,
  Copy,
  CopyPlus,
  Download,
  Maximize2,
  Music,
  RefreshCw,
  Search,
  Trash2,
} from 'lucide-react';
import { deleteTask, GenerationTask, listRuntimeModels, listTasks } from '@/shared/api';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { persistNodeById } from '@/modules/canvas/editor/persistence/canvasMutations';
import { ThumbImg } from '@/modules/canvas/editor/ThumbImg';
import { saveBlobToDisk, saveMediaToDisk, saveWithFeedback } from '@/shared/saveToDisk';

// 生产环境可能跑在 HTTP 下（非安全上下文），navigator.clipboard 不可用，
// 所以保留 textarea + execCommand 兜底。
async function copyTextToClipboard(text: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.left = '-9999px';
  document.body.appendChild(ta);
  ta.select();
  document.execCommand('copy');
  document.body.removeChild(ta);
}

export type OutputKind = 'image' | 'video' | 'audio';

// resolveOutputKind 判定一条历史记录的产物类型（图/视频/音频）。
//
// 优先级：task.output.outputKind（后端按下载 mime 算出的真实类型，见
// apps/api/internal/generation/service.go 的 outputKindFromMime）> model
// 目录里的 model.type > URL 扩展名兜底 > 图片。
//
// 为什么不能只信 model.type：隐藏模型（如 sd2.0 去字幕）enabled 但不进
// /models 列表，modelType 拿不到；且这类产物落库成 gen/task-N-i，没有
// .mp4 之类的扩展名，URL 兜底也会猜错。outputKind 是旧任务没有的新字段，
// 缺失时必须原样回退到旧逻辑，不能让老历史记录跟着变。
//
// 前身是二元的 resolveIsVideo——音频任务落进 else 分支被当图片渲染：
// 类型标成「文→图」、<img> 加载音频 URL 变裂图黑块、下载后缀 .png。
export function resolveOutputKind(
  outputKind: OutputKind | undefined,
  modelType: OutputKind | 'text' | undefined,
  url: string,
): OutputKind {
  if (outputKind) return outputKind;
  if (modelType === 'video' || modelType === 'audio') return modelType;
  if (modelType) return 'image';
  if (/\.(mp4|webm|mov)(\?|$)/i.test(url)) return 'video';
  if (/\.(mp3|wav|m4a|aac|ogg|flac)(\?|$)/i.test(url)) return 'audio';
  return 'image';
}

const DOWNLOAD_EXT: Record<OutputKind, string> = { image: 'png', video: 'mp4', audio: 'mp3' };

interface HistoryContextMenu {
  x: number;
  y: number;
  task: GenerationTask;
}

interface Props {
  open: boolean;
  projectId: number;
  onClose: () => void;
}

export function HistoryPanel({ open, projectId, onClose }: Props) {
  const { message } = AntApp.useApp();
  const queryClient = useQueryClient();
  const setLightbox = useCanvasStore((s) => s.setLightbox);
  const addNode = useCanvasStore((s) => s.addNode);
  const updateNode = useCanvasStore((s) => s.updateNode);

  const [search, setSearch] = useState('');
  const [status, setStatus] = useState<string>('succeeded');
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [downloadProgress, setDownloadProgress] = useState<{ current: number; total: number } | null>(null);
  const [contextMenu, setContextMenu] = useState<HistoryContextMenu | null>(null);

  useEffect(() => {
    if (!contextMenu) return;
    const close = () => setContextMenu(null);
    window.addEventListener('click', close);
    window.addEventListener('contextmenu', close);
    return () => {
      window.removeEventListener('click', close);
      window.removeEventListener('contextmenu', close);
    };
  }, [contextMenu]);

  const tasksQuery = useQuery({
    queryKey: ['tasks', { projectId, status, search }],
    queryFn: () => listTasks({ projectId, status: status || undefined, search: search || undefined, limit: 200 }),
    enabled: open && projectId > 0,
    staleTime: 5_000,
  });

  const modelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 60_000,
  });
  const modelTypeById = useMemo(() => {
    const map: Record<string, 'image' | 'video' | 'audio' | 'text'> = {};
    for (const m of modelsQuery.data ?? []) {
      map[m.modelId] = m.type;
    }
    return map;
  }, [modelsQuery.data]);

  const deleteMutation = useMutation({
    mutationFn: (ids: number[]) => Promise.all(ids.map((id) => deleteTask(id))),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] });
      setSelected(new Set());
    },
  });

  const tasks = tasksQuery.data?.data ?? [];
  const total = tasksQuery.data?.total ?? 0;

  const filtered = useMemo(() => {
    if (!search.trim()) return tasks;
    const q = search.toLowerCase();
    return tasks.filter((t) => {
      const promptVal = String((t.input?.inputs as any)?.prompt ?? '').toLowerCase();
      return promptVal.includes(q) || t.modelId.toLowerCase().includes(q);
    });
  }, [tasks, search]);

  const toggleSelect = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleSelectAll = () => {
    if (selected.size > 0) {
      setSelected(new Set());
    } else {
      setSelected(new Set(filtered.map((t) => t.id)));
    }
  };

  const collectUrlsFromTasks = (list: GenerationTask[]) => {
    const files: Array<{ url: string; name: string }> = [];
    list.forEach((task) => {
      const urls = task.output?.outputUrls ?? [];
      urls.forEach((url, idx) => {
        const kind = resolveOutputKind(task.output?.outputKind, modelTypeById[task.modelId], url);
        files.push({ url, name: `task-${task.id}-${idx + 1}.${DOWNLOAD_EXT[kind]}` });
      });
    });
    return files;
  };

  const handleBatchDownload = async () => {
    const targetTasks =
      selected.size > 0
        ? filtered.filter((t) => selected.has(t.id))
        : filtered;
    const files = collectUrlsFromTasks(targetTasks);
    if (files.length === 0) return;

    setDownloadProgress({ current: 0, total: files.length });
    const zip = new JSZip();
    let current = 0;

    for (const f of files) {
      try {
        const resp = await fetch(f.url);
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        zip.file(f.name, await resp.arrayBuffer());
      } catch (err) {
        console.error('download failed', f.name, err);
      }
      current += 1;
      setDownloadProgress({ current, total: files.length });
    }

    // ZIP 是前端内存里现打的包，没有现成的源文件可复制：字节经 IPC raw body 交给 Rust，
    // 由 Rust 弹保存对话框写盘（非 Tauri 环境回退浏览器下载）。
    try {
      await saveWithFeedback(async () => {
        const zipBlob = await zip.generateAsync({ type: 'blob' });
        return saveBlobToDisk(zipBlob, `history-${Date.now()}.zip`);
      }, message);
    } finally {
      setDownloadProgress(null);
    }
  };

  const handleBatchDelete = () => {
    if (selected.size === 0) return;
    if (!confirm(`确定删除选中的 ${selected.size} 条记录？此操作不可恢复。`)) return;
    deleteMutation.mutate(Array.from(selected));
  };

  const handleSendToCanvas = (task: GenerationTask) => {
    const url = task.output?.outputUrls?.[0];
    if (!url) return;
    const kind = resolveOutputKind(task.output?.outputKind, modelTypeById[task.modelId], url);
    const view = useCanvasStore.getState().view;
    const rect = { w: 1200, h: 800 };
    const worldX = (rect.w / 2 - view.x) / view.zoom;
    const worldY = (rect.h / 2 - view.y) / view.zoom;

    // 三类输入节点的内部类型名方向不统一（video-input vs input-audio）是前端
    // 历史遗留，存量快照里全是它们，别顺手统一。
    const nodeType = kind === 'video' ? 'video-input' : kind === 'audio' ? 'input-audio' : 'input-image';
    const nodeId = addNode(nodeType, { x: worldX, y: worldY });
    updateNode(nodeId, { content: url, status: 'succeeded' });
    persistNodeById(nodeId);
  };

  const handlePreview = (task: GenerationTask) => {
    const url = task.output?.outputUrls?.[0];
    if (!url) return;
    const kind = resolveOutputKind(task.output?.outputKind, modelTypeById[task.modelId], url);
    setLightbox({ url, kind, title: task.modelId });
  };

  const percentage = total > 0 ? Math.min(100, Math.round((tasks.length / total) * 100)) : 0;
  const dlPercentage = downloadProgress && downloadProgress.total > 0
    ? Math.round((downloadProgress.current / downloadProgress.total) * 100)
    : 0;

  if (!open) return null;

  return (
    <div className="w-[360px] z-30 flex flex-col border rounded-xl shadow-xl shadow-black/40 transition-colors duration-300 bg-sidebar border-border shrink-0 overflow-hidden">
      {/* 头部——跟 LibraryPanel 一致：ChevronLeft 关闭 + 默认字号的 foreground
          semibold 标题，操作按钮放右侧。 */}
      <div className="flex items-center gap-2 px-4 py-3 border-b border-border">
        <button
          onClick={onClose}
          className="text-sidebar-foreground hover:text-foreground bg-transparent border-none cursor-pointer flex items-center"
          aria-label="关闭生成历史"
        >
          <ChevronLeft size={18} />
        </button>
        <h2 className="text-foreground font-semibold leading-none m-0">生成历史</h2>
        <span className="text-[10px] text-muted-foreground font-mono whitespace-nowrap">
          {downloadProgress
            ? `${downloadProgress.current}/${dlPercentage}%`
            : `${tasks.length}/${percentage}%`}
        </span>
        <div className="flex-1" />
        <div className="flex items-center gap-1">
          <button
            onClick={() => queryClient.invalidateQueries({ queryKey: ['tasks'] })}
            className="p-1.5 rounded transition-colors bg-transparent border-none cursor-pointer text-muted-foreground hover:text-sidebar-foreground hover:bg-popover"
            title="刷新"
          >
            <RefreshCw size={14} className={tasksQuery.isFetching ? 'animate-spin' : ''} />
          </button>
          <button
            onClick={toggleSelectAll}
            className={`p-1.5 rounded transition-colors flex items-center gap-1 border-none cursor-pointer ${
              selected.size > 0
                ? 'text-blue-400 bg-blue-500/20 hover:bg-blue-500/30'
                : 'bg-transparent text-muted-foreground hover:text-sidebar-foreground hover:bg-popover'
            }`}
            title={selected.size > 0 ? `取消全选 (${selected.size})` : '全选'}
          >
            {selected.size > 0 ? <CheckCircle2 size={14} /> : <Circle size={14} />}
            {selected.size > 0 && (
              <span className="text-[9px] font-bold">{selected.size}</span>
            )}
          </button>
          <button
            onClick={handleBatchDownload}
            disabled={!!downloadProgress}
            className={`p-1.5 rounded transition-colors border-none cursor-pointer ${
              selected.size > 0
                ? 'text-blue-400 bg-blue-500/20'
                : 'bg-transparent text-sidebar-foreground hover:text-foreground hover:bg-popover'
            } disabled:opacity-50`}
            title={selected.size > 0 ? `下载选中 (${selected.size})` : '批量下载'}
          >
            <Download size={14} />
          </button>
          {selected.size > 0 && (
            <button
              onClick={handleBatchDelete}
              disabled={deleteMutation.isPending}
              className="p-1.5 rounded transition-colors border-none cursor-pointer text-red-300 bg-red-900/30 hover:bg-red-900/50"
              title="删除选中"
            >
              <Trash2 size={14} />
            </button>
          )}
        </div>
      </div>

      {/* 下载进度条 */}
      {downloadProgress && (
        <div className="w-full px-3 pb-2 pt-2 border-b border-border">
          <div className="w-full h-1.5 rounded-full overflow-hidden bg-popover">
            <div
              className="h-full bg-blue-500 transition-all duration-200"
              style={{ width: `${dlPercentage}%` }}
            />
          </div>
        </div>
      )}

      {/* 搜索 + 状态筛选 */}
      <div className="px-3 py-2 border-b border-border shrink-0 space-y-2">
        <div className="flex items-center gap-2 px-4 py-2.5 rounded-full bg-popover focus-within:bg-accent transition-colors w-full">
          <Search size={16} className="text-muted-foreground shrink-0" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="搜索 prompt 或模型..."
            className="flex-1 min-w-0 bg-transparent text-foreground placeholder:text-muted-foreground text-sm outline-none border-none p-0"
          />
        </div>
        <div className="flex gap-1 flex-wrap">
          {[
            { v: '', l: '全部' },
            { v: 'succeeded', l: '成功' },
            { v: 'failed', l: '失败' },
            { v: 'processing', l: '处理中' },
          ].map((opt) => (
            <button
              key={opt.v}
              onClick={() => setStatus(opt.v)}
              className={`px-2 py-0.5 rounded text-[10px] transition-colors border-none cursor-pointer ${
                status === opt.v
                  ? 'bg-blue-600 text-white'
                  : 'bg-popover text-sidebar-foreground hover:bg-accent hover:text-foreground'
              }`}
            >
              {opt.l}
            </button>
          ))}
        </div>
      </div>

      {/* 列表 */}
      <div className="flex-1 overflow-y-auto custom-scrollbar p-2 space-y-2">
        {tasksQuery.isLoading ? (
          <div className="text-center text-xs text-muted-foreground py-10">加载中...</div>
        ) : filtered.length === 0 ? (
          <div className="text-center text-xs text-muted-foreground py-10">暂无记录</div>
        ) : (
          filtered.map((task) => (
            <HistoryItem
              key={task.id}
              task={task}
              modelType={modelTypeById[task.modelId]}
              isSelected={selected.has(task.id)}
              onSelect={() => toggleSelect(task.id)}
              onSendToCanvas={() => handleSendToCanvas(task)}
              onPreview={() => handlePreview(task)}
              onDelete={() => {
                if (confirm('删除这条记录？')) deleteMutation.mutate([task.id]);
              }}
              onOpenContextMenu={(x, y) => setContextMenu({ x, y, task })}
            />
          ))
        )}
      </div>

      {contextMenu && (
        <div
          className="fixed z-[100] w-44 rounded-lg py-1 border border-solid border-white/25 bg-muted shadow-[0_12px_32px_rgba(0,0,0,0.55)]"
          style={{ left: contextMenu.x, top: contextMenu.y }}
          onClick={(e) => e.stopPropagation()}
          onContextMenu={(e) => {
            e.preventDefault();
            e.stopPropagation();
          }}
        >
          <div className="px-3 py-1.5 text-[10px] font-medium border-b border-border text-muted-foreground mb-1">
            操作
          </div>
          <ContextMenuItem
            icon={<CopyPlus size={14} className="text-blue-400" />}
            label="发送到画布"
            onClick={() => {
              handleSendToCanvas(contextMenu.task);
              setContextMenu(null);
            }}
          />
          <ContextMenuItem
            icon={<Maximize2 size={14} className="text-emerald-400" />}
            label="预览"
            onClick={() => {
              handlePreview(contextMenu.task);
              setContextMenu(null);
            }}
          />
          <ContextMenuItem
            icon={<Download size={14} className="text-sidebar-foreground" />}
            label="下载"
            onClick={async () => {
              const task = contextMenu.task;
              const url = task.output?.outputUrls?.[0];
              setContextMenu(null);
              if (!url) {
                message.warning('这条记录没有产物文件');
                return;
              }
              const kind = resolveOutputKind(task.output?.outputKind, modelTypeById[task.modelId], url);
              const name = `task-${task.id}.${DOWNLOAD_EXT[kind]}`;
              // 统一走 saveMediaToDisk：本地媒体由 Rust 弹保存对话框后直接复制，
              // 不再经前端 fetch → blob；失败必须报出来，不能「点了没反应」。
              await saveWithFeedback(() => saveMediaToDisk(url, name), message);
            }}
          />
          <div className="h-px bg-popover my-1" />
          <ContextMenuItem
            icon={<Trash2 size={14} className="text-red-400" />}
            label="删除"
            danger
            onClick={() => {
              const task = contextMenu.task;
              setContextMenu(null);
              if (confirm('删除这条记录？')) deleteMutation.mutate([task.id]);
            }}
          />
        </div>
      )}
    </div>
  );
}

function ContextMenuItem({
  icon,
  label,
  onClick,
  danger,
}: {
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
  danger?: boolean; // 破坏性动作（删除）文字标红警示
}) {
  return (
    <button
      onClick={onClick}
      className={`w-full whitespace-nowrap text-left px-3 py-2 text-xs flex items-center gap-2 transition-colors bg-transparent border-none cursor-pointer hover:bg-accent ${
        danger ? 'text-red-400' : 'text-sidebar-foreground'
      }`}
    >
      {icon}
      <span>{label}</span>
    </button>
  );
}

import { Loader2 } from 'lucide-react';
import { mediaCrossOrigin } from '../editor/utils/mediaCrossOrigin';

function HistoryItem({
  task,
  modelType,
  isSelected,
  onSelect,
  onSendToCanvas,
  onPreview,
  onDelete,
  onOpenContextMenu,
}: {
  task: GenerationTask;
  modelType?: 'image' | 'video' | 'audio' | 'text';
  isSelected: boolean;
  onSelect: () => void;
  onSendToCanvas: () => void;
  onPreview: () => void;
  onDelete: () => void;
  onOpenContextMenu: (x: number, y: number) => void;
}) {
  const url = task.output?.outputUrls?.[0] ?? '';
  const kind = resolveOutputKind(task.output?.outputKind, modelType, url);
  const isVideo = kind === 'video';
  const inputs = (task.input?.inputs as any) || {};
  const prompt = String(inputs.prompt ?? inputs.videoPrompt ?? '');
  const ratio = inputs.ratio;
  const resolution = inputs.resolution;
  const duration = inputs.duration;
  const n = inputs.n;
  const hasRefImage = Array.isArray(inputs.images) ? inputs.images.length > 0 : !!inputs.image;
  const genType =
    kind === 'audio'
      ? '文→音频'
      : kind === 'video'
        ? hasRefImage ? '图→视频' : '文→视频'
        : hasRefImage ? '图→图' : '文→图';
  const createdAt = new Date(task.createdAt);
  const timeLabel = createdAt.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
  const updatedAt = new Date(task.updatedAt);
  const durationMs = updatedAt.getTime() - createdAt.getTime();

  const status = task.status;
  const progress = status === 'succeeded' ? 100 : status === 'processing' ? 50 : status === 'queued' ? 5 : status === 'failed' ? 100 : 0;

  const [copied, setCopied] = useState(false);
  const handleCopyPrompt = async () => {
    if (!prompt) return;
    try {
      await copyTextToClipboard(prompt);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 无操作：复制失败时按钮只是不切到「已复制」状态。
    }
  };

  const handleDragStart = (e: React.DragEvent) => {
    if (!url) return;
    e.dataTransfer.setData('text/uri-list', url);
    // isVideo 字段并存是给旧消费端的兼容垫；kind 是三态真源（CanvasEditor 落
    // 画布按它选 input-image / video-input / input-audio）。
    e.dataTransfer.setData('application/x-tvmao-history', JSON.stringify({ url, isVideo, kind }));
    e.dataTransfer.effectAllowed = 'copy';
  };

  // 固定的紧凑缩略图高度——让历史列表保持易扫视，而不会因为 288px 宽面板上的
  // 9:16 任务膨胀到约 512px 高。原始宽高比会在 lightbox 预览里以全尺寸保留。
  const mediaHeight = 'h-28';
  void ratio;

  return (
    <div className={`rounded-lg overflow-hidden relative group ${isSelected ? 'ring-2 ring-green-500/70' : ''}`}>
      {/* 选择圆圈——右下角 */}
      <button
        onClick={(e) => {
          e.stopPropagation();
          e.preventDefault();
          onSelect();
        }}
        className={`absolute bottom-2 right-2 z-20 w-6 h-6 rounded-full border-2 flex items-center justify-center transition-all cursor-pointer ${
          isSelected
            ? 'bg-green-500 border-green-400 opacity-100'
            : 'border-white/60 bg-black/50 opacity-100 hover:bg-black/70'
        }`}
        title={isSelected ? '取消选择' : '选择'}
      >
        {isSelected && <CheckCircle2 size={16} className="text-white" />}
      </button>

      {/* 媒体区 */}
      <div className={`bg-black relative ${mediaHeight}`}>
        {status === 'succeeded' && url ? (
          kind === 'audio' ? (
            // 音频没有画面，给图标 + 原生播放器；拖拽/右键挂在容器上（audio 的
            // 原生控件要留给播放交互）。
            <div
              className="w-full h-full flex flex-col items-center justify-center gap-2 px-3"
              draggable
              onDragStart={handleDragStart}
              onContextMenu={(e) => {
                e.preventDefault();
                e.stopPropagation();
                onOpenContextMenu(e.clientX, e.clientY);
              }}
            >
              <Music size={20} className="text-muted-foreground" />
              <audio src={url} controls preload="metadata" className="w-full" style={{ maxHeight: 32 }} />
            </div>
          ) : isVideo ? (
            <video
              src={url} crossOrigin={mediaCrossOrigin(url)}
              className="w-full h-full object-contain cursor-zoom-in"
              muted
              loop
              playsInline
              preload="metadata"
              draggable
              onDragStart={handleDragStart}
              onDoubleClick={onPreview}
              onContextMenu={(e) => {
                e.preventDefault();
                e.stopPropagation();
                onOpenContextMenu(e.clientX, e.clientY);
              }}
            />
          ) : (
            // 列表展示走缩略图；双击预览/拖到画布/下载引用的都是闭包里的原图 url。
            <ThumbImg
              src={url}
              alt={prompt || '生成的图片'}
              className="w-full h-full object-contain cursor-zoom-in"
              draggable
              onDragStart={handleDragStart}
              onDoubleClick={onPreview}
              onContextMenu={(e) => {
                e.preventDefault();
                e.stopPropagation();
                onOpenContextMenu(e.clientX, e.clientY);
              }}
            />
          )
        ) : (
          <div className="w-full h-full flex items-center justify-center">
            <Loader2 size={24} className={`text-muted-foreground ${status === 'queued' || status === 'processing' ? 'animate-spin' : ''}`} />
          </div>
        )}

        {/* hover 预览按钮——行为跟双击一致。发送到画布通过右键菜单触达。 */}
        {url && (
          <button
            onClick={(e) => {
              e.stopPropagation();
              onPreview();
            }}
            className="absolute top-2 left-2 p-1 rounded bg-black/60 hover:bg-black/80 text-white border border-white/10 cursor-pointer opacity-0 group-hover:opacity-100 transition-opacity z-10"
            title="放大预览"
          >
            <Maximize2 size={12} />
          </button>
        )}

        {/* 进度条——底部 1px */}
        <div className="absolute bottom-0 left-0 right-0 h-1 bg-popover">
          <div className="h-full bg-blue-500 transition-all" style={{ width: `${progress}%` }} />
        </div>
      </div>

      {/* 信息 */}
      <div className="px-2 py-1.5 text-[11px]">
        {/* 第 1 行：prompt + 操作按钮 */}
        <div className="flex justify-between items-start gap-2">
          <span className="line-clamp-2 text-sidebar-foreground">
            {prompt || 'Untitled'}
          </span>
          <div className="shrink-0 ml-1 flex items-center gap-1">
            <button
              onClick={(e) => { e.stopPropagation(); void handleCopyPrompt(); }}
              disabled={!prompt}
              className={`shrink-0 p-1 bg-transparent border-none rounded ${
                prompt
                  ? `cursor-pointer ${copied ? 'text-green-500' : 'text-muted-foreground hover:text-sidebar-foreground'}`
                  : 'cursor-not-allowed text-muted-foreground/40'
              }`}
              title={copied ? '已复制' : '复制提示词'}
            >
              {copied ? <Check size={12} /> : <Copy size={12} />}
            </button>
            <button
              onClick={(e) => { e.stopPropagation(); onDelete(); }}
              className="shrink-0 p-1 bg-transparent border-none cursor-pointer text-muted-foreground hover:text-red-500 rounded"
              title="删除"
            >
              <Trash2 size={12} />
            </button>
          </div>
        </div>

        {/* 状态消息（failed/queued） */}
        {status === 'failed' && task.error && (
          <p className="text-[9px] text-red-500 mt-1 break-words whitespace-pre-wrap line-clamp-3">
            {task.error}
          </p>
        )}
        {status === 'processing' && (
          <p className="text-[9px] text-blue-500 mt-1">生成中...</p>
        )}
        {status === 'queued' && (
          <p className="text-[9px] text-muted-foreground mt-1">排队中...</p>
        )}

        <div className="h-1.5" />

        {/* 第 3、4 行：genType · ratio · resolution ；time · model · duration */}
        <div className="flex flex-col w-full text-muted-foreground">
          <span>
            {[
              genType,
              ratio,
              resolution,
              isVideo && duration ? `${duration}s` : null,
              n && n > 1 ? `×${n}` : null,
            ].filter(Boolean).join(' · ')}
          </span>
          <span>
            {timeLabel} · <span title={task.modelId} className="font-mono">{task.modelId}</span>
            {durationMs > 0 && status === 'succeeded' && (
              <> · 用时 {(durationMs / 1000).toFixed(1)}s</>
            )}
          </span>
        </div>
      </div>
    </div>
  );
}
