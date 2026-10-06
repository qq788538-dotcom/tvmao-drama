// apps/web/src/lib/permissions.ts
//
// 针对运维专用 UI（画布 → 工作流 JSON 导出等）的超管权限门控。
// 特权用户 id 在构建时从 VITE_SUPER_ADMIN_USER_ID 读取（默认 1）。
// 放进 env 既能让这个常量不进源码版本库，也能让 staging / prod
// 指向不同的账号。

import { useQuery } from '@tanstack/react-query';
import { AuthUser, getToken, me } from './api';

// useCurrentUser 返回已登录用户。共享 react-query 缓存，让每个调用方
// 都命中同一份数据——避免应用各挂载点（画布、面板、路由守卫）之间
// 重复发 /me 请求。
export function useCurrentUser() {
  return useQuery<AuthUser | null>({
    queryKey: ['me'],
    queryFn: async () => (getToken() ? me() : null),
    staleTime: 5 * 60 * 1000,
  });
}

// useIsSuperAdmin 门控运维专用 UI（可见性 + 操作）。在 /me 返回之前
// 都是 false，这样匿名渲染时不会一闪而过地露出管理界面。
export function useIsSuperAdmin(): boolean {
  const { data } = useCurrentUser();
  if (!data) return false;
  const raw = import.meta.env.VITE_SUPER_ADMIN_USER_ID;
  const superAdminId = raw == null || raw === '' ? 1 : Number(raw);
  if (!Number.isFinite(superAdminId)) return false;
  return data.id === superAdminId;
}
