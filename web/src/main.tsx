// 字体随应用打包（spec E7）：不再从 Google Fonts 在线加载。
import '@fontsource/inter/400.css';
import '@fontsource/inter/500.css';
import '@fontsource/inter/600.css';
import '@fontsource/inter/700.css';
import '@fontsource/jetbrains-mono/400.css';
import '@fontsource/jetbrains-mono/500.css';
import 'antd/dist/reset.css';
import './styles/canvas.css';
import dayjs from 'dayjs';
import 'dayjs/locale/zh-cn';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { installGestureGuards } from '@/shared/gestureGuard';
import { startApp } from '@/desktop/startApp';

// AntD 的 ConfigProvider locale 只汉化组件自身文案；DatePicker 的星期 / 月份名来自
// dayjs，需单独把 dayjs 全局 locale 设成中文（与参考仓 main.tsx 一致）。
dayjs.locale('zh-cn');

// 以下三项原样沿用参考仓 main.tsx：测试钩子、清理过期的抽帧缓存、拦截触控板手势。
(window as any).__CANVAS_STORE_FOR_TEST__ = useCanvasStore.getState();
import('@/shared/frameCache').then((m) => m.purgeExpired()).catch(() => {});
installGestureGuards();

void startApp(document.getElementById('root')!);
