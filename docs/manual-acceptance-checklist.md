# 人工验收清单

汇总各计划与报告里标为"待人工"的验收项（GUI、实机、权限被拒而未执行的步骤）。每项含步骤、预期结果、出处。发版时的逐机矩阵另见 `docs/release-checklist.md`。

编号规则：M = macOS，W = Windows，C = 通用（两个平台都要做）。

## 一、macOS

### M1 dmg 隔离属性与首次打开放行
- 步骤：把 dmg 放进"下载"目录（保留隔离属性）打开，拖入应用后首次启动，严格按包内《首次打开说明.txt》放行。
- 预期：说明与实际界面一致，能放行；`codesign -dv /Applications/喵剧AI.app 2>&1 | grep Signature` 输出 `Signature=adhoc`。说明与界面不符就改说明。
- 出处：p8 task-8-report（未做 GUI 隔离手测）、p6 task-8-report（Step 3–5）、`docs/release-checklist.md` 第 4 节。

### M2 macOS 14 / 15 回归（D8）
- 步骤：在 macOS 14 和 15 各一台 Apple 芯片机（另加一台 Intel 的 x64.dmg，不能用 Rosetta 代替）装对应 dmg，打开"设置 → 环境诊断"，运行连接与编解码检查，再选带声音的 mp4。
- 预期：17 项全部 PASS；任一 WebCodecs 项（尤其 AudioEncoder/AudioDecoder、AAC）失败则按 release-checklist 第 5 节抬高最低系统版本。
- 出处：spec D8；p0-results.md；p6 task-8-report；p8 task-11-report（实机矩阵）。

### M3 数据目录权限
- 步骤：首次启动后 `stat -f %Lp ~/Library/Application\ Support/com.miaoju.desktop`。
- 预期：输出 `700`。
- 出处：p0-carryover（数据目录权限）、p8 修复轮、release-checklist。

### M4 单实例、退出无残留、强杀联动（P0 遗留的三项）
- 步骤：运行中再次打开应用；正常退出后 `pgrep -fl sidecar`；在活动监视器强制退出 `miaoju`。
- 预期：只有一个窗口且被带到前台；退出后 `pgrep` 无输出；强杀主程序后 5 秒内 sidecar 消失。
- 出处：p0-results.md（macOS 三个未勾项）、p0 progress（单实例与强杀仍待确认）。

### M5 3D 导演台人偶模型与截图回传（dmg 再测）
- 步骤：打包版里添加 3D 导演台节点，加载 `/models/ue-mannequin-retopology.glb`，调整姿态并截图回传到画布。
- 预期：模型正常加载，截图回传成功；Web Inspector 无 CSP 报错。
- 出处：p3a task-6-report 清单第 9 项。

## 二、Windows

### W1 zip 打包与运行
- 步骤：用 `scripts/package-windows.sh` 出 zip，在 Windows 11 x64 与 Windows 10 22H2 x64 解压后运行 `miaoju.exe`；另在压缩包预览里直接双击一次。
- 预期：解压后正常启动、`%APPDATA%\com.miaoju.desktop\` 存在；压缩包预览里双击时失败页提示"请先把 zip 完整解压"；首次运行出现 SmartScreen，"更多信息 → 仍要运行"可放行；`首次打开说明.txt` 文件名与内容不乱码。
- 出处：p0 progress（Task 7 Step 3 留给用户）、p0-results.md Windows 一节、p8 task-11 / release-checklist"仅 Windows"。

### W2 Windows 单实例、退出无残留、强杀联动
- 步骤：再次双击 `miaoju.exe`；关闭窗口后看任务管理器；任务管理器结束 `miaoju.exe`。
- 预期：只有一个窗口；关闭后无 `sidecar.exe`；强杀后 5 秒内 `sidecar.exe` 消失；`%APPDATA%\com.miaoju.desktop\diag\sample.mp4` 存在。
- 出处：p0-results.md（Windows 未勾项）。

### W3 WebView2 缺失提示（E8）
- 步骤：在没有 WebView2 的环境（Windows Server / LTSC 虚拟机）运行；没有这样的环境时，用 debug 构建设 `MIAOJU_FAKE_NO_WEBVIEW2=1`。
- 预期：弹窗出现，点"确定"打开微软 WebView2 下载页。
- 出处：p8 task-11 / release-checklist；spec E8。

### W4 Private Network Access
- 步骤：Windows 实机打开诊断页，运行 HTTP 检查。
- 预期：不因 CORS 预检失败；若控制台提到 Private Network Access，记录并交 Go 侧加 `Access-Control-Allow-Private-Network: true` 后重新出包。
- 出处：p0-carryover（带入 P8）；release-checklist。

### W5 内置 Python 打包后在 Windows 跑 Skill
- 步骤：见 C5，在 Windows zip 包内执行。
- 预期：同 C5；Windows 没有 bash，不依赖 bash。
- 出处：p6 task-8-report（Step 3–5）；spec X6。

## 三、通用（macOS 与 Windows 都做）

### C1 app.restart 与单实例插件共存
- 步骤：`pnpm dev`（或打包版）里在设置页修改数据目录并保存，触发 `app.restart()`。
- 预期：旧进程退出后新窗口正常出现，且只有一个窗口；若重启后没有新窗口，按 brief 退化为"保存后提示手动重启"。
- 出处：p4 task-7-report（Step 7 未做）、p4 账本 Ruling I4。

### C2 Tauri 里的下载
- 步骤：在 Tauri 窗口里触发下载类操作（生成历史 ZIP 下载、"保存到本地"节点、结果文件下载）。
- 预期：能弹出保存对话框或落到下载目录，文件内容正确；WebView 默认的浏览器式下载在 Tauri 中不被静默吞掉。
- 出处：p3bc 账本 Ruling（下载问题另开 P8 小任务 side-dl）；p3bc task-11-report（S3、S5 待人工）。

### C3 打包后深层路由刷新（/canvas/:id）
- 步骤：打包版里进入某个项目（地址形如 `/canvas/<id>`），按 Cmd+R / Ctrl+R 刷新；再用"所有项目"切换项目后刷新。
- 预期：不白屏、不 404，回到同一项目。
- 出处：p3a task-6-report 清单第 4 项（Cmd+R 刷新不白屏，dmg 再测）。

### C4 3D 导演台在 tauri:// 来源下的 Cache Storage 与 postMessage
- 步骤：打包版（`tauri://` / `http://tauri.localhost` 来源）中打开 3D 导演台；检查 Cache Storage 是否可写、导演台 iframe 与主页面间 postMessage 是否往返正常；人偶模型加载与截图回传。
- 预期：Cache Storage 可用（或有降级而不报错）；postMessage 收发正常；无 CSP 与跨源报错。
- 出处：p3a task-6-report 清单第 9 项；p3bc task-11-report（F2 待人工）。

### C5 内置 Python 跑 Skill
- 步骤：打包版里运行脚本节点"剧本生成分镜"（Skill 里带脚本的任务）；日志中确认找到内置 Python（不再出现"找不到内置 Python"警告）。
- 预期：任务成功完成；脚本只能读写任务工作区；Python 路径由壳传入 `--python-home` 与打包后的目录结构一致。
- 出处：p6 task-8-report（Step 3–5 待人工）；integration-report 遗留问题 3。

### C6 失败页"改用默认数据目录"按钮
- 步骤：把数据目录设到外接盘后拔掉盘，或把 `location.json` 写坏，再启动应用；在失败页点"改用默认数据目录"。
- 预期：出现失败页；点击后改用默认目录启动成功；按钮流程无并发重复点击问题。（SidecarGate 按钮流程没有组件级测试，只有 server.ts 调用顺序和 gateState 判断函数的单测。）
- 出处：p8 fix-round-1-report（T12 演练时手动走一遍）。

### C7 sidecar 崩溃后自动重启，以及前端恢复
- 步骤：强制结束 `sidecar` 一次；60 秒内再结束第二次。
- 预期：第一次：界面自动恢复，主界面的 REST 请求与 Agent WS 都用新端口和新 token 继续工作，不需要重启应用；第二次：出现失败页，点"重试"恢复。
- 出处：p8 账本（崩溃 60 秒内只自动重启一次）；p3a fix-round-1（sidecar 重启后刷新端口与 token）；integration-report 遗留问题 1；release-checklist。

### C8 P3a 的 Tauri 手动清单
- 步骤与预期（逐项）：
  1. 启动先显示"正在连接本地服务…"，如接口不可用则显示错误页，有"重试""环境诊断"；不误建项目。
  2. "环境诊断"进 `/diag`：连接 3 项加编解码 8 项 PASS，选视频后 6 项媒体检查 PASS；"← 返回画布"可回。
  3. Web Inspector：无 CSP 报错、无 `fonts.googleapis.com` 请求，字体为本地 woff2（Inter）。
  4. 首次启动自动建"未命名项目"进画布（刷新见 C3）。
  5. 文字/上传/拖动/连线，显示"已保存"后重启回到原项目。
  6. 所有项目：新建/重命名/复制/删除；删除上次项目后重启，打开最近修改的项目。
  7. 宫格切分、标注、旋转无 SecurityError / tainted canvas。
  8. 故事板切换、小地图、快捷键提示、素材库。
  9. 3D 导演台（见 C4）。
- 出处：p3a task-6-report；p3a 账本（GUI 手动清单待人工）。

### C9 P3b/P3c 的 Tauri 手动清单
- 步骤与预期：以下六组各自在 Tauri GUI 里走一遍，核对对应功能行为。
  - X3：界面上没有积分、估价、价格说明；生成按钮不显示费用。
  - F2：15 种节点可添加；"保存到本地"在右键菜单；"图片对比""预览"仍隐藏。
  - S3：节点右键"在访达/资源管理器中显示"（macOS 与 Windows 各测）打开所在文件夹并选中文件。
  - S5：生成完成后立即下载到本地；下载失败时节点名行显示"未保存到本地"角标与重试按钮，重试可成功。
  - X2：无协作、presence、owner 锁相关界面或请求。
  - F10 / S10：Skill JSON 导入导出隐藏；无项目导出导入入口。
- 出处：p3bc task-11-report（手动验收 Step 2，全部标"待人工"）。

### C10 版本号、覆盖安装与数据保留
- 步骤："设置"里查看版本号；覆盖安装上一版本。
- 预期：版本号等于本次版本；项目、设置、媒体都保留，内置模型按 M11 合并。
- 出处：release-checklist；spec E4。

## 条目统计

macOS 5 项、Windows 5 项、通用 10 项，共 20 项（C8、C9 内部另含子项）。
