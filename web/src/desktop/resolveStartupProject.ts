// 启动时决定打开哪个项目（spec F9）：
//   1. 上一次打开的项目（还存在的话）；
//   2. 否则最近修改的项目（列表接口按 updated_at 倒序）；
//   3. 一个项目都没有时，新建「未命名项目」。
import axios from 'axios';
import { createProject, getProject, listProjectsPage } from '@/shared/api';
import { clearLastProjectId, loadLastProjectId } from './lastProject';

export interface StartupDeps {
  getProject: typeof getProject;
  listProjectsPage: typeof listProjectsPage;
  createProject: typeof createProject;
}

const defaultDeps: StartupDeps = { getProject, listProjectsPage, createProject };

export const UNTITLED_PROJECT_NAME = '未命名项目';

function isNotFound(err: unknown): boolean {
  return axios.isAxiosError(err) && err.response?.status === 404;
}

/**
 * 返回要打开的项目 id。只有「上一次的项目已不存在（404）」会被吞掉并退到下一步；
 * 其它错误（sidecar 没起来、网络、500）原样抛出，由调用方显示错误页——不能因为
 * 一次暂时性失败就去新建一个空项目。
 */
export function resolveStartupProject(deps: StartupDeps = defaultDeps): Promise<number> {
  // 去重：开发模式下 StrictMode 会让 StartupRedirect 的 effect「挂载 → 卸载 → 挂载」，
  // 两次调用各自查到空列表、各自新建，库里就多出一个「未命名项目」。进行中的请求共用
  // 同一个 promise；结束（成功或失败）后清掉，「重试」会重新解析。
  if (!inFlight) {
    const pending = doResolve(deps);
    inFlight = pending;
    const clear = () => {
      if (inFlight === pending) inFlight = null;
    };
    pending.then(clear, clear);
  }
  return inFlight;
}

let inFlight: Promise<number> | null = null;

async function doResolve(deps: StartupDeps): Promise<number> {
  const last = loadLastProjectId();
  if (last !== null) {
    try {
      const project = await deps.getProject(last);
      return project.id;
    } catch (err) {
      if (!isNotFound(err)) throw err;
      clearLastProjectId();
    }
  }
  const { items } = await deps.listProjectsPage(1, 0);
  if (items.length > 0) return items[0].id;
  const created = await deps.createProject(UNTITLED_PROJECT_NAME, '');
  return created.id;
}
