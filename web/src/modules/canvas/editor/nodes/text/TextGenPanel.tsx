// 文本节点的 AI 生成输入条（LibTV 图11 同款交互）：选文本/视觉模型 + 输入指令，
// 连了上游图片节点时自动作为视觉输入（图片反推提示词场景）。提交走 /canvas-tasks
// 统一 DB 队列，结果由后端 sink 权威写回 settings.text，轮询在 TextNode 里。
import { useState } from 'react';
import { ArrowUp, Check, ChevronDown, ImageIcon } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { useCanvasStore } from '../../store';
import { persistNodeById, flushNodePatch, scheduleNodePatch } from '../../persistence/canvasMutations';
import { listRuntimeModels, submitCanvasTask } from '@/shared/api';

export function TextGenPanel({ nodeId }: { nodeId: string }) {
  const node = useCanvasStore((s) => s.nodes.find((n) => n.id === nodeId));
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const getConnectedInputs = useCanvasStore((s) => s.getConnectedInputs);
  const [modelOpen, setModelOpen] = useState(false);

  const modelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 60_000,
  });

  if (!node) return null;

  // 模型下拉的口径：/models 里 type==='text' 的行。
  // VL（视觉）模型也配成 text 类型——是否吃图由模型的 requestTemplate 决定。
  const textModels = (modelsQuery.data ?? []).filter((m) => m.type === 'text');
  const selectedModelId = (node.settings?.modelId as string | undefined) ?? textModels[0]?.modelId ?? '';
  const selectedModel = textModels.find((m) => m.modelId === selectedModelId);

  // 指令存 settings.genPrompt——不能占 settings.text（那是正文），也别叫 prompt
  //（gen-image 的字段名，语义是「生成媒体的提示词」，混用会让 CLI/agent 误读）。
  const genPrompt = (node.settings?.genPrompt as string | undefined) ?? '';
  const isGenerating = node.status === 'generating';
  const linkedImageCount = getConnectedInputs(nodeId).images.length;
  const canSubmit = !isGenerating && Boolean(selectedModelId) && genPrompt.trim().length > 0;

  const handleGenerate = async () => {
    if (!canSubmit) return;
    const linked = getConnectedInputs(nodeId);
    const inputs: Record<string, unknown> = { prompt: genPrompt.trim() };
    if (linked.images.length > 0) {
      // 与 gen-image 的入参命名对齐：images 全量 + image 首张，模板端两种写法都好接。
      inputs.images = linked.images;
      inputs.image = linked.images[0];
    }
    updateNode(nodeId, { status: 'generating', error: undefined, taskId: undefined });
    // 先把「生成中」状态落库再发任务（同 GenImageNode：刷新页面能恢复轮询）。
    persistNodeById(nodeId);
    try {
      const projectId =
        Number(window.location.pathname.split('/canvas/')[1]?.split('/')[0]) || undefined;
      const task = await submitCanvasTask({
        projectId: projectId!,
        nodeId,
        modelId: selectedModelId,
        inputs,
      });
      updateNode(nodeId, { taskId: task.taskId });
      persistNodeById(nodeId);
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: string } }; message?: string };
      updateNode(nodeId, {
        status: 'failed',
        error: e?.response?.data?.error || e?.message || '生成失败',
        taskId: undefined,
      });
      persistNodeById(nodeId);
    }
  };

  return (
    <div
      className="bg-card rounded-2xl border border-border shadow-xl p-3 flex flex-col gap-2"
      onMouseDown={(e) => e.stopPropagation()}
      onWheel={(e) => e.stopPropagation()}
    >
      <textarea
        value={genPrompt}
        onChange={(e) => {
          updateNodeSettings(nodeId, { genPrompt: e.target.value });
          scheduleNodePatch(nodeId, { settings: { genPrompt: e.target.value } });
        }}
        onBlur={() => {
          void flushNodePatch(nodeId);
        }}
        placeholder={
          linkedImageCount > 0
            ? '写下指令，例如：根据图片，帮我生成提示词'
            : '写下你想生成的内容，例如：写一段城市夜景的分镜描述'
        }
        rows={2}
        disabled={isGenerating}
        className="w-full resize-none outline-none text-sm bg-transparent text-foreground placeholder-muted-foreground custom-scrollbar border-none p-1"
      />
      <div className="flex items-center justify-between">
        <div className="relative">
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              setModelOpen((v) => !v);
            }}
            className="flex items-center gap-1.5 px-2 py-1 rounded-lg text-xs text-foreground bg-transparent border-none cursor-pointer hover:bg-accent/60 transition-colors"
          >
            {selectedModel?.name ?? (modelsQuery.isLoading ? '加载模型…' : '无可用模型')}
            <ChevronDown size={12} className="text-muted-foreground" />
          </button>
          {modelOpen && (
            <div className="absolute bottom-full left-0 mb-1 min-w-[180px] max-h-56 overflow-y-auto custom-scrollbar bg-popover border border-border rounded-xl shadow-xl py-1 z-40">
              {textModels.map((m) => (
                <button
                  key={m.modelId}
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    updateNodeSettings(nodeId, { modelId: m.modelId });
                    scheduleNodePatch(nodeId, { settings: { modelId: m.modelId } });
                    void flushNodePatch(nodeId);
                    setModelOpen(false);
                  }}
                  className="w-full flex items-center justify-between gap-2 px-3 py-1.5 text-xs text-left text-foreground bg-transparent border-none cursor-pointer hover:bg-accent/60"
                >
                  <span className="truncate">{m.name}</span>
                  {m.modelId === selectedModelId && <Check size={12} className="text-green-500 shrink-0" />}
                </button>
              ))}
              {textModels.length === 0 && (
                <div className="px-3 py-1.5 text-xs text-muted-foreground">请在设置中配置文本模型</div>
              )}
            </div>
          )}
        </div>
        <div className="flex items-center gap-2">
          {linkedImageCount > 0 && (
            <span className="flex items-center gap-1 text-[11px] text-muted-foreground" title="已连接的上游图片将作为视觉输入">
              <ImageIcon size={12} />
              {linkedImageCount}
            </span>
          )}
          <button
            type="button"
            onClick={handleGenerate}
            disabled={!canSubmit}
            title={isGenerating ? '生成中…' : '生成'}
            className={`w-8 h-8 rounded-full flex items-center justify-center border-none transition-colors ${
              canSubmit
                ? 'bg-foreground text-background cursor-pointer hover:opacity-85'
                : 'bg-muted text-muted-foreground cursor-not-allowed'
            }`}
          >
            <ArrowUp size={15} />
          </button>
        </div>
      </div>
    </div>
  );
}
