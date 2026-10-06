import type { ReactNode } from 'react';
import { Popover } from 'antd';
import { Globe, Link2 } from 'lucide-react';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** 「发布作品」——投稿到作品广场。弹框内容是 Task 6 的范围，这里只出回调。 */
  onPickSubmit: () => void;
  /** 「分享链接」——生成/复用 unlisted 分享链接，走精简后的 PublishWorkflowModal。 */
  onPickShare: () => void;
  /** 触发浮层的元素，通常是顶栏的分享图标按钮。 */
  children: ReactNode;
}

interface OptionProps {
  icon: ReactNode;
  title: string;
  desc: string;
  onClick: () => void;
}

function Option({ icon, title, desc, onClick }: OptionProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: 10,
        width: '100%',
        padding: '10px 12px',
        background: 'transparent',
        border: 'none',
        borderRadius: 8,
        cursor: 'pointer',
        textAlign: 'left',
        color: 'inherit',
      }}
      onMouseEnter={(e) => { e.currentTarget.style.background = 'rgba(255,255,255,0.06)'; }}
      onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent'; }}
    >
      <span style={{ marginTop: 2, flexShrink: 0 }}>{icon}</span>
      <span style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        <span style={{ fontSize: 13, fontWeight: 600 }}>{title}</span>
        <span style={{ fontSize: 12, opacity: 0.65, lineHeight: 1.4 }}>{desc}</span>
      </span>
    </button>
  );
}

// 顶栏「发布与分享」双出口：发布作品(投稿广场，Task 6 落地) / 分享链接
// (unlisted 可分享链接，复用既有 publishWorkflow)。两条路径此前挤在同一个
// 「发布」按钮 + 单一弹框里，语义上其实是两件不同的事——发布是给别人看，
// 分享链接只是给指定的人一个可打开的入口，产品拍板拆成两个出口。
export function PublishSharePopover({ open, onOpenChange, onPickSubmit, onPickShare, children }: Props) {
  const content = (
    <div style={{ width: 260, padding: 4 }}>
      <Option
        icon={<Globe size={18} />}
        title="发布作品"
        desc="发布你的作品和创作过程，让更多创作者看到。"
        onClick={onPickSubmit}
      />
      <Option
        icon={<Link2 size={18} />}
        title="分享链接"
        desc="拥有此链接的人可以查看并复制你的画布。"
        onClick={onPickShare}
      />
    </div>
  );

  return (
    <Popover
      open={open}
      onOpenChange={onOpenChange}
      trigger="click"
      placement="bottomRight"
      content={content}
      styles={{ body: { padding: 4 } }}
    >
      {children}
    </Popover>
  );
}
