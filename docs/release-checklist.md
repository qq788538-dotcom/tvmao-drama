# 喵剧AI 发版检查清单

每次发版复制下面的"结果记录模板"到 `docs/release-results/<版本>.md`，逐项打勾并粘贴诊断结果。

## 1. 工具版本（记录到结果文件）

- `cargo tauri --version`、`rustc --version`、`go version`、`node --version`、`cargo xwin --version`
- 已知无害警告：（T9 Step 8 的结论写在这里）

## 2. 发版前

- [ ] 内置数据已重新导出（spec 第 9 节：模型和配置内置、不自动更新）
- [ ] `bash scripts/version.sh set <新版本>`，然后 `bash scripts/version.sh check` 输出"版本一致"
- [ ] `pnpm test:server && pnpm test:web && pnpm test:tauri && pnpm test:scripts` 全部通过
- [ ] `cd src-tauri && cargo xwin check --target x86_64-pc-windows-msvc` 通过

## 3. 构建

- [ ] `pnpm release` 产出：
  - `out/喵剧AI_<ver>_aarch64.dmg`
  - `out/喵剧AI_<ver>_x64.dmg`
  - `out/miaoju-windows-x64-<ver>.zip`
- [ ] 记录三个文件的大小和 `shasum -a 256`

## 4. 实机矩阵（D8：上线前 macOS 14 / 15 各跑一次；不通过就抬高最低版本）

| 机器 | 安装包 | 必须 |
|---|---|---|
| macOS 14（Apple 芯片） | aarch64.dmg | 是 |
| macOS 15（Apple 芯片） | aarch64.dmg | 是 |
| macOS 26（Apple 芯片，开发机） | aarch64.dmg | 是 |
| macOS 14 或 15（Intel） | x64.dmg | 是（没有实机时用借的机器，不能用 Rosetta 代替） |
| Windows 11 x64 | zip | 是 |
| Windows 10 22H2 x64 | zip | 是 |

虚拟机（UTM 等）可用于先行排查；虚拟机里 WebCodecs 编码失败可能是没有硬件编码器，必须回到实机复测后才能下结论。

### 每台机器都做

- [ ] 从"下载"目录打开（保留隔离属性），严格按包内《首次打开说明.txt》操作能放行；说明与实际界面不符就改说明
- [ ] 打开"环境诊断"（P3 之后在"设置 → 环境诊断"），点"运行连接与编解码检查"，再选一个带声音的 mp4：17 项全部 PASS，"复制结果"粘贴到结果文件
- [ ] "设置"里显示的版本号 = 本次版本
- [ ] 再次打开应用：只有一个窗口（S11）
- [ ] 退出应用后无残留：macOS `pgrep -fl sidecar` 无输出；Windows 任务管理器里无 `sidecar.exe`
- [ ] 强制结束主程序（活动监视器 / 任务管理器结束 `miaoju`）后 5 秒内 sidecar 消失
- [ ] 强制结束 `sidecar` 一次：界面自动恢复；60 秒内再结束一次：出现失败页，点"重试"恢复
- [ ] 覆盖安装上一个版本：项目、设置、媒体都还在

### 仅 macOS

- [ ] `codesign -dv /Applications/喵剧AI.app 2>&1 | grep Signature` 输出 `Signature=adhoc`
- [ ] `stat -f %Lp ~/Library/Application\ Support/com.miaoju.desktop` 输出 `700`

### 仅 Windows

- [ ] 在压缩包预览里直接双击 `miaoju.exe`：失败页提示"请先把 zip 完整解压"
- [ ] 解压后首次运行出现 SmartScreen，"更多信息 → 仍要运行"可放行
- [ ] 资源管理器里 `首次打开说明.txt` 文件名不乱码，记事本打开不乱码
- [ ] `%APPDATA%\com.miaoju.desktop\` 存在
- [ ] WebView2 缺失提示：在没有 WebView2 的环境（如 Windows Server / LTSC 虚拟机）运行，弹窗出现、点"确定"打开微软下载页；没有这样的环境时，用 debug 构建设 `MIAOJU_FAKE_NO_WEBVIEW2=1` 验证弹窗
- [ ] 若诊断页 HTTP 检查因 CORS 预检失败且控制台提到 Private Network Access：记录下来，交 Go 侧加 `Access-Control-Allow-Private-Network: true` 后重新出包

## 5. D8 判定

- macOS 14 或 15 上任一 WebCodecs 项（尤其 AudioEncoder/AudioDecoder、AAC）失败：把 `src-tauri/tauri.conf.json` 的 `bundle.macOS.minimumSystemVersion` 和 `scripts/release-macos.sh` 的校验值、两份说明里的"系统要求"一起提高到下一个通过的版本，重新出包。

## 6. 发出

- [ ] 三个安装包 + sha256 发给用户；附一句：首次打开请看包内《首次打开说明.txt》

## 结果记录模板

```
# <版本> 发版结果（<日期>）
工具版本：
sha256：
## <机器 1：系统版本 / 芯片>
诊断结果：
（粘贴）
勾选项：
```
