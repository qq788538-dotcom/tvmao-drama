import { describe, expect, it } from 'vitest';

// useMediaNodeMenu 返回的 badge 必须被节点真正渲染出来，否则「校验中 / 已校验」
// 状态对用户不可见。
//
// 这条守卫是拿真事故换来的：合规校验从图片扩到视频/音频时，只解除了 hook 里的
// kind 限制，而 VideoInputNode / AudioInputNode / GenVideoNode / SubtitleEraseNode
// 四个节点从来就没渲染过 badge——之前它们的状态查询是 disabled 的、badge 恒为
// null，所以这个遗漏一直看不出来。类型系统也发现不了：badge 只是没被用而已。
//
// 用 import.meta.glob 读源码而不是 node:fs，是为了不给 web 这个纯浏览器 tsconfig
// 引入 @types/node 依赖。
const sources = import.meta.glob('./*.tsx', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

describe('每个用 useMediaNodeMenu 的节点都渲染了 badge', () => {
  const consumers = Object.entries(sources).filter(
    ([path, src]) =>
      !path.includes('.test.') &&
      // hook 自身的函数签名也长这样，排掉——它是提供方，不是消费方。
      !path.endsWith('useMediaNodeMenu.tsx') &&
      src.includes('useMediaNodeMenu({'),
  );

  it('至少扫到了那几个已知的消费者（守卫本身没瞎）', () => {
    expect(consumers.length).toBeGreaterThanOrEqual(6);
  });

  it.each(consumers.map(([path]) => path))(
    '%s 把 badge 交给了 NodeShell（或自行渲染）',
    (path) => {
      const src = sources[path];
      // 两种合法形态：传 NodeShell 的 titleExtra，或在自定义布局里直接渲染 badge。
      const rendered =
        /titleExtra=\{[^}]*[bB]adge\}/.test(src) || /\{\s*\w*[mM]enu\.badge\s*\}/.test(src);
      expect(rendered, `${path} 取了 badge 但没渲染，用户看不到校验状态`).toBe(true);
    },
  );
});
