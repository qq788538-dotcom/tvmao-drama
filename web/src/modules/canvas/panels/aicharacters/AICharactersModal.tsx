import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { App as AntApp, Select } from 'antd';
import { useInfiniteQuery } from '@tanstack/react-query';
import { Loader2, Search, X } from 'lucide-react';
import { fetchAICharacters, type AICharacter } from '@/shared/api';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { persistNodeById } from '@/modules/canvas/editor/persistence/canvasMutations';
import { AICharacterDetail } from './AICharacterDetail';
import { mediaCrossOrigin } from '@/modules/canvas/editor/utils/mediaCrossOrigin';

interface Props {
  open: boolean;
  onClose: () => void;
}

// AI 角色库全屏弹框：平台预置的公共角色目录（seedance_characters，已过合规校验）。
// 顶部 搜索 + 性别/年龄段 两个过滤；网格每批 50 条滚动加载。
export function AICharactersModal({ open, onClose }: Props) {
  const { message } = AntApp.useApp();
  const addNode = useCanvasStore((s) => s.addNode);
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);

  const [search, setSearch] = useState('');
  const [debounced, setDebounced] = useState('');
  const [gender, setGender] = useState('');
  const [ageGroup, setAgeGroup] = useState('');
  const [detailIndex, setDetailIndex] = useState<number | null>(null);

  // 搜索防抖：条件一变 useInfiniteQuery 的 key 就变、自动回到第一批。
  useEffect(() => {
    const t = setTimeout(() => setDebounced(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  const query = useInfiniteQuery({
    queryKey: ['ai-characters', debounced, gender, ageGroup],
    queryFn: ({ pageParam }) =>
      fetchAICharacters({
        ...(debounced && { search: debounced }),
        ...(gender && { gender }),
        ...(ageGroup && { ageGroup }),
        offset: pageParam,
      }),
    initialPageParam: 0,
    getNextPageParam: (last, all) =>
      last.hasMore ? all.reduce((n, p) => n + p.items.length, 0) : undefined,
    enabled: open,
  });

  const items = useMemo(
    () => query.data?.pages.flatMap((p) => p.items) ?? [],
    [query.data],
  );

  // 滚动加载：底部哨兵进入视口就拉下一批。
  const sentinelRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!open) return;
    const el = sentinelRef.current;
    if (!el) return;
    const ob = new IntersectionObserver((entries) => {
      if (entries[0]?.isIntersecting && query.hasNextPage && !query.isFetchingNextPage) {
        void query.fetchNextPage();
      }
    });
    ob.observe(el);
    return () => ob.disconnect();
  }, [open, query.hasNextPage, query.isFetchingNextPage, query.fetchNextPage, items.length]);

  if (!open) return null;

  // 与素材库的「应用到画布」(ItemDetail) 同样的落点算法，但**刻意不走服务端**：
  // 素材库那条路会插一条 assets 引用行，而公共角色图绝不能进用户的 assets 引用
  // 计数——用户删节点会触发 TryReclaim 把公共对象标成 orphan，7 天后从对象存储
  // 真删，1729 张图是全员共享的。纯前端插节点即可：节点 content 持签名 URL，
  // 快照持久化时 canonical 化回 media://，合规角标与 asset:// 提交由服务端
  // 公共资产兜底（SeedanceService.SetPublicAssets）自动生效。
  const applyToCanvas = (c: AICharacter) => {
    const view = useCanvasStore.getState().view;
    const cx = (window.innerWidth / 2 - view.x) / view.zoom;
    const cy = (window.innerHeight / 2 - view.y) / view.zoom;
    const id = addNode('input-image', { x: cx, y: cy });
    updateNode(id, { content: c.imageUrl, status: 'succeeded' });
    updateNodeSettings(id, { url: c.imageUrl });
    persistNodeById(id);
    message.success('已应用到画布');
    setDetailIndex(null);
    onClose();
  };

  return createPortal(
    <div className="fixed inset-0 z-[110] bg-background flex flex-col">
      <div className="flex items-center gap-4 px-6 py-4">
        <h2 className="text-foreground text-xl font-semibold m-0 shrink-0">虚拟人像库</h2>
        <div className="flex-1" />
        <button
          onClick={onClose}
          className="w-9 h-9 flex items-center justify-center rounded-md bg-transparent border-none text-muted-foreground hover:text-foreground cursor-pointer"
          aria-label="关闭虚拟人像库"
        >
          <X size={20} />
        </button>
      </div>

      <div className="flex items-center gap-3 px-6 pb-4 flex-wrap">
        <div className="flex items-center gap-2 px-4 py-2 rounded-full bg-popover focus-within:bg-accent transition-colors w-72">
          <Search size={16} className="text-muted-foreground shrink-0" />
          <input
            type="text"
            placeholder="搜索职业或人设"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="flex-1 min-w-0 bg-transparent text-foreground placeholder:text-muted-foreground text-sm outline-none border-none p-0"
          />
        </div>
        <Select
          value={gender}
          onChange={setGender}
          style={{ width: 110 }}
          options={[
            { value: '', label: '性别：全部' },
            { value: '男', label: '男' },
            { value: '女', label: '女' },
          ]}
        />
        <Select
          value={ageGroup}
          onChange={setAgeGroup}
          style={{ width: 130 }}
          options={[
            { value: '', label: '年龄段：全部' },
            { value: '青年', label: '青年' },
            { value: '中年', label: '中年' },
            { value: '老年', label: '老年' },
          ]}
        />
        {query.isFetching && !query.isFetchingNextPage && (
          <Loader2 size={16} className="animate-spin text-muted-foreground" />
        )}
      </div>

      <div className="flex-1 overflow-y-auto px-6 pb-6">
        {query.isError ? (
          <div className="text-muted-foreground text-sm py-12 text-center">
            加载失败，请稍后重试
          </div>
        ) : items.length === 0 && !query.isFetching ? (
          <div className="text-muted-foreground text-sm py-12 text-center">
            没有符合条件的角色
          </div>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-4">
            {items.map((c, i) => (
              <button
                key={c.assetId}
                onClick={() => setDetailIndex(i)}
                className="group text-left bg-card border border-border rounded-xl overflow-hidden cursor-pointer p-0 hover:border-foreground/40 transition-colors"
              >
                <div className="aspect-[3/4] bg-black/30 overflow-hidden">
                  <img
                    src={c.thumbUrl}
                    crossOrigin={mediaCrossOrigin(c.thumbUrl)}
                    alt={c.occupation}
                    loading="lazy"
                    className="w-full h-full object-cover group-hover:scale-105 transition-transform"
                  />
                </div>
                <div className="px-3 py-2.5">
                  <div className="text-foreground text-sm font-medium truncate" title={c.occupation}>
                    {c.occupation}
                  </div>
                  <div className="text-muted-foreground text-xs mt-0.5">
                    {c.gender} · {c.ageGroup} · {c.age}岁
                  </div>
                </div>
              </button>
            ))}
          </div>
        )}
        <div ref={sentinelRef} className="h-8 flex items-center justify-center">
          {query.isFetchingNextPage && (
            <Loader2 size={16} className="animate-spin text-muted-foreground" />
          )}
        </div>
      </div>

      {detailIndex !== null && (
        <AICharacterDetail
          items={items}
          index={detailIndex}
          onNavigate={(i) => {
            if (i >= 0 && i < items.length) setDetailIndex(i);
          }}
          onApply={applyToCanvas}
          onClose={() => setDetailIndex(null)}
        />
      )}
    </div>,
    document.body,
  );
}
