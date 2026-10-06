import { AxiosError, AxiosHeaders, type InternalAxiosRequestConfig } from 'axios';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Project } from '@/shared/api';
import { loadLastProjectId, saveLastProjectId } from './lastProject';
import { resolveStartupProject, UNTITLED_PROJECT_NAME, type StartupDeps } from './resolveStartupProject';

function project(id: number): Project {
  return { id, userId: 1, name: `p${id}`, description: '', createdAt: '', updatedAt: '' };
}

function httpError(status: number): AxiosError {
  const config = { headers: new AxiosHeaders() } as InternalAxiosRequestConfig;
  return new AxiosError(`status ${status}`, String(status), config, undefined, {
    status,
    statusText: '',
    headers: {},
    config,
    data: {},
  });
}

function makeDeps(over: Partial<StartupDeps> = {}) {
  return {
    getProject: vi.fn(async (id: number) => project(id)),
    listProjectsPage: vi.fn(async () => ({ items: [] as Project[], total: 0 })),
    createProject: vi.fn(async () => project(99)),
    ...over,
  };
}

describe('resolveStartupProject', () => {
  beforeEach(() => localStorage.clear());

  it('上一次打开的项目还在：直接打开它，不查列表', async () => {
    saveLastProjectId(7);
    const deps = makeDeps();

    expect(await resolveStartupProject(deps)).toBe(7);
    expect(deps.getProject).toHaveBeenCalledWith(7);
    expect(deps.listProjectsPage).not.toHaveBeenCalled();
  });

  it('上一次的项目已删除（404）：清掉记录，打开最近修改的项目', async () => {
    saveLastProjectId(7);
    const deps = makeDeps({
      getProject: vi.fn(async () => {
        throw httpError(404);
      }),
      listProjectsPage: vi.fn(async () => ({ items: [project(12), project(3)], total: 2 })),
    });

    expect(await resolveStartupProject(deps)).toBe(12);
    expect(deps.listProjectsPage).toHaveBeenCalledWith(1, 0);
    expect(loadLastProjectId()).toBeNull();
  });

  it('没有记录：打开最近修改的项目（列表第一项）', async () => {
    const deps = makeDeps({
      listProjectsPage: vi.fn(async () => ({ items: [project(5)], total: 9 })),
    });

    expect(await resolveStartupProject(deps)).toBe(5);
    expect(deps.getProject).not.toHaveBeenCalled();
    expect(deps.createProject).not.toHaveBeenCalled();
  });

  it('一个项目都没有：新建「未命名项目」并打开', async () => {
    const deps = makeDeps();

    expect(await resolveStartupProject(deps)).toBe(99);
    expect(deps.createProject).toHaveBeenCalledWith(UNTITLED_PROJECT_NAME, '');
    expect(UNTITLED_PROJECT_NAME).toBe('未命名项目');
  });

  it('查上一次的项目遇到非 404 错误（sidecar 故障）：原样抛出，不新建项目', async () => {
    saveLastProjectId(7);
    const deps = makeDeps({
      getProject: vi.fn(async () => {
        throw httpError(500);
      }),
    });

    await expect(resolveStartupProject(deps)).rejects.toBeInstanceOf(AxiosError);
    expect(deps.createProject).not.toHaveBeenCalled();
    expect(loadLastProjectId()).toBe(7);
  });

  it('列表接口失败：原样抛出，不新建项目', async () => {
    const deps = makeDeps({
      listProjectsPage: vi.fn(async () => {
        throw httpError(404);
      }),
    });

    await expect(resolveStartupProject(deps)).rejects.toBeInstanceOf(AxiosError);
    expect(deps.createProject).not.toHaveBeenCalled();
  });
});
