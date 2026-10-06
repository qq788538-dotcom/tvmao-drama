import { useRef, useState } from 'react';
import { CheckCircle2, Info, MoreHorizontal, Plus, ZoomIn, Loader2 } from 'lucide-react';
import { useCanvasStore } from '../../store';
import { uploadFileIntoNode } from '../../utils/uploadIntoNode';
import { PopoverShell } from './cellEditors';
import { useScriptSettings } from './useScriptSettings';
import { createUploadAssetNode, detachAssetNodeLink } from './assetNodeOps';
import { renameTokenInPrompt, type TokenKind } from './scriptTokens';
import { AddAssetDialog } from './AddAssetDialog';
import { AssetEditDrawer, type AssetMenuAction } from './AssetEditDrawer';
import { ImagePreview } from './ImagePreview';
import { ThumbImg } from '../../ThumbImg';
import type { ScriptAsset, ScriptAssets } from './scriptData';

type SectionKey = keyof ScriptAssets;

// label 对齐 skill：character/scene/item → 角色/场景/物品（内部字段 key 沿用 props，
// 改 key 要动快照数据形状，纯展示对齐不值得）。kind 供 token 联动用。
const SECTIONS: { key: SectionKey; label: string; kind: TokenKind }[] = [
  { key: 'characters', label: '角色', kind: 'character' },
  { key: 'scenes', label: '场景', kind: 'scene' },
  { key: 'props', label: '物品', kind: 'prop' },
];

// 第二步「准备资产」：角色/场景/物品三分区卡片流。图片本体永远归画布节点
// （linkedNodeId），本面板只管指针与资产条目。单击卡片 → 右侧抽屉编辑（对标
// 上游）；卡片右上角 🔍 预览 + ··· 菜单（菜单紧贴按钮，不锚整卡）。
export function AssetsPanel({
  nodeId,
  onClose,
  readOnly = false,
}: {
  nodeId: string;
  onClose: () => void;
  readOnly?: boolean;
}) {
  const { settings, patch } = useScriptSettings(nodeId);
  const nodes = useCanvasStore((s) => s.nodes);
  const selectNode = useCanvasStore((s) => s.selectNode);
  const [preview, setPreview] = useState<{ url: string; title: string } | null>(null);

  const [openMenuId, setOpenMenuId] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ section: SectionKey; assetId: string } | null>(null);
  const [adding, setAdding] = useState<{ section: SectionKey; replaceAssetId?: string; initialTab?: 'ai' } | null>(null);

  const fileInputRef = useRef<HTMLInputElement>(null);
  const uploadTargetRef = useRef<{ section: SectionKey; assetId: string } | null>(null);

  const allAssets = [...settings.assets.characters, ...settings.assets.scenes, ...settings.assets.props];

  const updateAsset = (section: SectionKey, assetId: string, p: Partial<ScriptAsset>) => {
    patch({
      assets: {
        ...settings.assets,
        [section]: settings.assets[section].map((a) => (a.id === assetId ? { ...a, ...p } : a)),
      },
    });
  };

  // 删除资产只删条目：行 prompt 里的 token 不动——变成断链红点由用户处理，
  // 悄悄改用户的 prompt 文本比留一个可见的断链更吓人。
  const removeAsset = (section: SectionKey, assetId: string) => {
    patch({
      assets: {
        ...settings.assets,
        [section]: settings.assets[section].filter((a) => a.id !== assetId),
      },
    });
  };

  // 名称保存（抽屉失焦调用）：唯一校验 + token 全表联动。返回错误文案或 null。
  const saveAssetName = (section: SectionKey, assetId: string, rawName: string): string | null => {
    const name = rawName.trim();
    if (!name) return '名称不能为空';
    if (name.includes('[') || name.includes(']')) return '名称不能包含方括号';
    if (settings.assets[section].some((a) => a.name === name && a.id !== assetId)) return '同类型下已有同名资产';
    const prev = settings.assets[section].find((a) => a.id === assetId);
    if (!prev || prev.name === name) return null;
    const kind = SECTIONS.find((x) => x.key === section)!.kind;
    const nextAssets: ScriptAssets = {
      ...settings.assets,
      [section]: settings.assets[section].map((a) => (a.id === assetId ? { ...a, name } : a)),
    };
    if (prev.name !== '') {
      // 改名全表联动：名称即 token 外键，资产与行必须同一次 patch 写出。
      const nextRows = settings.rows.map((r) => ({
        ...r,
        prompt: renameTokenInPrompt(r.prompt, kind, prev.name, name),
      }));
      patch({ assets: nextAssets, rows: nextRows });
    } else {
      patch({ assets: nextAssets });
    }
    return null;
  };

  const saveAssetDesc = (section: SectionKey, assetId: string, desc: string) => {
    updateAsset(section, assetId, { description: desc });
  };

  const triggerUpload = (section: SectionKey, assetId: string) => {
    uploadTargetRef.current = { section, assetId };
    fileInputRef.current?.click();
  };

  const handleFilePicked = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    const target = uploadTargetRef.current;
    uploadTargetRef.current = null;
    if (!file || !target) return;
    const idx = Math.max(0, allAssets.findIndex((a) => a.id === target.assetId));
    const newId = createUploadAssetNode(nodeId, idx);
    if (!newId) return;
    updateAsset(target.section, target.assetId, { linkedNodeId: newId });
    await uploadFileIntoNode(newId, file);
  };

  const jumpTo = (linkedNodeId: string) => {
    if (!linkedNodeId) return;
    selectNode(linkedNodeId);
    onClose();
  };

  // 除指定资产外，还有多少资产引用着同一个图片节点（三类合并计数）。
  // 「从画布选择」允许多个资产共享同一图节点，解绑连线前必须确认没人再用。
  const otherRefCount = (imageNodeId: string, exceptAssetId: string): number =>
    allAssets.filter((a) => a.id !== exceptAssetId && a.linkedNodeId === imageNodeId).length;

  const detachOldLink = (asset: ScriptAsset) => {
    if (!asset.linkedNodeId) return;
    detachAssetNodeLink(nodeId, asset.linkedNodeId, otherRefCount(asset.linkedNodeId, asset.id) > 0);
  };

  // 卡片 ··· 菜单与抽屉内 ··· 菜单共用同一份动作处理。换图类动作（替换/AI生/清除/
  // 删除）都要先摘旧连线——不摘的话画布上旧图还挂着一条到脚本节点的死边。
  const doMenuAction = (action: AssetMenuAction, section: SectionKey, asset: ScriptAsset) => {
    if (action === 'replace') setAdding({ section, replaceAssetId: asset.id }); // 摘旧线在 onBind 成功换绑那一刻做
    else if (action === 'gen') setAdding({ section, replaceAssetId: asset.id, initialTab: 'ai' }); // 弹「选择图片」定位 AI生成 tab,预填描述词;摘旧线在 onBind 换绑时做
    else if (action === 'jump') jumpTo(asset.linkedNodeId);
    else if (action === 'clear') { detachOldLink(asset); updateAsset(section, asset.id, { linkedNodeId: '' }); }
    else if (action === 'remove') {
      detachOldLink(asset);
      removeAsset(section, asset.id);
      if (editing?.assetId === asset.id) setEditing(null);
    }
  };

  const missing = SECTIONS.map(({ key, label }) => ({
    label,
    count: settings.assets[key].filter(
      (a) => !a.linkedNodeId || !nodes.some((n) => n.id === a.linkedNodeId),
    ).length,
  })).filter((m) => m.count > 0);
  const totalAssets = allAssets.length;

  const editingAsset = editing
    ? settings.assets[editing.section].find((a) => a.id === editing.assetId)
    : undefined;
  const editingMeta = editing ? SECTIONS.find((x) => x.key === editing.section)! : undefined;

  return (
    <div data-testid="script-assets-panel" className="relative flex h-full flex-col">
      <input ref={fileInputRef} type="file" accept="image/*" className="hidden" onChange={handleFilePicked} />

      <div className="min-h-0 flex-1 overflow-y-auto p-5">
        {settings.globalStyle && (
          <div className="mb-4 rounded-lg border border-white/10 bg-white/[0.03] px-3 py-2 text-sm text-muted-foreground">
            <span className="mr-1.5 rounded bg-white/10 px-1.5 py-0.5 text-xs text-foreground">全局风格</span>
            {settings.globalStyle}
          </div>
        )}

        {SECTIONS.map(({ key, label }) => {
          // 角色设定图多为四视图横图，用横向大卡；场景/物品设定图偏方，走方卡。
          const horizontal = key === 'characters';
          const cardW = horizontal ? 'w-[300px]' : 'w-44';
          const thumbH = horizontal ? 'h-[168px]' : 'h-44';
          return (
            <div key={key} className="mb-6">
              <h3 className="mb-2.5 text-sm font-medium text-foreground">{label}</h3>
              <div className="flex flex-wrap items-start gap-3">
                {settings.assets[key].map((asset) => {
                  const linkedNode = asset.linkedNodeId ? nodes.find((n) => n.id === asset.linkedNodeId) : undefined;
                  const thumb = linkedNode?.content;
                  // 绑定的 gen-image 还没出图时把节点状态透到卡上——「AI 生X」确认后
                  // 若仍显示空态,用户不知道操作有没有成功(线上反馈)。
                  const generating = !thumb && linkedNode?.status === 'generating';
                  const genFailed = !thumb && linkedNode?.status === 'failed';
                  return (
                    <div key={asset.id} className={`relative ${cardW}`}>
                      <button
                        type="button"
                        onClick={() => setEditing({ section: key, assetId: asset.id })}
                        className={`flex ${thumbH} w-full items-center justify-center overflow-hidden rounded-xl border ${thumb ? 'border-transparent' : 'border-white/10'} bg-white/[0.03] bg-transparent p-0 cursor-pointer`}
                      >
                        {thumb ? (
                          <ThumbImg src={thumb} alt={asset.name} className="h-full w-full object-cover" />
                        ) : generating ? (
                          <span data-testid={`asset-generating-${asset.id}`} className="flex items-center gap-1.5 px-2 text-center text-xs text-muted-foreground">
                            <Loader2 size={14} className="animate-spin" />
                            AI 生成中…
                          </span>
                        ) : genFailed ? (
                          <span className="px-2 text-center text-xs text-red-400">
                            生成失败{linkedNode?.error ? `：${linkedNode.error}` : ''}
                          </span>
                        ) : (
                          <span className="px-2 text-center text-xs text-muted-foreground">生成或上传图</span>
                        )}
                      </button>
                      <div className="mt-1.5 truncate text-xs text-foreground">{asset.name || '未命名'}</div>
                      {asset.description && (
                        <div className="line-clamp-1 text-[11px] text-muted-foreground">{asset.description}</div>
                      )}

                      {/* 右上角操作组：🔍 + ···。菜单挂在本组容器内——PopoverShell 锚
                          parentElement，这样弹层紧贴按钮而不是整张卡片的左下角。 */}
                      <div className="absolute right-1.5 top-1.5 flex items-center gap-1.5">
                        {thumb && (
                          <button
                            type="button"
                            aria-label="放大预览"
                            onClick={() => setPreview({ url: thumb, title: asset.name })}
                            className="rounded-lg bg-black/60 p-1.5 text-foreground hover:bg-black/80 border-none cursor-pointer"
                          >
                            <ZoomIn size={14} />
                          </button>
                        )}
                        <button
                          type="button"
                          data-testid={`asset-menu-${asset.id}`}
                          aria-label="资产菜单"
                          disabled={readOnly}
                          onClick={() => setOpenMenuId((id) => (id === asset.id ? null : asset.id))}
                          className="rounded-lg bg-black/60 p-1.5 text-foreground hover:bg-black/80 border-none cursor-pointer disabled:cursor-not-allowed disabled:opacity-50"
                        >
                          <MoreHorizontal size={14} />
                        </button>
                        {openMenuId === asset.id && (
                          <PopoverShell onClose={() => setOpenMenuId(null)} className="w-44 !p-1.5">
                            {([
                              ['replace', '替换图片'],
                              ['gen', `AI 生${label}`],
                              ['jump', '跳转至节点'],
                              ['clear', '清除图片'],
                            ] as [AssetMenuAction, string][]).map(([action, text]) => (
                              <button
                                key={action}
                                type="button"
                                onClick={() => { setOpenMenuId(null); doMenuAction(action, key, asset); }}
                                className="w-full rounded-lg bg-transparent border-none cursor-pointer px-3 py-2 text-left text-sm text-foreground hover:bg-white/5"
                              >
                                {text}
                              </button>
                            ))}
                            <button
                              type="button"
                              onClick={() => { setOpenMenuId(null); doMenuAction('remove', key, asset); }}
                              className="mt-1 w-full rounded-lg bg-transparent border-t border-x-0 border-b-0 border-white/10 cursor-pointer px-3 py-2 text-left text-sm text-red-400 hover:bg-white/5"
                            >
                              删除
                            </button>
                          </PopoverShell>
                        )}
                      </div>
                    </div>
                  );
                })}
                <button
                  type="button"
                  data-testid={`asset-add-${key}`}
                  disabled={readOnly}
                  onClick={() => { if (!readOnly) setAdding({ section: key }); }}
                  className="flex h-44 w-44 flex-col items-center justify-center gap-1.5 rounded-xl border border-dashed border-white/20 bg-transparent text-muted-foreground hover:border-white/40 hover:text-foreground cursor-pointer disabled:cursor-not-allowed disabled:opacity-50"
                >
                  <Plus size={18} />
                  <span className="text-xs">新增</span>
                </button>
              </div>
            </div>
          );
        })}
      </div>

      <div className="flex items-center justify-between gap-4 border-t border-white/10 bg-white/[0.02] px-5 py-3">
        <span className="flex min-w-0 items-center gap-2 text-sm text-muted-foreground">
          {totalAssets === 0 ? (
            <>先在上方新增角色/场景/物品资产，分镜提示词里用 @ 引用它们</>
          ) : missing.length > 0 ? (
            <>
              <Info size={15} className="shrink-0 text-yellow-400" />
              检测到有{missing.map((m, i) => `${i > 0 ? '和' : ''} ${m.count} 个${m.label}`).join('')}没有设定图，您可以手动上传或创建生图节点
            </>
          ) : (
            <>
              <CheckCircle2 size={15} className="shrink-0 text-green-400" />
              资产已生成，如再次生成将会覆盖之前的图片/场景/物品等资产
            </>
          )}
        </span>
      </div>

      {preview && (
        <ImagePreview url={preview.url} title={preview.title} onClose={() => setPreview(null)} />
      )}

      {adding && (() => {
        const replaceAsset = adding.replaceAssetId
          ? settings.assets[adding.section].find((a) => a.id === adding.replaceAssetId)
          : undefined;
        return (
          <AddAssetDialog
            nodeId={nodeId}
            section={adding.section}
            sectionLabel={SECTIONS.find((x) => x.key === adding.section)!.label}
            replaceAsset={replaceAsset}
            initialTab={adding.initialTab}
            onBind={replaceAsset ? (newNodeId) => {
              // 换绑那一刻才摘旧连线：用户可能中途关弹窗放弃替换，提前摘会白摘。
              detachOldLink(replaceAsset);
              updateAsset(adding.section, replaceAsset.id, { linkedNodeId: newNodeId });
            } : undefined}
            onClose={() => setAdding(null)}
          />
        );
      })()}

      {editing && editingAsset && editingMeta && (
        <AssetEditDrawer
          section={editing.section}
          sectionLabel={editingMeta.label}
          asset={editingAsset}
          readOnly={readOnly}
          onSaveName={saveAssetName}
          onSaveDesc={saveAssetDesc}
          onMenuAction={doMenuAction}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  );
}
