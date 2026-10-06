import { describe, expect, it } from 'vitest';
import { gateReducer, initialGateState, offerDefaultDataDir } from './gateState';

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
