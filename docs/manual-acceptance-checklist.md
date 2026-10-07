# 人工验收清单

汇总各计划与报告里标为"待人工"的验收项（GUI、实机、权限被拒而未执行的步骤）。每项含步骤、预期结果、出处。发版时的逐机矩阵另见 `docs/release-checklist.md`。

编号规则：M = macOS，W = Windows，C = 通用（两个平台都要做）。

## 一、macOS

### M1 dmg 隔离属性与首次打开放行
- 步骤：把 dmg 放进"下载"目录（保留隔离属性）打开，拖入应用后首次启动，严格按包内《首次打开说明.txt》放行。
- 预期：说明与实际界面一致，能放行；`codesign -dv /Applications/喵剧AI.app 2>&1 | grep Signature` 输出 `Signature=adhoc`。说明与界面不符就改说明。
- 出处：p8 task-8-report（未做 GUI 隔离手测）、p6 task-8-report（Step 3–5）、`docs/release-checklist.md` 第 4 节。

### M2 macOS 14 / 15 回归（D8）
- 步骤：在 macOS 14 和 15 各一台 Apple 芯片机（另加一台 Intel 的 x64.dmg，不能用 Rosetta 代替）装对应 dmg，点顶栏最右侧"设置"打开弹框，进"环境诊断"，运行连接与编解码检查，再选带声音的 mp4。
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

### W6 文件锁（rename 覆盖正在被读取的文件）
- 步骤：Windows 实机上一边在画布里播放 / 预览某个媒体文件，一边触发会覆盖同一路径的写入（生成结果回写、脚本节点工作区 atomicWrite）；另在脚本节点任务运行中强杀 sidecar 后重启应用。
- 预期：写入不报 ACCESS_DENIED；重启时 `skillbundle.Extract` 清理旧 runtime 目录不因残留 python 子进程占用而失败。若出现失败，记录路径与报错交 Go 侧处理（tmp+rename 在 Windows 打开中的目标上会失败）。
- 出处：side-win-report 第 4 步“文件锁”（未确定，仅记录）。

### W7 长路径（260 字符）
- 步骤：把数据目录改到较深的目录（例如 `D:\很长的目录名\……\MiaojuData`，使总长接近 200 字符），运行脚本节点任务（skill-runs/日期/runId 下会再展开 Skill 文件），并生成、保存媒体。
- 预期：任务与写盘都成功。若因 MAX_PATH 失败，需要在 Rust 构建里加 longPathAware manifest 或在 Go 侧做 `\\?\` 前缀。
- 出处：side-win-report 第 4 步“长路径”（没把握，仅记录）。

### W8 中文用户目录下的内置 Python
- 步骤：用中文用户名（`C:\Users\张三\…`，最好含空格）的 Windows 账号解压运行，执行一次带脚本的 Skill（同 C5）。
- 预期：sidecar 找到 exe 同目录的 `python\python.exe`，传给 Python 的脚本与工作区路径不乱码，任务成功。
- 出处：side-win-report 第 4 步“中文用户名目录”。

### W9 WebView2 / SmartScreen / 单实例 实机复核
- 步骤：在 Windows 实机依次做 W1 的 SmartScreen 放行、W3 的 WebView2 缺失弹窗、W2 的单实例（再次双击 `miaoju.exe`）。
- 预期：同 W1–W3。side-win 只做了交叉编译与静态检查，这三项都没有在真机上运行过。
- 出处：side-win-report 第 5 步“无法在 mac 上验证的”。

## 三、通用（macOS 与 Windows 都做）

### C1 app.restart 与单实例插件共存（含数据目录迁移端到端）
- 步骤：`pnpm dev`（或打包版）里在设置弹框的"配置"里修改数据目录并保存，触发 `apply_data_dir_change` → 关闭 sidecar → `app.restart()`。接着走完迁移、切换、清理旧目录；再把目标设到外接盘、拔盘后重启一次。
- 预期：旧进程退出后新窗口正常出现，且只有一个窗口；迁移期间显示“正在迁移数据目录，请勿关闭应用”，完成后数据都在新目录、旧目录被清理；外接盘未连接时出现失败页。若重启后第二个实例被单实例插件拦下导致应用直接退出、没有新窗口，按 brief 退化为“已保存，请退出并重新打开应用”。
- 出处：p4 task-7-report（Step 7 未做）、p4 账本 Ruling I4、integration-round2-report 遗留问题 1（brief Step 7 第 7、8 条）。

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
- 步骤：设置弹框"配置"里查看版本号；覆盖安装上一版本。
- 预期：版本号等于本次版本；项目、设置、媒体都保留，内置模型按 M11 合并。
- 出处：release-checklist；spec E4。

### C11 设置弹框
- 步骤：点顶栏最右侧（Agent 之后）的"设置"按钮；依次切换左侧菜单；关闭后再打开；进入"环境诊断"点各按钮。
- 预期：以弹框打开，不跳转页面；菜单只有一级，只有"模型中心、配置、环境诊断"三项（默认打开模型中心），没有后处理、绘图预设、多角度、打光、剧本生成分镜、Agent 模型；"配置"含数据目录与版本号；环境诊断的按钮是 antd 样式，检查可正常运行。
- 出处：2026-10-07 变更（spec M8、实施期裁定）。

### C12 独立上游的配置与生效
- 步骤：设置 → 模型中心：① 顶部"全局上游"填好地址和 Key；② 给某视频或图片模型单独配置上游，只填地址不填 Key（或反之）后保存；③ 两项都填后保存，用该模型生成一次，再用一个未单独配置的模型生成一次；④ 把该模型的两项都清空后保存，再生成一次。
- 预期：②被拒绝并提示地址和 Key 必须同时填写；③单独配置的模型请求走它自己的上游（在上游侧看到请求），未配置的模型走全局；④留空后回到全局；Key 在界面只显示掩码，保存时不改 Key 则保留原值；重启应用与升级内置数据后独立上游仍保留。
- 出处：2026-10-07 变更（spec M2、M3，实施期裁定）。

### C13 换上游后旧任务快速失败
- 步骤：给一个视频模型配好独立上游并提交一个耗时较长的生成任务；任务运行中退出应用，改该模型的上游地址或 Key（或清空回全局），再启动应用。
- 预期：续轮询的旧任务因上游指纹不一致立即失败（不长时间挂起、不请求新上游），节点显示失败原因；新提交的任务正常。升级前就存在的无指纹旧任务照常续轮询。
- 出处：2026-10-07 变更（上游指纹，commit b1ec2bf）。

### C14 地址带 /v1 的自动处理
- 步骤：在模型中心把全局上游地址填成 `https://host/v1`（再试 `https://host/v1/` 与 `https://host/`），保存后重新打开；对单独配置的模型同样各试一次，然后生成一次图片或视频，并发一次 Agent 对话。
- 预期：保存后地址末尾的 `/` 与 `/v1` 被自动去掉；三种写法效果一致，生成、Agent、剧本生成都能连通，请求路径里没有重复的 `/v1/v1`。
- 出处：2026-10-07 变更（commit b1ec2bf M-1）。

## 四、下载（side-dl）

side-dl（5156c50 + 审查修复 fc8356a）已在 integration 第四轮合入（594e023），可以执行。

### D1 打包版各下载入口弹系统保存对话框
- 步骤：macOS 打包版（`tauri build`）里分别点：图片 / 视频 / 音频工具条、预览角标、Lightbox、保存到本地节点（单个 + 全部）、生成历史（右键 + 批量 ZIP）、导演台截图和 JSON 导出。
- 预期：弹出系统保存对话框（批量时是选文件夹），文件内容正确，并且有“已保存”提示。
- 出处：side-dl-report 第 6 节第 1 条。

### D2 保存对话框挂在主窗口上
- 步骤：触发任一保存。
- 预期：macOS 上显示为主窗口的 sheet，不会被窗口挡住。
- 出处：side-dl-report 第 6 节第 2 条。

### D3 拒绝保存到媒体目录或覆盖源文件
- 步骤：在保存对话框里选数据目录 `media/` 下的位置，或直接选源文件本身。
- 预期：提示错误，源文件不受影响。
- 出处：side-dl-report 第 6 节第 3 条。

### D4 Windows 走 Rust 对话框
- 步骤：Windows 上点 D1 中的同样入口。
- 预期：走 Rust 弹出的保存对话框，不再出现 WebView2 自带的下载条。
- 出处：side-dl-report 第 6 节第 4 条。

### D5 建议文件名的扩展名
- 步骤：保存 jpg/webp 格式的生成图片、mov/webm 视频、wav/m4a 音频，以及 Rust 识别不了的格式（avif/heic/ogg/flac/mkv 任选）。
- 预期：能识别的格式，对话框默认名按文件头修正为真实扩展名（fc8356a 的 sniff_media_ext）；识别不了的沿用 image→png、video→mp4、audio→mp3 兜底（已知遗留，记录即可）。
- 出处：side-dl-report 第 6 节第 5 条；side-dl-review 第二节、第五节第 2 条。

### D6 浏览器回退：导演台多张截图打成 ZIP
- 步骤：在纯浏览器（非 Tauri）里导出导演台四/十二方位截图。
- 预期：下载一个 ZIP，而不是逐张下载（行为变化，确认可接受）。
- 出处：side-dl-report 第 6 节第 6 条。

### D7 最终文件名的扩展名修正与不覆盖（macOS + Windows 打包版）
- 步骤：在 macOS 和 Windows 打包版里各保存一个 jpg 或 webp 产物：① 看对话框默认名；② 在对话框里把扩展名改成错误的值（如 `.png`）后确认；③ 目标文件夹里先放一个与“修正后文件名”同名的文件，再重复 ②。
- 预期：① 默认名就是正确扩展名；② 落盘文件是正确扩展名；③ 已有文件不被覆盖，新文件名自动加序号。
- 出处：side-dl-review 第五节第 4 条。

## 对象存储

### O1 对象存储配置、视频上传与去重
- 步骤：① 设置 → 配置 → 对象存储，填 TOS 配置，点「测试连接」；② 用本机视频作参考跑一次 doubao-seedance 生成；③ 同一视频再生成一次；④ 换成 OSS 配置重复 ①–②；⑤ 用本机图片作参考跑一次 seedream；⑥ 清除配置后再跑步骤 ②。
- 预期：① 显示连接正常；② 生成成功，桶里出现 `miaoju-inputs/<哈希>.mp4`；③ 桶里不新增对象；④ 同 ①–②；⑤ 生成成功且桶里没有新增图片；⑥ 报「未配置对象存储…」。
- 出处：对象存储设计 `docs/superpowers/specs/2026-10-07-object-storage-design.md`。

### O2 Seedance 合规校验（内置模型 seedance-asset）
- 步骤：配置对象存储和上游后，① 对一张图、一段视频点「合规校验」；② 用校验过的素材跑一次 seedance 生成；③ 故意用不合规素材（如含真人）校验。
- 预期：① 节点角标变「已通过」；② 上游请求里的素材是 `asset://…`；③ 角标显示上游返回的原文错误。
- 出处：`docs/superpowers/specs/2026-10-07-seedance-asset-model-design.md`。

## 命令行工具

Windows 实机全部必做；macOS 上 sidecar 级冒烟（`project list`/`node create`/SSE 推送/退出码 8/Codex 沙箱退出码 9）已由 CLI Task 14 用独立 sidecar 覆盖，下列需要真实壳与网页的项仍需手工做。

### L1 真实壳写地址文件
- 步骤：`pnpm dev` 启动应用后执行 `cat ~/Library/Application\ Support/com.miaoju.desktop/cli-endpoint.json; stat -f %Lp <同一路径>`；再退出应用并查看该文件。
- 预期：内容为 `{"version":1,"port":…,"token":"…","appVersion":"<package.json 版本>"}`，权限 `600`；退出应用后文件已被删除，再执行 `miaoju project list` 退出码为 8、stderr 为「喵剧未运行，请先打开应用」。
- 出处：CLI 计划 Task 14 Step 2（控制端裁定改为手工项）。

### L2 画布空闲刷新
- 步骤：应用里打开一个项目，终端执行 `miaoju node create --type image-input --content <图片 URL> --project <ID>`。
- 预期：画布在 1 秒内出现新节点，视口不跳动。
- 出处：CLI 计划 Task 14 Step 2。

### L3 设置卡片安装（macOS、Windows 各一次）
- 步骤：设置 → 配置 → 命令行工具 → 安装；重新打开终端执行 `miaoju --help`。
- 预期：命令可用。

### L4 Windows：用户 Path
- 步骤：安装后查看注册表 `HKCU\Environment\Path`。
- 预期：`%USERPROFILE%\.miaoju\bin` 已出现在用户 Path 中；原有的 `%USERPROFILE%` 类变量仍能展开（值类型仍是 REG_EXPAND_SZ）。

### L5 Windows：应用关闭时的退出码
- 步骤：关闭应用，执行 `miaoju project list`。
- 预期：退出码为 8（验证 WSAECONNREFUSED 的识别）。

### L6 覆盖安装后的版本不一致
- 步骤：覆盖安装新版本后打开设置里的命令行工具卡片，点「重新安装」。
- 预期：先显示「版本不一致」；重新安装后恢复正常。

### L7 拖拽与撤销
- 步骤：在画布上按住拖动一个节点时，终端执行 `miaoju node create`；松手；再按撤销。
- 预期：正在拖拽的节点不跳动；松手后新节点出现；撤销不会删掉 CLI 新建的节点。

### L8 应用退出耗时
- 步骤：打开一个项目（SSE 已连接）后退出应用，与接入 CLI 前的版本对比。
- 预期：退出耗时没有明显变长（SSE 连接已立即断开）。

### L9 卸载
- 步骤：设置 → 配置 → 命令行工具 → 卸载，重新打开终端。
- 预期：`~/.zshrc` 中的标记块已被移除，`miaoju` 命令已不存在。

### L10 Codex 沙箱（Windows）
- 步骤：Windows 上在 Codex 默认沙箱里执行 `miaoju project list`，再在允许网络的配置下执行一次。
- 预期：前者退出码 9 并提示「网络被 AI 助手的沙箱拦截，请允许本命令访问网络后重试」（若 Windows 沙箱不拦截 loopback 则记录实际行为）；后者退出码 0。

### L11 AI 助手直接使用 skill
- 步骤：设置 → 配置 → 命令行工具 → 安装（卡片显示 Claude Code、Codex 的 skill 均为「已安装」）；在 Claude Code 新会话里直接说「用喵剧列出我的项目」；Codex 同理。
- 预期：两者都会调用 `miaoju project list` 并列出项目，无需再复制提示词。

### L12 让 AI 助手安装
- 步骤：先卸载；切到「让 AI 助手安装」页签，复制提示词发给 Claude Code。
- 预期：它按提示词完成安装（被沙箱拦截时会申请权限）；重新打开终端 `miaoju --help` 可用，设置卡片刷新后 Claude Code 的 skill 显示「已安装」。
- Windows 真机上再按 Windows 版提示词做一次，完成后执行 `reg query HKCU\Environment /v Path`：值类型仍是 REG_EXPAND_SZ，原有的 `%USERPROFILE%` 类引用没有被展开。
- 预先放一个用户自己的 `~/.claude/skills/miaoju-cli` 目录（无 `.miaoju-managed`）再发提示词：AI 报「已存在同名目录，未覆盖」，目录内容不变。

## 条目统计

macOS 5 项、Windows 9 项、通用 14 项、下载 7 项，共 35 项（C8、C9 内部另含子项），另有对象存储 1 项（O1，内含 6 个步骤）、合规校验 1 项（O2）、命令行工具 12 项（L1–L12）。
