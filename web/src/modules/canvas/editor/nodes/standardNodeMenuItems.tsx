// 所有画布节点右键菜单的**标配两项**（对标 LibTV，用户拍板 2026-08-13）：
//   复制节点 ⌘C —— 剪贴板级复制，仅节点本身，可跨画布项目粘贴；
//   创建副本 ⌘D —— 原地副本并连着同样的上游输入（store.duplicateNodes 本就只复制入边）。
// 新写自定义节点菜单时引入这两项，别各自再造文案/快捷键；改文案只改这里。
// 曾经 useMediaNodeMenu 里叫「拷贝节点」，按 LibTV 统一改名「创建副本」。
import type { NodeMenuItem } from './NodeContextMenu';
import { useCanvasStore } from '../store';
import { copyNodeOrSelection } from '../utils/nodeClipboard';

// 与 CanvasContextMenu.shortcutLabels 同理：文案按平台切换，只影响显示，
// 真实按键处理在 CanvasEditor 的全局监听（metaKey/ctrlKey 都认）。
export function isMacLike() {
  return (
    typeof navigator !== 'undefined' &&
    /Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent)
  );
}

export function standardNodeMenuItems(nodeId: string, closeMenu: () => void): NodeMenuItem[] {
  const mac = isMacLike();
  return [
    {
      key: 'copy-node',
      label: '复制节点',
      shortcut: mac ? '⌘C' : 'Ctrl+C',
      tip: '仅复制节点本身。复制到画布剪贴板，可在本画布或其他项目里粘贴；对外部应用不可见。',
      onPick: () => {
        closeMenu();
        // 剪贴板复制（对标 libtv）：可在本画布或其他项目粘贴，外部应用不可见。
        copyNodeOrSelection(nodeId);
      },
    },
    {
      key: 'duplicate-node',
      label: '创建副本',
      shortcut: mac ? '⌘D' : 'Ctrl+D',
      tip: '复制节点及上游连线。副本落在旁边、连着同样的输入，可直接重跑同样的生成。',
      onPick: () => {
        closeMenu();
        // 走 store 的 duplicateNodes：它同时负责 commit(可撤销)、错开位置、
        // 复制上游入边、广播 upsertNode 落库。副本随即成为唯一选中项。
        useCanvasStore.getState().duplicateNodes([nodeId]);
      },
    },
  ];
}
