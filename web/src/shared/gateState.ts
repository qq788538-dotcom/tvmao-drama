// SidecarGate 的纯状态机。epoch 在每次收到状态事件时 +1；
// 异步取回的 info / error 带着发起时的 epoch，过期的结果直接丢弃（防止重启后用到旧端口）。
import type { ServerInfo, SidecarStatus } from './server';

export type GateView =
  | { kind: 'starting' }
  | { kind: 'ready'; info: ServerInfo }
  | { kind: 'failed'; reason: string };

export interface GateState {
  epoch: number;
  view: GateView;
}

export type GateEvent =
  | { type: 'status'; status: SidecarStatus }
  | { type: 'info'; epoch: number; info: ServerInfo }
  | { type: 'error'; epoch: number; reason: string };

export const initialGateState: GateState = { epoch: 0, view: { kind: 'starting' } };

export function gateReducer(s: GateState, e: GateEvent): GateState {
  switch (e.type) {
    case 'status':
      if (e.status.status === 'failed') {
        return { epoch: s.epoch + 1, view: { kind: 'failed', reason: e.status.reason } };
      }
      // starting 和 ready 都先进入“启动中”：ready 事件不带 token，由组件随后调用 getServerInfo 取回
      return { epoch: s.epoch + 1, view: { kind: 'starting' } };
    case 'info':
      return e.epoch === s.epoch ? { epoch: s.epoch, view: { kind: 'ready', info: e.info } } : s;
    case 'error':
      return e.epoch === s.epoch ? { epoch: s.epoch, view: { kind: 'failed', reason: e.reason } } : s;
  }
}
