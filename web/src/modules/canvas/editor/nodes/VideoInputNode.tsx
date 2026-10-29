import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  CheckCircle2,
  CheckSquare,
  ChevronDown,
  ChevronUp,
  CopyPlus,
  Square,
  Trash2,
  Upload,
  UploadCloud,
  Video,
  X,
} from 'lucide-react';
import { useCanvasStore } from '../store';
import { persistNodeById } from '../persistence/canvasMutations';
import { uploadFileIntoNode } from '../utils/uploadIntoNode';
import { ThumbImg } from '../ThumbImg';
import { useStoryboardMode } from '../../storyboard/StoryboardContext';
import { NodeShell } from '../NodeShell';
import { VideoActionsBar } from './VideoActionsBar';
import { uploadAsset, currentProjectId } from '@/shared/api';
import { saveFrames, loadFrames } from '@/shared/frameCache';
import { useMediaNodeMenu } from './useMediaNodeMenu';
import { VideoPlayer } from './VideoPlayer';
import { useNodeMediaActive } from './useNodeMediaActive';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';

interface Frame {
  url: string;
  time: number;
  filename?: string;
}

export function VideoInputNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodesById.get(id));
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const setLightbox = useCanvasStore((s) => s.setLightbox);
  // 详情面板里视频已经是原片、还带播放器控制条，再双击开 Lightbox 是多余的
  // （开出来还是同一个源）。画布上保留——那里的节点小，看不清。
  const storyboardMode = useStoryboardMode();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const [frameMenu, setFrameMenu] = useState<{ x: number; y: number; frame: Frame; idx: number } | null>(null);
  const { onContextMenu, overlay: menuOverlay, badge: seedanceBadge, mediaProps } = useMediaNodeMenu({
    url: node?.content,
    kind: 'video',
    nodeId: id,
  });
  const mediaActive = useNodeMediaActive(id);

  useEffect(() => {
    if (!frameMenu) return;
    const close = () => setFrameMenu(null);
    window.addEventListener('click', close);
    window.addEventListener('contextmenu', close);
    return () => { window.removeEventListener('click', close); window.removeEventListener('contextmenu', close); };
  }, [frameMenu]);

  // 挂载时从 IndexedDB 恢复帧
  useEffect(() => {
    if (!node) return;
    if ((node.settings?.frames as Frame[] | undefined)?.length) return;
    loadFrames(id).then((cached) => {
      if (cached && cached.frames.length > 0) {
        updateNodeSettings(id, { frames: cached.frames, selectedKeyframes: cached.selectedKeyframes });
      }
    }).catch(() => {});
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!node) return null;

  const videoExpanded = (node.settings?.videoExpanded as boolean | undefined) ?? true;
  const videoMeta = node.settings?.videoMeta as { duration?: number; w?: number; h?: number } | undefined;
  const frames = (node.settings?.frames as Frame[] | undefined) ?? [];
  const selectedKeyframes = (node.settings?.selectedKeyframes as Frame[] | undefined) ?? [];
  const extractingFrames = node.settings?.extractingFrames as boolean | undefined;

  const handleFile = async (file: File | undefined) => {
    if (!file) return;
    const asset = await uploadFileIntoNode(id, file);
    if (!asset) return;
    // 换了视频，旧视频的帧就不再属于这个节点：内存和 IndexedDB 缓存一起清掉，
    // 否则新视频下面挂着旧帧，重开画布还会从缓存里恢复回来（P8）。
    // videoMeta 一并清掉：新片探测完成前，旧片的时长/尺寸不能拿来做合规预检。
    updateNodeSettings(id, { frames: [], selectedKeyframes: [], videoMeta: undefined });
    saveFrames(id, [], []).catch(() => {});
    // 尝试获取视频元数据。它比上传晚落定，所以自己再持久化一次——
    // 时长要拿来做 seedance 合规校验，丢了就得重新探测。
    const tempVideo = document.createElement('video');
    // 探测完（成功或失败）都主动释放：脱离 DOM 的 <video> 要等 GC 才放掉解码器（P35）。
    const release = () => {
      tempVideo.onloadedmetadata = null;
      tempVideo.onerror = null;
      tempVideo.removeAttribute('src');
      tempVideo.load();
    };
    tempVideo.preload = 'metadata';
    tempVideo.onloadedmetadata = () => {
      // 探测期间又换了片：这份时长属于上一个视频，写进去就会拿旧片时长给新片做合规预检
      //（useMediaNodeMenu 优先信 videoMeta）。
      if (useCanvasStore.getState().nodesById.get(id)?.content !== asset.url) {
        release();
        return;
      }
      updateNodeSettings(id, {
        videoMeta: { duration: tempVideo.duration, w: tempVideo.videoWidth, h: tempVideo.videoHeight },
      });
      release();
      persistNodeById(id);
    };
    tempVideo.onerror = release;
    tempVideo.src = asset.url;
  };

  const handleAutoExtract = async (fps: number) => {
    const videoUrl = node.content;
    if (!videoUrl) return;
    updateNodeSettings(id, { extractingFrames: true });

    const video = document.createElement('video');
    try {
      video.crossOrigin = 'anonymous';
      video.preload = 'auto';
      video.muted = true;
      video.src = videoUrl;

      await new Promise<void>((resolve, reject) => {
        video.onloadeddata = () => resolve();
        video.onerror = () => reject(new Error('视频加载失败'));
      });

      const duration = video.duration;
      const interval = 1 / fps;
      const canvas = document.createElement('canvas');
      canvas.width = video.videoWidth;
      canvas.height = video.videoHeight;
      const ctx = canvas.getContext('2d')!;
      const newFrames: Frame[] = [];

      for (let t = 0; t < duration; t += interval) {
        // 目标时间等于当前位置时 `currentTime = t` 不触发 seeked（同 AdvancedEditDialog
        // 的 captureFrame）——首帧 t=0 必踩，死等会让「抽帧中」永远转圈。loadeddata
        // 之后当前帧已可画，直接取。
        if (Math.abs(video.currentTime - t) > 0.001) {
          await new Promise<void>((resolve, reject) => {
            video.onseeked = () => resolve();
            video.onerror = () => reject(new Error('视频定位失败'));
            video.currentTime = t;
          });
        }
        ctx.drawImage(video, 0, 0);
        const dataUrl = canvas.toDataURL('image/jpeg', 0.8);
        newFrames.push({ url: dataUrl, time: t });
      }

      updateNodeSettings(id, { frames: newFrames, selectedKeyframes: [], extractingFrames: false });
      saveFrames(id, newFrames, []).catch(() => {});
    } catch (err) {
      console.error('抽帧失败', err);
      updateNodeSettings(id, { extractingFrames: false });
    } finally {
      // 脱离 DOM 的 <video> 在 GC 前一直占着解码器和缓冲（WebKit 尤甚），主动释放。
      video.onseeked = null;
      video.onerror = null;
      video.removeAttribute('src');
      video.load();
    }
  };

  const persistCache = (f: Frame[], s: Frame[]) => { saveFrames(id, f, s).catch(() => {}); };

  const toggleKeyframe = (frame: Frame) => {
    const exists = selectedKeyframes.some((f) => f.url === frame.url && f.time === frame.time);
    const next = exists
      ? selectedKeyframes.filter((f) => !(f.url === frame.url && f.time === frame.time))
      : [...selectedKeyframes, { url: frame.url, time: frame.time, filename: frame.filename }];
    updateNodeSettings(id, { selectedKeyframes: next });
    persistCache(frames, next);
  };

  const toggleSelectAll = () => {
    if (selectedKeyframes.length > 0) {
      updateNodeSettings(id, { selectedKeyframes: [] });
      persistCache(frames, []);
    } else {
      const all = frames.map((f) => ({ url: f.url, time: f.time, filename: f.filename }));
      updateNodeSettings(id, { selectedKeyframes: all });
      persistCache(frames, all);
    }
  };

  const clearFrames = () => {
    if (confirm('确定要清空所有抽帧缩略图吗？')) {
      updateNodeSettings(id, { frames: [], selectedKeyframes: [] });
      import('@/shared/frameCache').then((m) => m.deleteFrames(id)).catch(() => {});
    }
  };

  const deleteFrame = (idx: number) => {
    const frame = frames[idx];
    const newFrames = frames.filter((_, i) => i !== idx);
    const newSelected = selectedKeyframes.filter((f) => f.url !== frame.url);
    updateNodeSettings(id, { frames: newFrames, selectedKeyframes: newSelected });
    persistCache(newFrames, newSelected);
  };

  const swapFrames = (a: number, b: number) => {
    const newFrames = [...frames];
    [newFrames[a], newFrames[b]] = [newFrames[b], newFrames[a]];
    updateNodeSettings(id, { frames: newFrames });
    persistCache(newFrames, selectedKeyframes);
  };

  const toggleExpand = () => {
    updateNodeSettings(id, { videoExpanded: !videoExpanded });
  };

  const sendFrameToCanvas = async (frame: Frame) => {
    const view = useCanvasStore.getState().view;
    const worldX = (600 - view.x) / view.zoom;
    const worldY = (400 - view.y) / view.zoom;
    const nodeId = addNode('input-image', { x: worldX, y: worldY });

    let url = frame.url;
    // data URL → 先上传到 MinIO
    if (url.startsWith('data:')) {
      updateNode(nodeId, { status: 'uploading' });
      try {
        const resp = await fetch(url);
        const blob = await resp.blob();
        const file = new File([blob], `frame-${frame.time.toFixed(2)}s.jpg`, { type: blob.type || 'image/jpeg' });
        const asset = await uploadAsset(file, currentProjectId());
        url = asset.url;
      } catch (err: any) {
        updateNode(nodeId, { status: 'failed', error: '帧上传失败' });
        persistNodeById(nodeId);
        return;
      }
    }
    updateNode(nodeId, { content: url, status: 'succeeded' });
    persistNodeById(nodeId);
  };

  const addNode = useCanvasStore((s) => s.addNode);

  const sendSelectedToCanvas = async () => {
    if (selectedKeyframes.length === 0) return;
    const view = useCanvasStore.getState().view;
    const baseX = (600 - view.x) / view.zoom;
    const baseY = (300 - view.y) / view.zoom;

    for (let i = 0; i < selectedKeyframes.length; i++) {
      const frame = selectedKeyframes[i];
      let url = frame.url;
      if (url.startsWith('data:')) {
        try {
          const resp = await fetch(url);
          const blob = await resp.blob();
          const file = new File([blob], `frame-${frame.time.toFixed(2)}s.jpg`, { type: blob.type || 'image/jpeg' });
          const asset = await uploadAsset(file, currentProjectId());
          url = asset.url;
        } catch {
          continue;
        }
      }
      const nodeId = addNode('input-image', { x: baseX + i * 40, y: baseY + i * 40 });
      updateNode(nodeId, { content: url, status: 'succeeded' });
      persistNodeById(nodeId);
    }
  };

  return (
    <NodeShell node={node} inputs={0} minWidth={360} minHeight={300} titleExtra={seedanceBadge} bodyOverflow="visible">
      {/* 故事板详情面板里节点是铺满容器的静态预览，没有选中/工具条语义。 */}
      <VideoActionsBar nodeId={id} visible={!!node.content && !storyboardMode} url={node.content} />
      <input ref={fileInputRef} type="file" accept="video/*" className="hidden" onChange={(e) => handleFile(e.target.files?.[0])} />

      {/* 头部——节点名称 + 图标现在放在卡片上方那个通用 chip 里（NodeShell）。
          这一条只承载功能性内容：左边关键帧开关，右边视频元数据 + 已选数量。 */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-input/50 text-xs font-semibold shrink-0">
        <div className="flex items-center gap-1.5">
          <button
            onClick={(e) => { e.stopPropagation(); toggleExpand(); }}
            className={`p-1 hover:bg-accent rounded transition-colors bg-transparent border-none cursor-pointer text-sidebar-foreground flex items-center gap-1 ${!videoExpanded ? 'bg-accent' : ''}`}
            title={videoExpanded ? '收起视频' : '展开视频'}
          >
            {videoExpanded ? <ChevronUp size={12} /> : <ChevronDown size={12} />}
            <span className="text-[11px] text-sidebar-foreground font-medium">关键帧</span>
          </button>
        </div>
        <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
          {videoMeta?.duration ? <span>时长 {videoMeta.duration.toFixed(1)}s</span> : null}
          {videoMeta?.w ? <span>{videoMeta.w}x{videoMeta.h}</span> : null}
          {selectedKeyframes.length > 0 && <span className="text-blue-500">关键帧 {selectedKeyframes.length} 个</span>}
        </div>
      </div>

      {/* 主体 */}
      <div className="flex-1 flex flex-col gap-3 p-3 overflow-hidden min-h-0" onContextMenu={onContextMenu}>
        {node.content ? (
          <div className="space-y-2 shrink-0">
            {videoExpanded && (() => {
              const aspect = videoMeta?.w && videoMeta?.h ? videoMeta.w / videoMeta.h : 16 / 9;
              // 按长边限制，避免竖屏视频把预览在纵向撑爆——横屏用 360px 宽，
              // 竖屏用 240px 高。
              const maxW = aspect >= 1 ? 360 : Math.round(240 * aspect);
              return (
                <div
                  className={`relative w-full bg-black rounded-lg overflow-hidden flex items-center justify-center mx-auto ${
                    storyboardMode ? '' : 'cursor-zoom-in'
                  }`}
                  style={{ aspectRatio: `${videoMeta?.w ?? 16} / ${videoMeta?.h ?? 9}`, maxWidth: maxW }}
                  onDoubleClick={storyboardMode ? undefined : (e) => { e.stopPropagation(); setLightbox({ url: node.content!, kind: 'video', title: '视频输入' }); }}
                >
                  {/* 控制条区域拦 mousedown 交给播放器；画面区域放行冒泡给 NodeShell
                      拖动——此前完全不拦，按进度条拖动时节点会跟着跑。 */}
                  <VideoPlayer ref={videoRef} active={mediaActive} {...mediaProps} src={node.content} crossOrigin={mediaCrossOrigin(node.content)} className="w-full h-full object-contain" draggable={false} />
                </div>
              );
            })()}
            <div className="flex items-center gap-2 flex-wrap">
              <label className="bg-blue-600 hover:bg-blue-500 text-white px-3 py-1.5 rounded text-xs font-medium transition-colors cursor-pointer" onMouseDown={(e) => e.stopPropagation()}>
                更换视频
                <input type="file" className="hidden" accept="video/*" onChange={(e) => handleFile(e.target.files?.[0])} />
              </label>
              <button
                className="px-3 py-1.5 rounded text-xs border border-input bg-popover text-sidebar-foreground hover:border-blue-500 hover:text-blue-400 transition-colors bg-transparent cursor-pointer"
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => { e.stopPropagation(); handleAutoExtract(2); }}
                disabled={!!extractingFrames}
              >
                自动抽帧（2fps）
              </button>
              <button
                className="px-3 py-1.5 rounded text-xs border bg-green-600/40 border-green-500 text-green-200 hover:bg-green-600/60 hover:border-green-400 transition-colors cursor-pointer"
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => { e.stopPropagation(); handleAutoExtract(1); }}
                disabled={!!extractingFrames}
              >
                智能抽帧
              </button>
              {extractingFrames && <span className="text-[11px] text-blue-500">抽帧中...</span>}
            </div>
          </div>
        ) : videoExpanded ? (
          <div className="flex flex-col items-center justify-center gap-3 w-full shrink-0">
            <div
              className="relative w-full h-48 flex flex-col items-center justify-center border-2 border-dashed border-input hover:border-input rounded-lg transition-colors cursor-pointer"
              onClick={(e) => { e.stopPropagation(); fileInputRef.current?.click(); }}
              onMouseDown={(e) => e.stopPropagation()}
            >
              {node.status === 'uploading' ? (
                <div className="flex flex-col items-center gap-2 text-sidebar-foreground">
                  <Upload size={32} className="canvas-pulse" />
                  <span className="text-xs">上传中…</span>
                </div>
              ) : (
                <>
                  <UploadCloud size={48} className="text-muted-foreground mb-2" />
                  <span className="text-xs text-sidebar-foreground">点击选择或拖入视频</span>
                </>
              )}
            </div>
            <div className="text-[10px] text-center text-muted-foreground pointer-events-none">支持 MP4/WEBM</div>
          </div>
        ) : null}

        {/* 缩略图头部 */}
        <div className="flex items-center justify-between text-[11px] text-muted-foreground shrink-0">
          <span>抽帧缩略图</span>
          <div className="flex items-center gap-2 flex-nowrap shrink-0 whitespace-nowrap">
            <span>{frames.length} 张</span>
            {frames.length > 0 && (
              <>
                <button
                  onClick={toggleSelectAll}
                  className="text-[10px] px-1.5 py-0.5 rounded transition-colors flex items-center gap-1 bg-blue-600/30 text-blue-300 hover:bg-blue-600/50 border-none cursor-pointer"
                  onMouseDown={(e) => e.stopPropagation()}
                  title={selectedKeyframes.length > 0 ? '取消全选' : '全选'}
                >
                  {selectedKeyframes.length > 0 ? <Square size={10} /> : <CheckSquare size={10} />}
                  {selectedKeyframes.length > 0 ? '取消' : '全选'}
                </button>
                {selectedKeyframes.length > 0 && (
                  <button
                    onClick={sendSelectedToCanvas}
                    className="text-[10px] px-1.5 py-0.5 rounded transition-colors flex items-center gap-1 bg-blue-600/40 text-blue-200 hover:bg-blue-600/60 border-none cursor-pointer"
                    onMouseDown={(e) => e.stopPropagation()}
                    title={`发送 ${selectedKeyframes.length} 张到画布`}
                  >
                    <CopyPlus size={10} /> 发送到画布 ({selectedKeyframes.length})
                  </button>
                )}
                <button
                  onClick={clearFrames}
                  className="text-[10px] px-1.5 py-0.5 rounded transition-colors flex items-center gap-1 bg-red-600/30 text-red-300 hover:bg-red-600/50 border-none cursor-pointer"
                  onMouseDown={(e) => e.stopPropagation()}
                  title="清空所有缩略图"
                >
                  <Trash2 size={10} /> 清空
                </button>
              </>
            )}
          </div>
        </div>

        {/* 缩略图网格 */}
        <div
          className="grid gap-3 w-full overflow-y-auto custom-scrollbar flex-1 min-h-0 relative p-1 content-start items-start"
          style={{ gridTemplateColumns: videoExpanded ? 'repeat(auto-fill, minmax(200px, 1fr))' : '1fr', gridAutoRows: 'auto' }}
        >
          {frames.length === 0 ? (
            <div className="col-span-full text-[11px] text-muted-foreground text-center py-4 border rounded bg-popover/40 border-input">
              点击「自动抽帧」即可生成缩略图
              <div className="mt-1 opacity-50 text-[9px]">可拖拽图片插入</div>
            </div>
          ) : (
            frames.map((frame, idx) => {
              const selected = selectedKeyframes.some((f) => f.url === frame.url && f.time === frame.time);
              return (
                <div key={`${frame.time}-${idx}`} className="relative">
                  <button
                    className="relative w-full rounded overflow-hidden group transition-all cursor-pointer p-0"
                    style={{
                      backgroundColor: selected ? 'rgba(30, 58, 138, 0.3)' : 'transparent',
                      border: selected ? '2px solid #3b82f6' : '1px solid #3f3f46',
                      boxShadow: selected ? '0 0 0 2px rgba(59, 130, 246, 0.5)' : 'none',
                    }}
                    onMouseDown={(e) => e.stopPropagation()}
                    onClick={(e) => { e.stopPropagation(); toggleKeyframe(frame); }}
                    onContextMenu={(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      setFrameMenu({ x: e.clientX, y: e.clientY, frame, idx });
                    }}
                  >
                    <ThumbImg
                      src={frame.url}
                      className={`w-full bg-black ${videoExpanded ? 'aspect-video object-contain max-h-[140px]' : 'h-[90px] object-cover'}`}
                      alt={`frame-${idx}`}
                      draggable={false}
                    />
                    <div className="absolute left-1 top-1 text-[10px] px-1 py-0.5 rounded bg-black/60 text-white">
                      {typeof frame.time === 'number' ? frame.time.toFixed(2) : frame.time}s
                    </div>
                    {frame.filename && (
                      <div className="absolute left-0 bottom-0 right-0 bg-black/80 text-white text-[9px] px-1 py-0.5 opacity-0 group-hover:opacity-100 transition-opacity truncate" title={frame.filename}>
                        {frame.filename}
                      </div>
                    )}
                    {/* 右侧控制条 */}
                    <div className="absolute top-1 bottom-1 right-1 flex flex-col justify-between items-center z-20 pointer-events-none">
                      <div className="pointer-events-auto w-5 h-5 rounded-full border border-white/80 bg-black/40 flex items-center justify-center shrink-0 mb-auto">
                        {selected ? <CheckCircle2 size={12} className="text-white" /> : null}
                      </div>
                      <button
                        onClick={(e) => { e.stopPropagation(); deleteFrame(idx); }}
                        className="pointer-events-auto w-5 h-5 rounded bg-red-600/80 text-white flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity hover:bg-red-500 my-1 shrink-0 border-none cursor-pointer"
                        title="删除此帧"
                      >
                        <X size={12} />
                      </button>
                      <div className="pointer-events-auto flex flex-col gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity shrink-0 mt-auto">
                        {idx > 0 && (
                          <button
                            onClick={(e) => { e.stopPropagation(); swapFrames(idx - 1, idx); }}
                            className="w-5 h-5 rounded bg-black/70 text-white flex items-center justify-center hover:bg-blue-600 border-none cursor-pointer"
                            title="上移"
                          >
                            <ChevronUp size={12} />
                          </button>
                        )}
                        {idx < frames.length - 1 && (
                          <button
                            onClick={(e) => { e.stopPropagation(); swapFrames(idx, idx + 1); }}
                            className="w-5 h-5 rounded bg-black/70 text-white flex items-center justify-center hover:bg-blue-600 border-none cursor-pointer"
                            title="下移"
                          >
                            <ChevronDown size={12} />
                          </button>
                        )}
                      </div>
                    </div>
                  </button>
                </div>
              );
            })
          )}
        </div>
      </div>

      {frameMenu && createPortal(
        <div
          className="fixed z-[110] w-48 rounded-lg py-1 border border-solid border-white/25 bg-muted shadow-[0_12px_32px_rgba(0,0,0,0.55)]"
          style={{ left: frameMenu.x, top: frameMenu.y }}
          onClick={(e) => e.stopPropagation()}
          onContextMenu={(e) => { e.preventDefault(); e.stopPropagation(); }}
        >
          <div className="px-3 py-1.5 text-[10px] font-medium border-b border-border text-muted-foreground mb-1">操作</div>
          <button
            className="w-full text-left px-3 py-2 text-xs flex items-center gap-2 transition-colors bg-transparent border-none cursor-pointer text-sidebar-foreground hover:bg-accent"
            onClick={() => { sendFrameToCanvas(frameMenu.frame); setFrameMenu(null); }}
          >
            <CopyPlus size={14} className="text-blue-500" /> 发送到画布
          </button>
          <div className="h-px bg-popover my-1" />
          <button
            className="w-full text-left px-3 py-2 text-xs flex items-center gap-2 transition-colors bg-transparent border-none cursor-pointer text-red-400 hover:bg-accent"
            onClick={() => { deleteFrame(frameMenu.idx); setFrameMenu(null); }}
          >
            <Trash2 size={14} className="text-red-400" /> 删除此帧
          </button>
        </div>,
        document.body,
      )}
      {menuOverlay}
    </NodeShell>
  );
}
