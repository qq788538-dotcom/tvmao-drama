// 输出 tauri.conf.json 里 bundle.resources 的 {源: 目标} 映射，每行 "源\t目标"。
// Windows zip 用 --no-bundle 构建，Tauri 不会复制资源；package-windows.sh 按这里的输出手动复制到 exe 同目录
// （Windows 未打包时 Tauri 的 resource_dir() 就是 exe 所在目录）。
// 用法：node scripts/lib/list-resources.mjs src-tauri/tauri.conf.json
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export function listResources(conf) {
  const r = conf?.bundle?.resources;
  if (r == null) return [];
  if (Array.isArray(r)) {
    throw new Error('bundle.resources 请用 {"源路径": "目标路径"} 映射形式：Windows zip 打包需要明确的目标路径');
  }
  return Object.entries(r).map(([src, dest]) => {
    if (src.includes('*')) throw new Error(`bundle.resources 的源路径不支持通配符：${src}`);
    if (
      typeof dest !== 'string' ||
      dest === '' ||
      dest.startsWith('/') ||
      /^[A-Za-z]:/.test(dest) ||
      dest.split(/[\\/]/).includes('..')
    ) {
      throw new Error(`bundle.resources 目标路径无效：${src} → ${dest}`);
    }
    return [src, dest];
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const conf = JSON.parse(readFileSync(process.argv[2], 'utf8'));
  for (const [src, dest] of listResources(conf)) console.log(`${src}\t${dest}`);
}
