import { useState } from 'react';
import { Lock, X } from 'lucide-react';
import { changePassword } from '@/shared/api';

interface Props {
  open: boolean;
  onClose: () => void;
}

export function ChangePasswordModal({ open, onClose }: Props) {
  const [oldPwd, setOldPwd] = useState('');
  const [newPwd, setNewPwd] = useState('');
  const [confirmPwd, setConfirmPwd] = useState('');
  const [error, setError] = useState('');
  const [success, setSuccess] = useState(false);
  const [loading, setLoading] = useState(false);

  if (!open) return null;

  const handleSubmit = async () => {
    setError('');
    if (!oldPwd) { setError('请输入旧密码'); return; }
    if (newPwd.length < 6) { setError('新密码至少 6 位'); return; }
    if (newPwd !== confirmPwd) { setError('两次输入的新密码不一致'); return; }
    setLoading(true);
    try {
      await changePassword(oldPwd, newPwd);
      setSuccess(true);
      setTimeout(() => {
        onClose();
        setOldPwd(''); setNewPwd(''); setConfirmPwd('');
        setSuccess(false);
      }, 1500);
    } catch (err: any) {
      setError(err?.response?.data?.error || err?.message || '修改失败');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 z-[200] flex items-center justify-center bg-black/60 backdrop-blur-sm">
      <div className="w-[360px] bg-card border border-input rounded-xl shadow-2xl">
        {/* 头部 */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <div className="flex items-center gap-2 text-foreground text-sm font-semibold">
            <Lock size={16} className="text-blue-400" />
            修改密码
          </div>
          <button
            onClick={() => { onClose(); setError(''); setOldPwd(''); setNewPwd(''); setConfirmPwd(''); setSuccess(false); }}
            className="p-1 rounded bg-transparent border-none cursor-pointer text-muted-foreground hover:text-foreground hover:bg-popover transition-colors"
          >
            <X size={16} />
          </button>
        </div>

        {/* 主体 */}
        <div className="px-5 py-4 flex flex-col gap-3">
          {success ? (
            <div className="text-center text-emerald-400 text-sm py-6">密码修改成功 ✓</div>
          ) : (
            <>
              <div className="flex flex-col gap-1">
                <label className="text-[11px] text-sidebar-foreground">旧密码</label>
                <input
                  type="password"
                  value={oldPwd}
                  onChange={(e) => setOldPwd(e.target.value)}
                  className="w-full bg-card border border-input rounded-lg px-3 py-2 text-sm text-foreground outline-none focus:border-blue-500 placeholder:text-muted-foreground"
                  placeholder="请输入当前密码"
                />
              </div>
              <div className="flex flex-col gap-1">
                <label className="text-[11px] text-sidebar-foreground">新密码</label>
                <input
                  type="password"
                  value={newPwd}
                  onChange={(e) => setNewPwd(e.target.value)}
                  className="w-full bg-card border border-input rounded-lg px-3 py-2 text-sm text-foreground outline-none focus:border-blue-500 placeholder:text-muted-foreground"
                  placeholder="至少 6 位"
                />
              </div>
              <div className="flex flex-col gap-1">
                <label className="text-[11px] text-sidebar-foreground">确认新密码</label>
                <input
                  type="password"
                  value={confirmPwd}
                  onChange={(e) => setConfirmPwd(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter') handleSubmit(); }}
                  className="w-full bg-card border border-input rounded-lg px-3 py-2 text-sm text-foreground outline-none focus:border-blue-500 placeholder:text-muted-foreground"
                  placeholder="再次输入新密码"
                />
              </div>
              {error && (
                <div className="text-[11px] text-red-400 bg-red-950/40 border border-red-800/50 rounded px-3 py-1.5">
                  {error}
                </div>
              )}
              <button
                onClick={handleSubmit}
                disabled={loading}
                className="w-full py-2 mt-1 rounded-lg text-sm font-medium bg-blue-600 hover:bg-blue-500 text-white border-none cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {loading ? '提交中...' : '确认修改'}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
