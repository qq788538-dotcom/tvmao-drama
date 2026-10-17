# 贡献指南

感谢你愿意改进喵剧AI！提交之前请花几分钟读完下面的约定。

## 提问与反馈

- **使用问题**：先查 [用户手册](docs/user-guide.md) 和其中的常见问题。
- **Bug**：用 Bug 模板新建 Issue，附上系统版本、应用版本和「设置 → 环境诊断」的检查结果。
- **新功能**：先开 Issue 说明使用场景，确认方向后再写代码，避免白费功夫。
- **安全问题**：不要公开提 Issue，按 [安全策略](SECURITY.md) 私下报告。

## 开发流程

1. Fork 仓库，从 `main` 拉一个分支，例如 `fix/history-zip`、`feat/audio-fade`。
2. 按 [开发指南](docs/development.md) 搭好环境，跑通 `pnpm dev`。
3. 修改代码并补上测试。修 Bug 时最好先写一个能复现问题的失败测试。
4. 提交前在本地跑一遍相关检查：

   ```sh
   cd server && gofmt -l . && go vet ./... && go test ./...
   cd cli && gofmt -l . && go test ./...
   cd web && npx tsc --noEmit && npx vitest run
   cd src-tauri && cargo test
   ```

5. 推送分支并发起 Pull Request，说明改了什么、为什么改、怎么验证的。CI 全绿后等待评审。

## 代码约定

- **跟着周围的代码写。** 命名、注释密度、注释语言（本仓库注释以中文为主）与所在文件保持一致。
- **一个 PR 只做一件事。** 重构、格式化和功能修改分开提交，方便评审和回滚。
- **Go**：提交前 `gofmt`；错误要么处理要么返回，不要静默丢弃；外部 HTTP 调用必须有超时或可取消的 context。
- **前端**：TypeScript 严格模式；effect 里注册的监听器、定时器、`URL.createObjectURL` 都要在清理函数里释放；异步回调落地前确认组件和项目仍是当前的。
- **Rust**：前端能调用的命令不得接受任意文件路径；新增 Tauri 权限要在 PR 里说明理由。
- **安全**：不要提交任何密钥、令牌或个人数据；不要在日志里输出 API Key。

## 提交信息

沿用仓库现有风格：`<类型>(<范围>): <说明>`，例如：

```
fix(history): 批量下载用 arrayBuffer 交给 JSZip
feat(canvas): 视频节点支持逐帧拉片
```

常用类型：`feat`、`fix`、`perf`、`refactor`、`test`、`docs`、`ci`、`chore`。

## 许可

提交贡献即表示你同意以本项目的 [许可证](LICENSE) 发布你的代码。
