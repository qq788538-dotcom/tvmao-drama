import { useMemo, useState } from 'react';
import { App as AntApp, Modal, Pagination, Segmented, Spin } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { Check, Music } from 'lucide-react';
import {
  applyGenerationOutputToCanvas,
  listTasks,
  type GenerationTask,
} from '@/shared/api';
import { useCanvasStore } from './store';
import { persistNodeById } from './persistence/canvasMutations';
import { ThumbImg } from './ThumbImg';
import { mediaCrossOrigin } from './utils/mediaCrossOrigin';
import type { NodeType } from './types';

type PickKind = 'image' | 'video' | 'audio';

const MAX_SELECT = 10;
const PAGE_SIZE_OPTIONS = [15, 30, 60];

/** 与素材库 ItemDetail 的 nodeTypeFor 同一张映射表（video 的内部类型名是
 *  video-input，历史遗留的命名方向差异，见 CLAUDE.md）。 */
function nodeTypeFor(kind: PickKind): NodeType {
  if (kind === 'image') return 'input-image';
  if (kind === 'video') return 'video-input';
  return 'input-audio';
}

/** 一格 = 一个产物（按图平铺），不是一个任务——多产物任务拆开展示。 */
interface OutputEntry {
  taskId: number;
  outputIndex: number;
  url: string;
}

function entryKey(e: OutputEntry): string {
  return `${e.taskId}:${e.outputIndex}`;
}

function flattenOutputs(tasks: GenerationTask[]): OutputEntry[] {
  const out: OutputEntry[] = [];
  for (const task of tasks) {
    const urls = task.output?.outputUrls ?? [];
    urls.forEach((url, i) => {
      if (url) out.push({ taskId: task.id, outputIndex: i, url });
    });
  }
  return out;
}

interface Props {
  projectId: number;
  /** 双击画布的世界坐标——选中的产物从这里开始按网格铺开 */
  worldX: number;
  worldY: number;
  /** 拖连线松手进菜单的来源节点：落画布后自动连线（与菜单添加节点同款行为） */
  sourceNodeId?: string;
  onClose: () => void;
}

export function GenerationHistoryPickerModal({ projectId, worldX, worldY, sourceNodeId, onClose }: Props) {
  const { message } = AntApp.useApp();
  const addNode = useCanvasStore((s) => s.addNode);
  const addConnection = useCanvasStore((s) => s.addConnection);
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);

  const [kind, setKind] = useState<PickKind>('image');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(PAGE_SIZE_OPTIONS[0]);
  const [selected, setSelected] = useState<Map<string, OutputEntry>>(new Map());
  const [applying, setApplying] = useState(false);

  // 不传 projectId → 跨所有画布；只要成功任务（产物才有实体可引用）。
  const tasksQuery = useQuery({
    queryKey: ['gen-history-picker', { kind, page, pageSize }],
    queryFn: () =>
      listTasks({ status: 'succeeded', kind, limit: pageSize, offset: (page - 1) * pageSize }),
    staleTime: 5_000,
  });

  const entries = useMemo(() => flattenOutputs(tasksQuery.data?.data ?? []), [tasksQuery.data]);
  const total = tasksQuery.data?.total ?? 0;

  const toggle = (e: OutputEntry) => {
    setSelected((prev) => {
      const next = new Map(prev);
      const key = entryKey(e);
      if (next.has(key)) {
        next.delete(key);
      } else {
        if (next.size >= MAX_SELECT) {
          message.warning(`最多选择 ${MAX_SELECT} 个`);
          return prev;
        }
        next.set(key, e);
      }
      return next;
    });
  };

  const handleConfirm = async () => {
    const picks = Array.from(selected.values());
    if (picks.length === 0) return;
    setApplying(true);
    let ok = 0;
    for (let i = 0; i < picks.length; i++) {
      const pick = picks[i];
      try {
        // 服务端插 generation_ref 引用行（跨项目 GC 防线）并返回节点可用的
        // 签名 URL / assetId / 真实 kind——落画布流程与素材库 ItemDetail 同款。
        const res = await applyGenerationOutputToCanvas(pick.taskId, projectId, pick.outputIndex);
        const col = ok % 3;
        const row = Math.floor(ok / 3);
        const id = addNode(nodeTypeFor(res.kind), { x: worldX + col * 320, y: worldY + row * 320 });
        updateNode(id, { content: res.url, status: 'succeeded' });
        updateNodeSettings(id, { url: res.url, assetId: res.assetId, mimeType: res.mimeType });
        if (sourceNodeId) addConnection(sourceNodeId, id);
        persistNodeById(id);
        ok += 1;
      } catch (err: any) {
        message.error(err?.response?.data?.error || err?.message || '添加失败');
      }
    }
    setApplying(false);
    if (ok > 0) {
      message.success(`已添加 ${ok} 个到画布`);
      onClose();
    }
  };

  return (
    <Modal
      open
      onCancel={onClose}
      title="从生成历史选择"
      width={1080}
      destroyOnClose
      okText="确定"
      okButtonProps={{ disabled: selected.size === 0, loading: applying }}
      onOk={handleConfirm}
    >
      <div className="flex items-center justify-between mb-3">
        <Segmented
          value={kind}
          onChange={(v) => {
            setKind(v as PickKind);
            setPage(1);
          }}
          options={[
            { label: '图片', value: 'image' },
            { label: '视频', value: 'video' },
            { label: '音频', value: 'audio' },
          ]}
        />
        <span className="text-sm text-muted-foreground">
          已选 <span className="text-primary">{selected.size}/{MAX_SELECT}</span>
        </span>
      </div>

      {tasksQuery.isLoading ? (
        <div className="flex items-center justify-center py-16">
          <Spin />
        </div>
      ) : entries.length === 0 ? (
        <div className="py-16 text-center text-muted-foreground text-sm">
          还没有{kind === 'image' ? '图片' : kind === 'video' ? '视频' : '音频'}生成记录
        </div>
      ) : (
        <div className="grid grid-cols-5 gap-3 min-h-[280px] content-start">
          {entries.map((e) => {
            const key = entryKey(e);
            const isSelected = selected.has(key);
            return (
              <button
                key={key}
                type="button"
                onClick={() => toggle(e)}
                className={`relative aspect-square overflow-hidden rounded-xl border-2 border-solid bg-black/20 p-0 cursor-pointer transition-colors ${
                  isSelected ? 'border-primary' : 'border-transparent hover:border-white/30'
                }`}
              >
                {kind === 'video' ? (
                  <video
                    src={e.url}
                    crossOrigin={mediaCrossOrigin(e.url)}
                    className="w-full h-full object-cover pointer-events-none"
                    muted
                    playsInline
                    preload="metadata"
                  />
                ) : kind === 'audio' ? (
                  <span className="w-full h-full flex items-center justify-center">
                    <Music size={28} className="text-muted-foreground" />
                  </span>
                ) : (
                  <ThumbImg src={e.url} alt="生成的图片" className="w-full h-full object-cover" />
                )}
                {isSelected && (
                  <span className="absolute top-1.5 right-1.5 flex h-5 w-5 items-center justify-center rounded-full bg-primary text-primary-foreground">
                    <Check size={13} />
                  </span>
                )}
              </button>
            );
          })}
        </div>
      )}

      <div className="mt-4 flex justify-start">
        <Pagination
          current={page}
          pageSize={pageSize}
          total={total}
          showSizeChanger
          pageSizeOptions={PAGE_SIZE_OPTIONS}
          showQuickJumper
          size="small"
          onChange={(p, ps) => {
            setPage(ps !== pageSize ? 1 : p);
            setPageSize(ps);
          }}
        />
      </div>
    </Modal>
  );
}
