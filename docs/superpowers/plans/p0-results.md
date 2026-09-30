# P0 验证结果

## 已完成的自动验证（2026-09-30，macOS 26.2，Apple Silicon）

- `go test ./...`：3 个包全部通过（localauth 10、http 8、cmd/sidecar 7）。
- `pnpm --dir web test`：7 个测试通过；`pnpm --dir web build`：通过。
- `cargo test`：7 个测试通过；`cargo build`：无警告。
- debug 二进制冒烟：壳拉起 sidecar；sidecar 只监听 `127.0.0.1:<随机端口>`；进程参数只有 `--data-dir`，不含 token；不带 token 的 ping 返回 401；`kill -9` 主进程后 4 秒内 sidecar 自行退出，无残留进程。
- macOS 打包：`pnpm build` 产出 `src-tauri/target/release/bundle/dmg/喵剧AI_0.1.0_aarch64.dmg`（9.5MB，ad-hoc 签名）。
- Windows 打包：在 macOS 上用 cargo-xwin 交叉编译成功，产出 `out/miaoju-windows-x64-0.1.0.zip`（6.6MB，含 `miaoju.exe`、`sidecar.exe`）。构建有 1 条弃用警告 `STATIC_VCRUNTIME is deprecated`（转 P8）。

## 待手动验证

### macOS 打包版（dmg）

系统版本：macOS 26.2（用户于 2026-09-30 实机运行并反馈）
诊断页“复制结果”的文本：

```
UA: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)
PASS HTTP ping（带 token） — version=0.1.0 (19ms)
PASS HTTP ping（不带 token 应被拒绝） — 401 (2ms)
PASS WebSocket 回显 — 回显一致 (4ms)
PASS API VideoEncoder — 存在 (0ms)
PASS API VideoDecoder — 存在 (0ms)
PASS API AudioEncoder — 存在 (1ms)
PASS API AudioDecoder — 存在 (0ms)
PASS 编码 H.264 1080p — 支持 (4ms)
PASS 解码 H.264 — 支持 (59ms)
PASS 编码 AAC — 支持 (0ms)
PASS 解码 AAC — 支持 (8ms)
PASS Range 请求返回 206 — bytes 0-99/11576606 (3ms)
PASS 抽缩略图条（10 帧） — 时长 8.38 秒 (342ms)
PASS 剪辑（截取前段） — 输出 6380901 字节 (17ms)
PASS 强制重编码 H.264 — 输出 1065762 字节 (498ms)
PASS 音频分离：无声 mp4 — 输出 11367794 字节 (18ms)
PASS 音频分离：WAV — 输出 1478700 字节 (57ms)
```

- [x] 连接检查 3 项 + 编解码检查 8 项全部 PASS
- [x] 选择测试视频后 6 项媒体检查全部 PASS（测试视频 11.6MB、8.38 秒）
- [ ] 再次打开应用，只有一个窗口
- [ ] 退出应用后 `pgrep -fl sidecar` 无输出
- [ ] 在“活动监视器”强制退出 `miaoju` 后，5 秒内 `sidecar` 消失

### Windows（zip）

Windows 版本：
诊断页“复制结果”的文本：

```
（粘贴到这里）
```

- [ ] 连接检查 3 项 + 编解码检查 8 项全部 PASS
- [ ] 选择测试视频后 6 项媒体检查全部 PASS
- [ ] 再次双击 `miaoju.exe`，只有一个窗口
- [ ] 关闭窗口后任务管理器里没有 `sidecar.exe`
- [ ] 在任务管理器结束 `miaoju.exe` 后，5 秒内 `sidecar.exe` 消失
- [ ] `%APPDATA%\com.miaoju.desktop\diag\sample.mp4` 存在
