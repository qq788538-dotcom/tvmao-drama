import { useEffect } from 'react';
import { beginInteraction, endInteraction } from '../interactionRegistry';
import { sendNodeEditingTransient } from '../transientChannel';

/**
 * 「正在编辑」的续帧间隔。必须**明显小于**收端的 NODE_EDITING_TTL_MS（5s），
 * 否则一次网络抖动丢掉一帧，队友那边的角标就会闪一下再回来。3s 留了一帧的余量。
 */
export const NODE_EDITING_HEARTBEAT_MS = 3_000;

/**
 * 把「本端正在编辑某节点」这件事广播出去，并在本地登记为在途交互。
 *
 * 两件事绑在同一个 effect 里是有意的——它们是同一段生命周期的两半，各写一处
 * 迟早会漏配对：
 *   - **广播**：进入编辑发 editing:true + 每 3s 续帧（收端靠 TTL 判活，见
 *     presenceStore），退出/卸载发 editing:false。
 *   - **在途登记**：编辑期间队友对这个节点的远端 patch 必须挂起到编辑结束
 *     （见 interactionRegistry / store 的 deferredRemoteOps），不然用户正打着字
 *     光标处的文本就被换掉了。**endInteraction 漏调 = 挂起的远端更新永久卡住**，
 *     那个节点从此再也收不到队友的改动，还一声不响，所以 cleanup 里必须成对收尾
 *     （卸载路径同样走 cleanup，不依赖 blur 事件一定发生）。
 *
 * 通道没注册（个人项目）时 sendNodeEditingTransient 是 no-op，本 hook 只剩本地
 * 登记这一半——个人项目一帧都不发。
 */
export function useNodeEditingBroadcast(nodeId: string, editing: boolean): void {
  useEffect(() => {
    if (!editing || !nodeId) return;
    beginInteraction(nodeId, 'text');
    sendNodeEditingTransient(nodeId, true);
    const timer = window.setInterval(() => {
      sendNodeEditingTransient(nodeId, true);
    }, NODE_EDITING_HEARTBEAT_MS);
    return () => {
      window.clearInterval(timer);
      sendNodeEditingTransient(nodeId, false);
      endInteraction(nodeId, 'text');
    };
  }, [nodeId, editing]);
}
