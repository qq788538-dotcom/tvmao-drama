# 上游来源

本仓从参考仓复制代码后**独立演进，不做同步**（spec E2）。这里只记录每次复制的来源，供追溯。

| 日期 | 参考仓 commit | 复制内容 | 备注 |
|---|---|---|---|
| 2026-09-30 | `fea92ab9ad58ac1aa6752855999c221fcb460d89` | `apps/web/public/logo.png` | P0 骨架，仅复制 logo |
| 2026-10-06 | `fea92ab9ad58ac1aa6752855999c221fcb460d89` | `apps/web/src/modules/canvas/**`、`src/shared/**`、`src/styles`、`src/types`、`src/setupTests.ts`、`src/vite-env.d.ts`、`public/models/`、`tailwind.config.js`、`postcss.config.js` | P3a 前端画布迁移；参考仓 HEAD 已到 `4d77979`，用 `git archive fea92ab` 取文件 |

参考仓：`/Users/james/github/huabu_studio`
