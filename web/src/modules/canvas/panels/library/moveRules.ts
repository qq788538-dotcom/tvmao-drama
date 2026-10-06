import type { LibraryFolder } from './types';

// 素材库「移动到」的可选性规则。抽成纯函数是为了能单测——这些规则必须和后端
// library_folder_handlers.go 的守卫保持一致，前端只是提前置灰，后端才是权威。

export function byIdMap(folders: LibraryFolder[]): Map<number, LibraryFolder> {
  return new Map(folders.map((f) => [f.id, f]));
}

export function isSelfOrDescendant(
  f: LibraryFolder,
  ancestorId: number,
  byId: Map<number, LibraryFolder>,
): boolean {
  let cur: LibraryFolder | undefined = f;
  while (cur) {
    if (cur.id === ancestorId) return true;
    cur = cur.parentId != null ? byId.get(cur.parentId) : undefined;
  }
  return false;
}




