import { useEffect, useRef } from 'react';
import { Image as ImageIcon } from 'lucide-react';
import { useCanvasStore } from '../store';
import { NodeShell } from '../NodeShell';
import { uploadFileIntoNode } from '../utils/uploadIntoNode';
import { useMediaNodeMenu } from './useMediaNodeMenu';
import { ImageActionsBar } from './ImageActionsBar';
import { useThumbUrl } from '../thumbs';
import { useStoryboardMode } from '../../storyboard/StoryboardContext';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';

export function InputImageNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodes.find((n) => n.id === id));
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const resizeNode = useCanvasStore((s) => s.resizeNode);
  const getConnectedInputs = useCanvasStore((s) => s.getConnectedInputs);
  const setLightbox = useCanvasStore((s) => s.setLightbox);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { onContextMenu, overlay: menuOverlay, badge: seedanceBadge } = useMediaNodeMenu({
    url: node?.content,
    kind: 'image',
    nodeId: id,
  });

  // 提前算好 displayUrl（不靠 `node` 短路），这样下面的 auto-fit effect
  // 才能无条件执行——hooks 必须在任何提前 return 之前。
  const linked = getConnectedInputs(id);
  const displayUrl = node?.content || linked.images[0] || '';
  // 节点内展示走缩略图；Lightbox/生成 inputs 仍读 displayUrl（原图）。
  const thumbUrl = useThumbUrl(displayUrl);
  // 故事板详情面板里这张媒体是主角，占满预览区、用户就是来看清的：直接上原图，
  // 也不需要再点开 Lightbox（开出来还是同一张）。画布上必须相反——节点才两三百
  // 像素宽、一张画布可能挂上百个，缩略图省流量，双击预览是唯一看清的途径。
  const storyboardMode = useStoryboardMode();
  const autoFittedFor = node?.settings?.autoFittedFor as string | undefined;

  // 把节点宽高比自动适配到当前显示的图片（自己上传的 OR 上游引用的）。
  // 持久化的 `autoFittedFor` 记录了我们已经适配过哪个 URL，这样项目重载
  // 时不会覆盖用户后来手动调整的尺寸。探测用缩略图（等比缩放，宽高比
  // 不变），避免为了量尺寸下载整张原图；缩略图未换取到之前先不探测。
  useEffect(() => {
    if (!displayUrl || !thumbUrl) return;
    if (autoFittedFor === displayUrl) return;
    const probe = new Image();
    // 探测也统一 CORS 模式（语义见 mediaCrossOrigin 注释）：不带的话这次加载
    // 会把「无 ACAO 的响应」写进缓存，之后读像素点位命中毒缓存被 CORS 拦截。
    const co = mediaCrossOrigin(thumbUrl);
    if (co) probe.crossOrigin = co;
    probe.onload = () => {
      const aspect = probe.naturalWidth / probe.naturalHeight;
      if (!isFinite(aspect) || aspect <= 0) return;
      const targetArea = 290 * 290;
      const h = Math.sqrt(targetArea / aspect);
      const w = h * aspect;
      resizeNode(id, Math.max(200, Math.round(w)), Math.max(200, Math.round(h)));
      updateNodeSettings(id, { autoFittedFor: displayUrl });
    };
    probe.src = thumbUrl;
  }, [id, displayUrl, thumbUrl, autoFittedFor, resizeNode, updateNodeSettings]);

  if (!node) return null;

  const isLinked = !node.content && !!displayUrl;

  const handleFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    // 清掉 autoFittedFor，让 effect 重新适配到新图片。
    await uploadFileIntoNode(id, file, { autoFittedFor: undefined });
  };

  return (
    <NodeShell
      titleExtra={seedanceBadge}
      node={node}
      inputs={0}
      minWidth={220}
      minHeight={220}
      bodyOverflow="visible"
    >
      {/* 故事板详情面板里节点是铺满容器的静态预览，没有选中/工具条语义。 */}
      <ImageActionsBar nodeId={id} visible={!!displayUrl && !storyboardMode} />
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={handleFile}
      />
      {/* 这里不接管拖放:文件拖到画布任何位置都由 CanvasEditor 统一新建节点。
          节点自己再接一份会「既替换本节点又新建一个」(drop 先在节点触发,再冒泡)。
          换图走下面的「更换」按钮。 */}
      <div
        className="relative w-full h-full flex flex-col items-center justify-center transition-colors"
        onContextMenu={onContextMenu}
      >
        {displayUrl ? (
          <div
            className={`relative w-full h-full ${storyboardMode ? '' : 'cursor-zoom-in'}`}
            onDoubleClick={storyboardMode ? undefined : (e) => {
              e.stopPropagation();
              setLightbox({ url: displayUrl, kind: 'image', title: '图片输入' });
            }}
            title={storyboardMode ? undefined : '双击查看大图'}
          >
            <img
              src={storyboardMode ? displayUrl : thumbUrl}
              crossOrigin={mediaCrossOrigin(storyboardMode ? displayUrl : thumbUrl)}
              loading="lazy"
              decoding="async"
              className="w-full h-full object-contain bg-black/50"
              alt=""
              draggable={false}
              style={{ imageRendering: 'auto' }}
            />
            {isLinked && (
              <div className="absolute top-2 left-2 text-[10px] px-1.5 py-0.5 rounded backdrop-blur-sm border bg-blue-500/20 text-blue-200 border-blue-300/30">
                上游引用
              </div>
            )}
            {/* 悬停浮层（换图）。故事板详情面板里不给：那儿是用来把图看清楚的，
                一 hover 就蒙上一层 40% 黑、正中压个按钮，恰好挡住要看的东西。
                「更换图片」是编辑动作，跟看图不是一回事，画布上那个节点有同样的
                按钮，要换切回去就行。 */}
            {!storyboardMode && (
              <div className="absolute inset-0 bg-black/40 transition-opacity gap-2 opacity-0 group-hover:opacity-100 flex flex-col items-center justify-center">
                <div className="flex items-center gap-2">
                  <label
                    className="cursor-pointer px-3 py-1.5 rounded-lg text-xs backdrop-blur-sm border bg-white/10 hover:bg-white/20 text-white border-white/10 transition-colors"
                    onMouseDown={(e) => e.stopPropagation()}
                  >
                    更换
                    <input
                      type="file"
                      className="hidden"
                      accept="image/*"
                      onChange={handleFile}
                    />
                  </label>
                </div>
              </div>
            )}
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center h-full w-full">
            <div className="w-12 h-12 rounded-2xl flex items-center justify-center mb-3 border bg-popover border-input/50">
              <ImageIcon className="w-6 h-6 text-muted-foreground" />
            </div>
            {node.status === 'uploading' ? (
              <div className="text-xs text-sidebar-foreground canvas-pulse">上传中…</div>
            ) : (
              <label
                className="px-4 py-2 rounded-lg text-xs font-medium transition-colors cursor-pointer bg-blue-600 hover:bg-blue-500 text-white"
                onMouseDown={(e) => e.stopPropagation()}
              >
                选择图片
                <input
                  type="file"
                  className="hidden"
                  accept="image/*"
                  onChange={handleFile}
                />
              </label>
            )}
          </div>
        )}
      </div>
      {menuOverlay}
    </NodeShell>
  );
}
