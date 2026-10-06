import { describe, expect, it } from 'vitest';
import { gateReducer, initialGateState } from './gateState';

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
