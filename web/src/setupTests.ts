import '@testing-library/jest-dom/vitest';

// 本 jsdom 配置下 window.localStorage 方法不完整（removeItem/clear 等可能缺失），
// 补一个内存版 Storage，供用到 localStorage 的代码/测试（如项目密码解锁 24h 有效期）。
class MemStorage implements Storage {
  private m = new Map<string, string>();
  get length() { return this.m.size; }
  clear() { this.m.clear(); }
  getItem(k: string) { return this.m.has(k) ? this.m.get(k)! : null; }
  key(i: number) { return Array.from(this.m.keys())[i] ?? null; }
  removeItem(k: string) { this.m.delete(k); }
  setItem(k: string, v: string) { this.m.set(k, String(v)); }
}
Object.defineProperty(window, 'localStorage', { writable: true, value: new MemStorage() });

// antd 会用到 window.matchMedia（通过 responsiveObserver / useBreakpoint）
// jsdom 没有实现它，所以这里 stub 一个出来。
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
});

// rc-drawer/rc-util 会以第二个 pseudoElt 参数调用 getComputedStyle；
// jsdom 对这个参数未实现，会打印 Not implemented 噪声。测试里忽略 pseudoElt 即可。
const originalGetComputedStyle = window.getComputedStyle.bind(window);
Object.defineProperty(window, 'getComputedStyle', {
  writable: true,
  value: (elt: Element) => originalGetComputedStyle(elt),
});
