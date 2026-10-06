import { useEffect, useRef, useState } from 'react';
import { MoreHorizontal, X, ZoomIn } from 'lucide-react';
import { useCanvasStore } from '../../store';
import { PopoverShell, FIELD_BOX } from './cellEditors';
import { ImagePreview } from './ImagePreview';
import { ThumbImg } from '../../ThumbImg';
import type { ScriptAsset, ScriptAssets } from './scriptData';

type SectionKey = keyof ScriptAssets;

// 资产编辑右侧抽屉（对标上游：单击资产图 → 右侧滑出编辑面板）。
// 字段失焦自动保存，没有保存按钮——名称改动由父组件做唯一校验 + token 全表联动，
// 校验失败在抽屉内红字提示且不写回。抽屉内形象图带 🔍 预览与 ··· 菜单，与卡片
// 右上角为同一套操作（onMenuAction 上抛给父组件复用同一份处理逻辑）。
export type AssetMenuAction = 'replace' | 'gen' | 'jump' | 'clear' | 'remove';

export function AssetEditDrawer({
  section,
  sectionLabel,
  asset,
  readOnly = false,
  onSaveName,
  onSaveDesc,
  onMenuAction,
  onClose,
}: {
  section: SectionKey;
  sectionLabel: string;
  asset: ScriptAsset;
  readOnly?: boolean;
  /** 返回错误文案（校验失败不写回），null=保存成功 */
  onSaveName: (section: SectionKey, assetId: string, name: string) => string | null;
  onSaveDesc: (section: SectionKey, assetId: string, desc: string) => void;
  onMenuAction: (action: AssetMenuAction, section: SectionKey, asset: ScriptAsset) => void;
  onClose: () => void;
}) {
  const nodes = useCanvasStore((s) => s.nodes);
  const [preview, setPreview] = useState(false);
  const linkedNode = asset.linkedNodeId ? nodes.find((n) => n.id === asset.linkedNodeId) : undefined;
  const thumb = linkedNode?.content;

  const [name, setName] = useState(asset.name);
  const [desc, setDesc] = useState(asset.description);
  const [nameError, setNameError] = useState('');
  const [menuOpen, setMenuOpen] = useState(false);

  // 切换编辑对象时刷新草稿（同一个抽屉实例连续编辑不同资产）。
  useEffect(() => {
    setName(asset.name);
    setDesc(asset.description);
    setNameError('');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [asset.id]);

  const saveName = () => {
    if (readOnly || name === asset.name) return;
    const err = onSaveName(section, asset.id, name);
    setNameError(err ?? '');
    if (err) return;
  };
  const saveDesc = () => {
    if (readOnly || desc === asset.description) return;
    onSaveDesc(section, asset.id, desc);
  };

  // 关闭前 flush：点抽屉外/X 时字段可能来不及触发 blur（CanvasEditor 全局
  // mousedown preventDefault 同根因），capture 阶段先落值。ref 镀最新草稿。
  const flushRef = useRef(() => {});
  flushRef.current = () => { saveName(); saveDesc(); };
  const containerRef = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  useEffect(() => {
    const onMouseDown = (e: MouseEvent) => {
      if (!containerRef.current?.contains(e.target as Node)) {
        flushRef.current();
        onCloseRef.current();
      }
    };
    document.addEventListener('mousedown', onMouseDown, true);
    return () => document.removeEventListener('mousedown', onMouseDown, true);
  }, []);
  useEffect(() => () => flushRef.current(), []);

  const menuItems: { action: AssetMenuAction; text: string; danger?: boolean }[] = [
    { action: 'replace', text: '替换图片' },
    { action: 'gen', text: `AI 生${sectionLabel}` },
    { action: 'jump', text: '跳转至节点' },
    { action: 'clear', text: '清除图片' },
    { action: 'remove', text: '删除', danger: true },
  ];

  return (
    <div
      ref={containerRef}
      data-testid="asset-edit-drawer"
      className="absolute bottom-0 right-0 top-0 z-20 flex w-[min(560px,80vw)] flex-col overflow-y-auto border-l border-white/10 bg-[#171717] shadow-2xl"
      onMouseDown={(e) => e.stopPropagation()}
    >
      <div className="flex items-center justify-between border-b border-white/10 px-5 py-4">
        <h2 className="text-base font-medium text-foreground">编辑{sectionLabel}</h2>
        <button
          type="button"
          data-testid="asset-drawer-close"
          onClick={() => { flushRef.current(); onClose(); }}
          aria-label="关闭"
          className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground bg-transparent border-none cursor-pointer"
        >
          <X size={18} />
        </button>
      </div>

      <div className="flex flex-col gap-4 px-5 py-4">
        <div>
          <div className="mb-2 text-sm text-foreground">{sectionLabel}形象</div>
          <div className="relative">
            <div className="flex h-64 w-full items-center justify-center overflow-hidden rounded-xl border border-white/10 bg-white/[0.03]">
              {thumb ? (
                <ThumbImg src={thumb} alt={asset.name} className="h-full w-full object-cover" />
              ) : (
                <span className="text-xs text-muted-foreground">生成或上传{sectionLabel}图</span>
              )}
            </div>
            <div className="absolute right-2 top-2 flex items-center gap-1.5">
              {thumb && (
                <button
                  type="button"
                  aria-label="放大预览"
                  onClick={() => setPreview(true)}
                  className="rounded-lg bg-black/60 p-1.5 text-foreground hover:bg-black/80 border-none cursor-pointer"
                >
                  <ZoomIn size={15} />
                </button>
              )}
              <button
                type="button"
                aria-label="资产菜单"
                disabled={readOnly}
                onClick={() => setMenuOpen((v) => !v)}
                className="rounded-lg bg-black/60 p-1.5 text-foreground hover:bg-black/80 border-none cursor-pointer disabled:cursor-not-allowed disabled:opacity-50"
              >
                <MoreHorizontal size={15} />
              </button>
              {menuOpen && (
                <PopoverShell onClose={() => setMenuOpen(false)} className="w-44 !p-1.5">
                  {menuItems.map(({ action, text, danger }) => (
                    <button
                      key={action}
                      type="button"
                      onClick={() => { setMenuOpen(false); onMenuAction(action, section, asset); }}
                      className={
                        'w-full rounded-lg bg-transparent border-none cursor-pointer px-3 py-2 text-left text-sm hover:bg-white/5 '
                        + (danger ? 'border-t border-white/10 text-red-400' : 'text-foreground')
                      }
                    >
                      {text}
                    </button>
                  ))}
                </PopoverShell>
              )}
            </div>
          </div>
        </div>

        <div>
          <div className="mb-2 text-sm text-foreground">{sectionLabel}名称</div>
          <input
            data-testid="asset-name-input"
            value={name}
            readOnly={readOnly}
            onChange={(e) => setName(e.target.value)}
            onBlur={saveName}
            className={`w-full ${FIELD_BOX}`}
          />
          {nameError && <div className="mt-1.5 text-xs text-red-400">{nameError}</div>}
        </div>

        <div>
          <div className="mb-2 text-sm text-foreground">{sectionLabel}描述</div>
          <textarea
            data-testid="asset-desc-input"
            value={desc}
            readOnly={readOnly}
            rows={10}
            placeholder={`${sectionLabel}描述（用于生图提示词）`}
            onChange={(e) => setDesc(e.target.value)}
            onBlur={saveDesc}
            className={`w-full resize-none ${FIELD_BOX}`}
          />
        </div>
      </div>

      {preview && thumb && (
        <ImagePreview url={thumb} title={asset.name} onClose={() => setPreview(false)} />
      )}
    </div>
  );
}
