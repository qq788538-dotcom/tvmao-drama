// 在线成员头像条（团队画布）。名册由服务端在每次连接进出时全量下发
// （canvas_presence 帧），本组件只负责渲染当前这份。
//
// 只有头像、没有光标：拍板的范围就是「谁在这张画布上」，跨端光标位置需要另一
// 套高频瞬态流（每次 mousemove 都要广播），投入产出比不成立。
import { Tooltip } from 'antd';
import { memberLabel, useCanvasPresenceStore } from './presenceStore';

// 用邮箱算一个稳定色相当占位头像色——同一个人每次刷新、在每个人的屏幕上都是
// 同一个颜色，不需要存头像也不需要额外请求。同 首页 作者头像的做法。
function avatarHue(seed: string): number {
  let hash = 0;
  for (let i = 0; i < seed.length; i += 1) {
    hash = (hash * 31 + seed.charCodeAt(i)) % 360;
  }
  return hash < 0 ? hash + 360 : hash;
}

function initialOf(email: string): string {
  const trimmed = email.trim();
  return trimmed ? trimmed[0].toUpperCase() : '?';
}

/**
 * @param selfEmail 当前操作者的**真人**邮箱。团队身份下 /me 返回的是影子用户
 *   （team-xxx@team.internal），只有 /auth/contexts 的 personalEmail 是真人的，
 *   所以这个值由页面传进来而不是本组件自己去查——查错了就会把自己标成别人。
 */
export function PresenceBar({ selfEmail }: { selfEmail?: string }) {
  const members = useCanvasPresenceStore((s) => s.members);
  // 只有自己一个人在线时不渲染：一条只有你自己的头像条纯粹是噪音，还占着
  // 顶栏下方那条本来就紧张的横向空间。
  if (members.length <= 1) return null;

  const self = (selfEmail ?? '').trim().toLowerCase();

  return (
    <div
      data-presence-bar
      className="absolute top-16 right-4 z-40 flex items-center gap-2 px-3 py-1.5 rounded-xl shadow-xl shadow-black/40"
      style={{
        backgroundColor: 'rgba(28, 28, 28, 0.95)',
        borderWidth: 1,
        borderStyle: 'solid',
        borderColor: 'rgba(255, 255, 255, 0.10)',
        backdropFilter: 'blur(24px)',
        WebkitFontSmoothing: 'antialiased',
      }}
    >
      {members.map((member) => {
        const label = memberLabel(member);
        const isSelf = !!self && member.email.trim().toLowerCase() === self;
        const hue = avatarHue(member.email || String(member.actorUserId));
        return (
          <Tooltip key={member.actorUserId} title={isSelf ? `${label}（我）` : label} placement="bottom">
            <div
              data-presence-member={member.actorUserId}
              className="w-7 h-7 rounded-full flex items-center justify-center text-[11px] font-semibold text-white select-none"
              style={{
                backgroundColor: `hsl(${hue}, 55%, 42%)`,
                // 自己那个圈描一道亮边：一屏头像里第一眼要能找到「我在哪」。
                boxShadow: isSelf ? '0 0 0 2px rgba(255, 255, 255, 0.85)' : 'none',
              }}
            >
              {isSelf ? '我' : initialOf(member.email)}
            </div>
          </Tooltip>
        );
      })}
    </div>
  );
}
