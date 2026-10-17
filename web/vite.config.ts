import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';

export default defineConfig({
  // 3D 导演台（modules/canvas/editor/nodes/director/desk）的模型库 import.meta.glob
  // 需要把 fbx/obj 当资源模块处理。
  assetsInclude: ['**/*.fbx', '**/*.obj'],
  plugins: [react()],
  // 资源用站点根的绝对路径：画布路由是 /canvas/:id 这样的多级路径，相对路径 './'
  // 在深层路由下刷新时会把 ./assets、./models 解析成 /canvas/assets… 而 404。
  // Tauri 的 tauri://localhost 与 http://tauri.localhost 都以站点根提供 dist，
  // 找不到的路径回落到 index.html，所以绝对路径 + BrowserRouter 可用。
  base: '/',
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server: { port: 5173, strictPort: true },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/setupTests.ts',
  },
});
