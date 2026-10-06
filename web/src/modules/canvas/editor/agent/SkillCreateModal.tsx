import { useEffect, useState } from 'react';
import { Loader2, X } from 'lucide-react';

import { api } from '@/shared/api';
import type { SkillDraft } from './types';

export interface SkillCreateModalProps {
  draft: SkillDraft | null;
  onClose: () => void;
  onSaved: () => void;
}

// 类别输入框的多值解析：英文/中文逗号、顿号都当分隔符，trim 后去空去重。
// 首元素即主分类（与后端 normalizeSkillCategories 的「categories[0] 回写
// category」约定一致）。
function parseCategories(text: string): string[] {
  return Array.from(
    new Set(
      text
        .split(/[,，、]/)
        .map((s) => s.trim())
        .filter(Boolean),
    ),
  );
}

export function SkillCreateModal({ draft, onClose, onSaved }: SkillCreateModalProps) {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [category, setCategory] = useState('Custom');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (draft) {
      setName(draft.name);
      setDescription(draft.description ?? '');
      setCategory(draft.category ?? 'Custom');
      setError(null);
    }
  }, [draft]);

  if (!draft) return null;

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    try {
      // categories 是后端真源；category 冗余带上首元素，兼容还没升级多分类的
      // 旧服务端（新服务端会自己按 categories[0] 回写主分类）。
      const categories = parseCategories(category);
      await api.post('/me/skills', {
        id: draft.id,
        name,
        description,
        category: categories[0] ?? '',
        categories,
        icon_url: '',
        skill_type: 'force_tool',
        system_prompt_addon: draft.system_prompt_addon ?? '',
        force_tools: draft.force_tools ?? [],
        param_defaults: draft.param_defaults ?? {},
        is_published: false,
        sort_weight: 0,
      });
      onSaved();
    } catch (e) {
      const msg = e instanceof Error ? e.message : 'save failed';
      setError(msg);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/40 z-[999] flex items-center justify-center">
      <div className="bg-card border border-input rounded-lg shadow-2xl w-[420px] max-w-[90vw] flex flex-col gap-3 p-5">
        <div className="flex items-center justify-between">
          <div className="text-base font-semibold text-foreground">保存为 Skill</div>
          <button
            type="button"
            onClick={onClose}
            disabled={saving}
            className="text-muted-foreground hover:text-foreground bg-transparent border-none cursor-pointer p-0 disabled:opacity-40"
            title="关闭"
          >
            <X size={16} />
          </button>
        </div>
        <div className="text-xs text-muted-foreground">来自当前对话抽取的 Skill 草稿</div>
        <label className="text-xs text-foreground flex flex-col gap-1">
          <span>名称</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="bg-background border border-input focus:border-blue-500 rounded px-2 py-1.5 text-xs text-foreground outline-none placeholder:text-muted-foreground"
          />
        </label>
        <label className="text-xs text-foreground flex flex-col gap-1">
          <span>描述</span>
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={2}
            className="bg-background border border-input focus:border-blue-500 rounded px-2 py-1.5 text-xs text-foreground outline-none resize-none placeholder:text-muted-foreground"
          />
        </label>
        <label className="text-xs text-foreground flex flex-col gap-1">
          <span>类别（多个用逗号分隔，首个为主类别）</span>
          <input
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            className="bg-background border border-input focus:border-blue-500 rounded px-2 py-1.5 text-xs text-foreground outline-none placeholder:text-muted-foreground"
          />
        </label>
        <div className="text-[11px] text-muted-foreground">
          ID: {draft.id} · Tools: {(draft.force_tools ?? []).join(', ') || '(none)'}
        </div>
        {error && <div className="text-xs text-red-400">{error}</div>}
        <div className="flex justify-end gap-2 mt-1">
          <button
            type="button"
            onClick={onClose}
            disabled={saving}
            className="px-3 py-1.5 text-xs rounded border border-input bg-card hover:bg-popover text-foreground cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          >
            取消
          </button>
          <button
            type="button"
            onClick={handleSave}
            disabled={saving || !name.trim()}
            className="px-3 py-1.5 text-xs rounded border-none bg-blue-600 hover:bg-blue-500 text-white cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed flex items-center gap-1.5"
          >
            {saving && <Loader2 size={12} className="animate-spin" />}
            {saving ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </div>
  );
}
