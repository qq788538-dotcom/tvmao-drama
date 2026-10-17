import { describe, expect, it } from 'vitest';
import { gateReducer, initialGateState, isDataDirBusy, offerDefaultDataDir, startingMessage } from './gateState';

const info = { port: 4001, token: 't' };

describe('gateReducer', () => {
  it('初始为启动中，epoch 0', () => {
    expect(initialGateState).toEqual({ epoch: 0, view: { kind: 'starting' } });
  });

  it('同一 epoch 的 info 进入就绪', () => {
    expect(gateReducer(initialGateState, { type: 'info', epoch: 0, info })).toEqual({
      epoch: 0,
      view: { kind: 'ready', info },
    });
  });

  it('过期 epoch 的 info / error 被忽略', () => {
    const s = gateReducer(initialGateState, { type: 'status', status: { status: 'starting' } });
    expect(s.epoch).toBe(1);
    expect(gateReducer(s, { type: 'info', epoch: 0, info })).toBe(s);
    expect(gateReducer(s, { type: 'error', epoch: 0, reason: 'x' })).toBe(s);
  });

  it('failed 事件进入失败页', () => {
    const s = gateReducer(initialGateState, { type: 'status', status: { status: 'failed', reason: '崩了' } });
    expect(s).toEqual({ epoch: 1, view: { kind: 'failed', reason: '崩了' } });
  });

  it('ready 事件不带 token：回到启动中，等组件取到 info', () => {
    const ready = gateReducer(initialGateState, { type: 'info', epoch: 0, info });
    const s = gateReducer(ready, { type: 'status', status: { status: 'ready' } });
    expect(s).toEqual({ epoch: 1, view: { kind: 'starting' } });
  });

  it('同一 epoch 的 error 进入失败页', () => {
    expect(gateReducer(initialGateState, { type: 'error', epoch: 0, reason: '启动超时' })).toEqual({
      epoch: 0,
      view: { kind: 'failed', reason: '启动超时' },
    });
  });
});

// 失败页上的“改用默认数据目录”：只在数据目录可能是原因时出现。
describe('offerDefaultDataDir', () => {
  it('正在用自定义目录时提供', () => {
    expect(offerDefaultDataDir({ ok: true, info: { current: '/Volumes/x', default: '/d', custom: true } })).toBe(true);
  });
  it('用的就是默认目录时不提供（切过去没有意义）', () => {
    expect(offerDefaultDataDir({ ok: true, info: { current: '/d', default: '/d', custom: false } })).toBe(false);
  });
  it('读不出数据目录配置（location.json 损坏）时提供', () => {
    expect(offerDefaultDataDir({ ok: false })).toBe(true);
  });
});

describe('gateReducer migrating', () => {
  it('migrating 进入启动中并标记迁移，随后的 info 照常就绪', () => {
    const s1 = gateReducer(initialGateState, { type: 'status', status: { status: 'migrating' } });
    expect(s1.view).toEqual({ kind: 'starting', migrating: true });
    const s2 = gateReducer(s1, { type: 'info', epoch: s1.epoch, info: { port: 1, token: 't' } });
    expect(s2.view).toEqual({ kind: 'ready', info: { port: 1, token: 't' } });
  });
});

// 等上一次迁移的锁时与普通迁移区分开
describe('gateReducer migrating waiting', () => {
  it('waiting=true 标记等待；随后 waiting=false 回到迁移中', () => {
    const s1 = gateReducer(initialGateState, { type: 'status', status: { status: 'migrating', waiting: true } });
    expect(s1.view).toEqual({ kind: 'starting', migrating: true, waitingLock: true });
    expect(startingMessage(s1.view)).toContain('正在等待上一次数据目录迁移结束');
    const s2 = gateReducer(s1, { type: 'status', status: { status: 'migrating', waiting: false } });
    expect(s2.view).toEqual({ kind: 'starting', migrating: true });
    expect(startingMessage(s2.view)).toContain('正在迁移数据目录');
    expect(startingMessage({ kind: 'starting' })).toBe('正在启动本地服务…');
  });
});

// 上一个窗口仍在关闭（数据目录被占用）→ 只需稍后重试，不提供“改用默认数据目录”
describe('isDataDirBusy', () => {
  it('识别壳给出的“上一个窗口仍在关闭”原因', () => {
    expect(isDataDirBusy('本地服务启动失败：上一个窗口仍在关闭，数据目录暂时被它占用。请稍后点“重试”。')).toBe(true);
    expect(isDataDirBusy('本地服务启动失败：sidecar 未就绪即退出')).toBe(false);
  });
});
