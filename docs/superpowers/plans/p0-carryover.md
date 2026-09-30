# P0 遗留事项（写后续计划时必须带上）

来源：P0 逐任务审查与最终整体审查（2026-09-30）。每条都已裁定不在 P0 修，按阶段归类。

## 带入 P1（Go sidecar 业务迁移）——在存入 API Key 之前必须完成

- **数据目录权限**：Go `main.go` 用 0o755、Rust `lib.rs` 的 `create_dir_all` 先建目录；两边都要改成仅当前用户可读写（Unix 0o700）。只改 Go 无效。
- **开发源只在 debug 构建放行**：`http://localhost:5173` 现在在 release 里也在 Origin 白名单。改为默认不含，由壳在 `cfg!(debug_assertions)` 下通过 `--origins` 传入。
- **优雅退出**：stdin EOF 时现在是 `os.Exit(0)`，壳退出时是直接 kill。有了 SQLite 和在飞的生成任务后，sidecar 需要退出钩子；壳应先关 stdin、短暂等待，再 kill。
- `Authorization` 头必须真的带 `Bearer ` 前缀才接受（现在 `TrimPrefix` 不校验；`Basic <token>` 会挡住 `?token=` 回退）。
- 预检响应加 `Access-Control-Max-Age: 600`；P3 接入参考仓 API 客户端时核对 Allow-Headers 是否够用。
- `http.Server` 加 `ReadHeaderTimeout`；`readToken` 加长度上限（低优先）。
- 补测试：`Origin: null`、错 token + 白名单 Origin、带正确 token 的外来 Origin 预检、WebSocket 外来 Origin 403、二进制级的缺 `--data-dir` / 短 token / `--origins`、失败用例断言退出码 1 和 stderr 非空。

## 带入 P3（前端迁移，诊断页移入“设置 → 环境诊断”）

- `checks.ts`：媒体名未 `encodeURIComponent`；`runBasic` 无 catch；剪贴板写入被拒未处理；WebSocket 未处理 `onclose`。
- `diag.go`：`dst.Close()` 错误被忽略、拷贝失败留半截文件；超 500MB 返回 400 而非 413；500 响应回显 `err.Error()`；注释“只覆盖不堆积”只对同扩展名成立。
- 诊断路由和 WebSocket 回显路由届时替换或收进设置页。
- CSP：若 `index.html` 加入内联 `<style>`，Tauri 可能注入 style nonce 使 `'unsafe-inline'` 失效，React 的 `style=` 会被拦——引入 CSS 时复查。

## 带入 P5（Agent）

- `/api/v1/agent/ws` 现在是回显占位，换成真正的 Agent hub。

## 带入 P8（Tauri 壳与打包）

- **sidecar 生命周期状态机**：启动改为异步（现在 `setup` 里最多阻塞主线程 20 秒），状态分“启动中 / 就绪 / 失败”；READY 之后 sidecar 崩溃要能被发现并提示或重启；提供重启命令，失败页恢复“重试”按钮（P0 已改成“请退出并重新打开应用”的提示）。
- `stderr_tail` 只保留最后一行，多行错误丢上下文。
- 版本号现在写在 4 处（根和 web 的 `package.json`、`Cargo.toml`、`tauri.conf.json`），定一个单一来源。
- 图标换成 1024×1024 正式图（P0 用 80×80 的 logo 生成，偏糊）。
- 构建警告：`STATIC_VCRUNTIME is deprecated`，改用 `tauri.conf.json` 的 `build.windows.staticVCRuntime` 或 `tauri_build::WindowsAttributes`。
- 脚本改用 `bash scripts/…` 调用，不依赖文件可执行位。
- Windows：若 WebView2 对 `http://tauri.localhost → 127.0.0.1` 触发 Private Network Access 限制，预检响应需加 `Access-Control-Allow-Private-Network`（以 P0 的 Windows 实机结果为准）。
- E8 WebView2 缺失提示、E5 首次打开放行说明。
