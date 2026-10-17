// SidecarGate 的纯状态机。epoch 在每次收到状态事件时 +1；
// 异步取回的 info / error 带着发起时的 epoch，过期的结果直接丢弃（防止重启后用到旧端口）。
import type { DataDirInfo, ServerInfo, SidecarStatus } from './server';

export type GateView =
  | { kind: 'starting'; migrating?: boolean; waitingLock?: boolean }
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
      if (e.status.status === 'migrating') {
        const view: GateView = e.status.waiting
          ? { kind: 'starting', migrating: true, waitingLock: true }
          : { kind: 'starting', migrating: true };
        return { epoch: s.epoch + 1, view };
      }
      // starting 和 ready 都先进入“启动中”：ready 事件不带 token，由组件随后调用 getServerInfo 取回
      return { epoch: s.epoch + 1, view: { kind: 'starting' } };
    case 'info':
      return e.epoch === s.epoch ? { epoch: s.epoch, view: { kind: 'ready', info: e.info } } : s;
    case 'error':
      return e.epoch === s.epoch ? { epoch: s.epoch, view: { kind: 'failed', reason: e.reason } } : s;
  }
}

/** “启动中”页面的文案：普通启动 / 迁移数据目录 / 等上一次迁移结束。 */
export function startingMessage(view: GateView): string {
  if (view.kind !== 'starting') return '';
  if (view.waitingLock) {
    return '正在等待上一次数据目录迁移结束，请勿关闭应用…（最多约 10 分钟；超时后本次先用原目录，下次启动自动重试）';
  }
  if (view.migrating) return '正在迁移数据目录，请勿关闭应用…（数据多时需要几分钟）';
  return '正在启动本地服务…';
}

/** 失败原因是“上一个窗口仍在关闭、数据目录被占用”（I4，壳 sidecar.rs 的 DATA_DIR_BUSY_MSG）：稍后重试即可。 */
export function isDataDirBusy(reason: string): boolean {
  return reason.includes('上一个窗口仍在关闭');
}

/** get_data_dir 的结果：成功带 info；失败（location.json 损坏等）只记失败。 */
export type DataDirProbe = { ok: true; info: DataDirInfo } | { ok: false };

/**
 * 失败页是否提供“改用默认数据目录”：正在用自定义目录（外接盘没插、没有写权限……），
 * 或者连数据目录配置都读不出来。已经是默认目录时不提供。
 */
export function offerDefaultDataDir(p: DataDirProbe): boolean {
  return !p.ok || p.info.custom;
}
