# P0 遗留事项（写后续计划时必须带上）

来源：P0 逐任务审查与最终整体审查（2026-09-30）。每条都已裁定不在 P0 修，按阶段归类。

## 带入 P1（Go sidecar 业务迁移）——在存入 API Key 之前必须完成

- ~~**数据目录权限**：Go `main.go` 用 0o755、Rust `lib.rs` 的 `create_dir_all` 先建目录；两边都要改成仅当前用户可读写（Unix 0o700）。只改 Go 无效。~~ **已解决**：P1b 修复 9a456ec（Go 端 0700）与 P8 `data_dir::prepare`（Rust 端 0700）；自定义目录不再强制 0700（P8 修复轮），P4 迁移时在所选目录下建 0700 子目录。
- ~~**开发源只在 debug 构建放行**：`http://localhost:5173` 现在在 release 里也在 Origin 白名单。改为默认不含，由壳在 `cfg!(debug_assertions)` 下通过 `--origins` 传入。~~ **已解决**：P8 `sidecar_args` 仅在 debug 下通过 `--origins` 传入。
- ~~**优雅退出**：stdin EOF 时现在是 `os.Exit(0)`，壳退出时是直接 kill。有了 SQLite 和在飞的生成任务后，sidecar 需要退出钩子；壳应先关 stdin、短暂等待，再 kill。~~ **已解决**：Go 端 stdin EOF/`SHUTDOWN` 走同一条优雅退出路径（P1b 修复轮：先停 worker 再关库）；壳端 `SidecarManager::shutdown` 先写 `SHUTDOWN`、关 stdin、等待后才 kill（整合时补握手与测试）。注：生成管线/Seedance/Agent 刷新/脚本节点的后台 goroutine 另由 side-go（b469cbe）纳入等待。
- ~~`Authorization` 头必须真的带 `Bearer ` 前缀才接受（现在 `TrimPrefix` 不校验；`Basic <token>` 会挡住 `?token=` 回退）。~~ **已解决**：localauth 的 `HasPrefix` 校验，见 `TestAuthorizationWithoutBearerPrefixRejected`；CLI 路由同样只认 Bearer（CLI 计划 Task 1）。
- 预检响应加 `Access-Control-Max-Age: 600`；P3 接入参考仓 API 客户端时核对 Allow-Headers 是否够用。
- `http.Server` 加 `ReadHeaderTimeout`；`readToken` 加长度上限（低优先）。
- 补测试：`Origin: null`、错 token + 白名单 Origin、带正确 token 的外来 Origin 预检、WebSocket 外来 Origin 403、二进制级的缺 `--data-dir` / 短 token / `--origins`、失败用例断言退出码 1 和 stderr 非空。

## 带入 P3（前端迁移，诊断页移入“设置 → 环境诊断”）

- ~~`checks.ts`：媒体名未 `encodeURIComponent`；`runBasic` 无 catch；剪贴板写入被拒未处理；WebSocket 未处理 `onclose`。~~ P3a Task 5 已修。
- ~~P3 接入参考仓 API 客户端时核对 Allow-Headers 是否够用。~~ P3a 核实：参考仓客户端只发 `Authorization`、`Content-Type`，现有 `Authorization, Content-Type, Range` 够用。
- ~~CSP：若 `index.html` 加入内联 `<style>`，Tauri 可能注入 style nonce 使 `'unsafe-inline'` 失效。~~ P3a Task 3 已在 `tauri.conf.json` 设 `dangerousDisableAssetCspModification: ["style-src"]`。
- `diag.go`：`dst.Close()` 错误被忽略、拷贝失败留半截文件；超 500MB 返回 400 而非 413；500 响应回显 `err.Error()`；注释“只覆盖不堆积”只对同扩展名成立。（Go 侧，转 P4 搬诊断页时一并处理）
- ~~诊断路由和 WebSocket 回显路由届时替换或收进设置页。（P3a 暂挂隐藏路由 `/diag`；转 P4/P5）~~ **已解决**：P3a 保留隐藏路由 `/diag`，P5 把回显挪到 `/diag/ws`，整合后诊断页走 `/diag/ws`。

## 带入 P5（Agent）

- ~~`/api/v1/agent/ws` 现在是回显占位，换成真正的 Agent hub。~~ **已解决**：P5 真正的 Agent hub；回显诊断挪到 `/api/v1/diag/ws`。

## 带入 P8（Tauri 壳与打包）

- ~~**sidecar 生命周期状态机**：启动改为异步（现在 `setup` 里最多阻塞主线程 20 秒），状态分“启动中 / 就绪 / 失败”；READY 之后 sidecar 崩溃要能被发现并提示或重启；提供重启命令，失败页恢复“重试”按钮（P0 已改成“请退出并重新打开应用”的提示）。~~ **已解决**：P8（异步启动、启动中/就绪/失败、崩溃自动重启、重试命令）；主界面接入重启由 P3a 修复轮 d7052bd 解决，SidecarGate 主题色等见 side-ui 9bdf93e。实机验证见 `docs/manual-acceptance-checklist.md`。
- `stderr_tail` 只保留最后一行，多行错误丢上下文。
- ~~版本号现在写在 4 处（根和 web 的 `package.json`、`Cargo.toml`、`tauri.conf.json`），定一个单一来源。~~ **已解决**：P8 T1 `scripts/version.sh`（`set`/`check`）。
- ~~图标换成 1024×1024 正式图（P0 用 80×80 的 logo 生成，偏糊）。~~ **部分解决**：已用程序生成的 1024 占位图（`packaging/icon-1024.png`），正式设计稿到后重跑 `gen-icons.sh`。
- ~~构建警告：`STATIC_VCRUNTIME is deprecated`，改用 `tauri.conf.json` 的 `build.windows.staticVCRuntime` 或 `tauri_build::WindowsAttributes`。~~ **已解决**：P8 T9 升级 tauri-cli 到 2.12.1，警告消失。
- ~~脚本改用 `bash scripts/…` 调用，不依赖文件可执行位。~~ **已解决**：P8 各脚本按 `bash scripts/…` 调用。
- Windows：若 WebView2 对 `http://tauri.localhost → 127.0.0.1` 触发 Private Network Access 限制，预检响应需加 `Access-Control-Allow-Private-Network`（以 P0 的 Windows 实机结果为准）。
- ~~E8 WebView2 缺失提示、E5 首次打开放行说明。~~ **已解决（待实机）**：P8 已实现 WebView2 检测弹窗与首次打开说明，实机验证见 `docs/manual-acceptance-checklist.md`。

## 状态说明（2026-10-06）

未划线的条目在 sidecar 账本里没有明确的"已解决"记录（Authorization Bearer 校验、Max-Age、ReadHeaderTimeout、补测试、`diag.go` 细节、`stderr_tail`、Private Network Access），保持开放，需对照代码逐项确认后再划线。
