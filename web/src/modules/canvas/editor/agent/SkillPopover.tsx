import { useEffect, useMemo, useState } from 'react';
import { Box, Film, Image as ImageIcon, Megaphone, Palette, ShoppingBag, Share2, Video, X, Zap } from 'lucide-react';
import { listAgentSkills, listSkillCategories, type AgentSkillDTO, type SkillCategoryDTO } from '@/shared/api';
import {
  buildCategoryGroups,
  colorVisual,
  filterSkillsByGroup,
  LEGACY_CATEGORY_COLOR,
  NEUTRAL_VISUAL,
} from './skillCategoryGroups';

export interface SkillPopoverProps {
  open: boolean;
  onClose: () => void;
  onSelect: (skill: AgentSkillDTO) => void;
}

// 已知分类名 → 图标。颜色已交给后台分类字典驱动（见 skillCategoryGroups），
// 图标后台没得配，仍按名字映射；字典里出现的新分类落到通用 Box。
const CATEGORY_ICON: Record<string, typeof Box> = {
  视频: Video,
  社交媒体: Share2,
  电子商务: ShoppingBag,
  品牌推广: Palette,
  营销: Megaphone,
  图像: ImageIcon,
  Video: Film,
};

// 分类字典跨弹层开合缓存（模块级）：弹层每次输入 @ 都会重开，字典本身极少变，
// 没必要每次都打接口。失败不缓存——下次打开重试，中间用回退分组顶着。
let categoriesPromise: Promise<SkillCategoryDTO[]> | null = null;
function fetchCategoriesOnce(): Promise<SkillCategoryDTO[]> {
  if (!categoriesPromise) {
    categoriesPromise = listSkillCategories().catch((err) => {
      categoriesPromise = null;
      throw err;
    });
  }
  return categoriesPromise;
}

export function SkillPopover({ open, onClose, onSelect }: SkillPopoverProps) {
  const [skills, setSkills] = useState<AgentSkillDTO[]>([]);
  // null = 字典还没拿到（加载中或拉取失败）→ buildCategoryGroups 回退为按
  // skills 里出现的分类原样分组，弹层不因字典接口挂掉而白屏。
  const [apiCategories, setApiCategories] = useState<SkillCategoryDTO[] | null>(null);
  const [activeCategory, setActiveCategory] = useState<string>('');

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    void listAgentSkills().then((list) => {
      if (!cancelled) setSkills(list);
    });
    fetchCategoriesOnce().then(
      (list) => {
        if (!cancelled) setApiCategories(list);
      },
      () => {
        if (!cancelled) setApiCategories(null);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [open]);

  // 多分类 skill 按 categories 展开归组（旧行回退 [category]），见
  // skillCategoryGroups 的 effectiveCategories。
  const { groups, toGroup } = useMemo(
    () => buildCategoryGroups(skills, apiCategories),
    [skills, apiCategories],
  );

  // 没有「全部」胶囊（用户拍板砍掉），默认选中第一个分组；字典晚于 skills 到达
  // 时分组名可能重算（孤儿被并入「其他」），已选中的胶囊若已不存在同样落回第一
  // 个分组，避免列表卡在永远为空的过滤态。分组还没算出来前保持 ''（不过滤），
  // 加载过渡态不闪「暂无匹配」。
  useEffect(() => {
    if (groups.length > 0 && !groups.includes(activeCategory)) setActiveCategory(groups[0]);
  }, [groups, activeCategory]);

  // 分组名 → AntD 色名。字典在手用字典色；回退时用旧写死配色，保持与现状一致。
  const groupColor = useMemo(() => {
    const map: Record<string, string> = apiCategories
      ? Object.fromEntries(apiCategories.map((c) => [c.name, c.color]))
      : LEGACY_CATEGORY_COLOR;
    return (group: string) => map[group];
  }, [apiCategories]);

  const visualFor = (group: string) => {
    const color = groupColor(group);
    const { bg, fg } = color ? colorVisual(color) : NEUTRAL_VISUAL;
    return { icon: CATEGORY_ICON[group] ?? Box, bg, fg };
  };

  // 胶囊筛选是「包含即命中」；activeCategory='' 只出现在分组算出来前的过渡态，
  // 此时原样返回 skills（每个 skill 恰好一行——不因多分类重复）。
  const filtered = useMemo(
    () => filterSkillsByGroup(skills, activeCategory, toGroup),
    [skills, activeCategory, toGroup],
  );

  if (!open) return null;

  return (
    <div
      // 固定宽度，锚定在输入区左下角。更重的边框 + 阴影 + bg-popover（比面板的
      // bg-card 浅一档），让 popover 明显地浮在聊天界面之上。
      className="absolute bottom-[60px] left-3 w-[420px] max-w-[calc(100%-24px)] max-h-[440px] bg-popover border-2 border-solid border-blue-500/60 rounded-xl shadow-2xl flex flex-col z-30 overflow-hidden ring-1 ring-black/40"
      onMouseDown={(e) => e.stopPropagation()}
    >
      {/* 头部 */}
      <div className="flex items-center justify-between px-3 py-2.5 border-b border-white/10 shrink-0">
        <div className="flex items-center gap-1.5 text-sm font-semibold text-foreground">
          <Zap size={14} className="text-blue-400" />
          Skills
        </div>
        <button
          type="button"
          onClick={onClose}
          className="w-6 h-6 flex items-center justify-center text-muted-foreground hover:text-foreground bg-transparent border-none cursor-pointer p-0"
          title="关闭"
        >
          <X size={14} />
        </button>
      </div>

      {/* 类别胶囊行——数量多时横向滚动 */}
      <div className="flex gap-1.5 px-3 py-2 overflow-x-auto shrink-0 custom-scrollbar">
        {groups.map((c) => {
          const v = visualFor(c);
          const Icon = v.icon;
          return (
            <Chip
              key={c}
              active={activeCategory === c}
              label={c}
              icon={<Icon size={12} className={activeCategory === c ? 'text-white' : v.fg} />}
              onClick={() => setActiveCategory(c)}
            />
          );
        })}
      </div>

      {/* Skill 列表 */}
      <div className="flex-1 overflow-y-auto flex flex-col custom-scrollbar">
        {filtered.length === 0 && (
          <div className="text-muted-foreground text-xs px-3 py-6 text-center">暂无匹配的 skill</div>
        )}
        {filtered.map((s) => {
          // 行图标配色跟主分类走（后端保证 category = categories[0]）。
          const v = visualFor(toGroup(s.category));
          const Icon = v.icon;
          return (
            <button
              key={s.id}
              type="button"
              onClick={() => {
                onSelect(s);
                onClose();
              }}
              className="text-left px-3 py-2.5 border-b border-white/5 hover:bg-accent/60 cursor-pointer flex items-center gap-3 bg-transparent border-l-0 border-r-0 border-t-0"
            >
              {s.icon_url ? (
                // 后台配了图标就用图（icon_url 已由服务端签好名）；没配回落到分类图标。
                <img
                  src={s.icon_url}
                  alt=""
                  className="w-10 h-10 rounded-lg object-cover shrink-0"
                />
              ) : (
                <div className={`w-10 h-10 rounded-lg ${v.bg} ${v.fg} flex items-center justify-center shrink-0`}>
                  <Icon size={18} />
                </div>
              )}
              <div className="min-w-0 flex-1">
                <div className="font-semibold text-[13px] text-foreground truncate">{s.name}</div>
                {s.description && (
                  <div className="text-[11px] text-muted-foreground mt-0.5 line-clamp-2">
                    {s.description}
                  </div>
                )}
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}

function Chip({
  active,
  label,
  icon,
  onClick,
}: {
  active: boolean;
  label: string;
  icon?: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-1 px-2.5 py-1 text-[11px] rounded-full border cursor-pointer transition-colors shrink-0 ${
        active
          ? 'bg-blue-600 text-white border-blue-500'
          : 'bg-card text-sidebar-foreground border-white/15 hover:bg-accent'
      }`}
    >
      {icon}
      {label}
    </button>
  );
}
