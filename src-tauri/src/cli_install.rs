//! 设置 → 命令行工具：把安装包里的 miaoju-cli 装成终端里的 `miaoju`。
//! macOS：~/.miaoju/bin/miaoju 符号链接到包内文件，~/.zshrc（及已存在的 ~/.bash_profile）追加带标记的 PATH 行。
//! Windows：复制到 %USERPROFILE%\.miaoju\bin\miaoju.exe（普通用户建不了符号链接），写用户级 Path。
//! 同时把包内 skill 装进已存在的 ~/.claude、~/.codex（macOS 符号链接，Windows 复制并写标记文件）；
//! 用户自己的同名目录一律不动。

use serde::Serialize;
use std::path::{Path, PathBuf};
use tauri::Manager;

const MARK_BEGIN: &str = "# >>> miaoju cli >>>";
const MARK_END: &str = "# <<< miaoju cli <<<";
const PATH_LINE: &str = r#"export PATH="$HOME/.miaoju/bin:$PATH""#;

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CliToolStatus {
    installed: bool,
    up_to_date: bool,
    command_path: String,
    bin_dir: String,
    skill_entry: String,
    skills: Vec<SkillStatus>,
    agent_prompt: String,
}

/// 安装后 skill 在 AI 助手里的目录名，与 SKILL.md frontmatter 的 name 一致。
const SKILL_NAME: &str = "miaoju-cli";
/// 包内 skill 目录名（资源目录下的 cli-skill/）：指向同名目录的链接才算"我们装的"。
const SKILL_SRC_NAME: &str = "cli-skill";
/// 复制安装（Windows）时写进目录的标记文件：有它才算"我们装的"，卸载、重装才敢动。
const MANAGED_MARK: &str = ".miaoju-managed";

#[derive(Serialize, Debug)]
#[serde(rename_all = "camelCase")]
pub struct SkillStatus {
    agent: String,
    path: String,
    /// 该 AI 助手的主目录（~/.claude、~/.codex）存在
    available: bool,
    installed: bool,
    /// 已有用户自己的同名目录（或文件、指向别处的链接），没有覆盖
    conflict: bool,
    /// 复制安装的 skill 版本与应用不一致，需要重新安装
    outdated: bool,
    /// 本次安装 / 卸载该 agent 时出的错
    error: Option<String>,
}

/// 某个 agent 的 skill 操作失败：(agent, 原因)
type SkillErrors = Vec<(&'static str, String)>;

/// (agent, 主目录, skill 安装位置)
fn skill_targets(home: &Path) -> [(&'static str, PathBuf, PathBuf); 2] {
    [("claude", ".claude"), ("codex", ".codex")].map(|(agent, dir)| {
        let root = home.join(dir);
        let dest = root.join("skills").join(SKILL_NAME);
        (agent, root, dest)
    })
}

#[derive(PartialEq)]
enum SkillOwner {
    Missing,
    /// 指向 cli-skill 的符号链接（含应用被移走后的悬空链接），或带标记文件的目录
    Ours,
    Foreign,
}

/// 判断存在用 symlink_metadata：悬空链接用 exists() 会被当成不存在，既删不掉也建不了新链接。
fn skill_owner(dest: &Path, src: &Path) -> SkillOwner {
    match std::fs::symlink_metadata(dest) {
        Err(_) => SkillOwner::Missing,
        Ok(m) if m.file_type().is_symlink() => match std::fs::read_link(dest) {
            Ok(t) if t == src || t.file_name().is_some_and(|n| n == SKILL_SRC_NAME) => {
                SkillOwner::Ours
            }
            _ => SkillOwner::Foreign,
        },
        Ok(m) if m.is_dir() && std::fs::symlink_metadata(dest.join(MANAGED_MARK)).is_ok() => {
            SkillOwner::Ours
        }
        Ok(_) => SkillOwner::Foreign,
    }
}

fn remove_ours(dest: &Path) -> std::io::Result<()> {
    let m = std::fs::symlink_metadata(dest)?;
    if m.file_type().is_symlink() {
        // Windows 上指向目录的链接要用 remove_dir 删
        std::fs::remove_file(dest).or_else(|_| std::fs::remove_dir(dest))
    } else {
        std::fs::remove_dir_all(dest)
    }
}

fn copy_dir_all(src: &Path, dst: &Path) -> std::io::Result<()> {
    std::fs::create_dir_all(dst)?;
    for e in std::fs::read_dir(src)? {
        let e = e?;
        let to = dst.join(e.file_name());
        if e.file_type()?.is_dir() {
            copy_dir_all(&e.path(), &to)?;
        } else {
            std::fs::copy(e.path(), &to)?;
        }
    }
    Ok(())
}

/// 把 `src`（包内 skill 目录）装进每个已存在的 AI 助手主目录。`link` 为 true 建符号链接，
/// 否则复制并写入内容为 `version` 的标记文件。用户自己的同名目录跳过不动。
/// 逐个 agent 尝试，一个失败不影响另一个，返回各自的错误。
fn install_skills(home: &Path, src: &Path, link: bool, version: &str) -> SkillErrors {
    let mut errs = Vec::new();
    for (agent, root, dest) in skill_targets(home) {
        if !root.is_dir() {
            continue;
        }
        if let Err(e) = install_skill(&root, &dest, src, link, version) {
            errs.push((agent, e));
        }
    }
    errs
}

fn install_skill(
    root: &Path,
    dest: &Path,
    src: &Path,
    link: bool,
    version: &str,
) -> Result<(), String> {
    match skill_owner(dest, src) {
        SkillOwner::Foreign => return Ok(()),
        SkillOwner::Ours => {
            remove_ours(dest).map_err(|e| format!("删除旧的 {} 失败：{e}", dest.display()))?
        }
        SkillOwner::Missing => {}
    }
    let parent = dest.parent().unwrap_or(root);
    std::fs::create_dir_all(parent).map_err(|e| format!("创建 {} 失败：{e}", parent.display()))?;
    place_skill(src, dest, link, version)
        .map_err(|e| format!("安装 skill 到 {} 失败：{e}", dest.display()))
}

fn place_skill(src: &Path, dest: &Path, link: bool, version: &str) -> std::io::Result<()> {
    #[cfg(unix)]
    if link {
        return std::os::unix::fs::symlink(src, dest);
    }
    #[cfg(not(unix))]
    let _ = link;
    let r = copy_dir_all(src, dest).and_then(|()| std::fs::write(dest.join(MANAGED_MARK), version));
    if r.is_err() {
        // 复制到一半失败时目录里还没有标记文件，不清掉的话下次会被当成用户目录
        let _ = std::fs::remove_dir_all(dest);
    }
    r
}

/// 只删我们装的（指向 cli-skill 的符号链接，或带标记文件的目录），逐个 agent 尝试。
fn uninstall_skills(home: &Path, src: &Path) -> SkillErrors {
    let mut errs = Vec::new();
    for (agent, _, dest) in skill_targets(home) {
        if skill_owner(&dest, src) == SkillOwner::Ours {
            if let Err(e) = remove_ours(&dest) {
                errs.push((agent, format!("删除 {} 失败：{e}", dest.display())));
            }
        }
    }
    errs
}

/// `version` 为应用版本，用于判断复制安装的 skill 是否过期；`errs` 是本次操作各 agent 的错误。
fn skill_statuses(
    home: &Path,
    src: &Path,
    version: &str,
    errs: &[(&str, String)],
) -> Vec<SkillStatus> {
    skill_targets(home)
        .into_iter()
        .map(|(agent, root, dest)| {
            let owner = skill_owner(&dest, src);
            let installed = owner == SkillOwner::Ours && dest.join("SKILL.md").is_file();
            // 只有复制安装才有标记文件；符号链接直接指向包内目录，不会过期
            let outdated = installed
                && std::fs::read_to_string(dest.join(MANAGED_MARK))
                    .is_ok_and(|v| v.trim() != version);
            SkillStatus {
                agent: agent.to_string(),
                path: dest.display().to_string(),
                available: root.is_dir(),
                // 悬空链接（应用被移走）不算已安装，提示用户重新安装
                installed,
                conflict: owner == SkillOwner::Foreign,
                outdated,
                error: errs
                    .iter()
                    .find(|(a, _)| *a == agent)
                    .map(|(_, e)| e.clone()),
            }
        })
        .collect()
}

const SANDBOX_NOTE: &str = "如果被沙箱拦截，请向我申请权限后重试，不要跳过。";

/// POSIX shell 字面量：单引号包住，内部的 ' 写成 '\''。双引号里 $ ` \ 仍会展开，
/// 安装目录名里带 $ 或反引号（Windows zip 解压到任意文件夹、改名后的 .app）就会被当成命令或变量。
fn sh_quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', r"'\''"))
}

/// PowerShell 字面量：单引号包住，内部的引号（含 PowerShell 同样视为单引号的 ‘ ’ ‚ ‛）重复一次。
/// 双引号字符串会展开 $var / $(...)，反引号是转义符。
fn ps_quote(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('\'');
    for c in s.chars() {
        if matches!(c, '\'' | '\u{2018}' | '\u{2019}' | '\u{201A}' | '\u{201B}') {
            out.push(c);
        }
        out.push(c);
    }
    out.push('\'');
    out
}

/// "让 AI 助手安装"页签里的提示词：AI 助手照着执行即可完成与安装按钮等价的步骤。
/// 安装包里的路径一律按字面量加引号（可能含空格、中文，甚至 $ 或引号，如 /Applications/喵剧.app/...）。
/// skill 一步带着与按钮安装相同的判断：已有用户自己的同名目录（或指向别处的链接）就不覆盖。
/// `version` 为应用版本，Windows 复制件的标记文件写它，与按钮安装一致（否则会被判为过期）。
pub fn agent_install_prompt(
    windows: bool,
    bundled_cli: &str,
    skill_dir: &str,
    version: &str,
) -> String {
    if windows {
        let (cli_q, skill_q) = (ps_quote(bundled_cli), ps_quote(skill_dir));
        let skill_md = ps_quote(&format!(r"{skill_dir}\SKILL.md"));
        let skill_cmd = |dir: &str| {
            format!(
                r#"$d = "$env:USERPROFILE\{dir}\skills\{SKILL_NAME}"; if ((Test-Path -LiteralPath $d) -and -not (Test-Path -LiteralPath "$d\{MANAGED_MARK}")) {{ Write-Error "已存在同名目录，未覆盖：$d" }} else {{ if (Test-Path -LiteralPath $d) {{ Remove-Item -LiteralPath $d -Recurse -Force }}; New-Item -ItemType Directory -Force "$env:USERPROFILE\{dir}\skills" | Out-Null; Copy-Item -LiteralPath {skill_q} -Destination $d -Recurse -Force; Set-Content -LiteralPath "$d\{MANAGED_MARK}" -Value "{version}" }}"#
            )
        };
        format!(
            r#"请帮我安装喵剧命令行工具 miaoju，并学会使用它。在 PowerShell 里依次执行：
1. New-Item -ItemType Directory -Force "$env:USERPROFILE\.miaoju\bin" | Out-Null; Copy-Item -LiteralPath {cli_q} "$env:USERPROFILE\.miaoju\bin\miaoju.exe" -Force
2. 把该目录加入用户 Path（已有就跳过；直接读写注册表，保留原值类型和其中未展开的 %USERPROFILE%）：
   $bin = "$env:USERPROFILE\.miaoju\bin"; $k = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment',$true); $p = $k.GetValue('Path','','DoNotExpandEnvironmentNames'); $kind = if ($p) {{ $k.GetValueKind('Path') }} else {{ 'ExpandString' }}; if (-not ($p -split ';' | Where-Object {{ $_.TrimEnd('\') -eq $bin }})) {{ $k.SetValue('Path', $(if ($p) {{ "$p;$bin" }} else {{ $bin }}), $kind) }}; $k.Close()
   然后执行下面这行，让新开的终端读到新的 Path（设置再清除一个临时用户变量，会广播环境变量变更）：
   [Environment]::SetEnvironmentVariable('MIAOJU_TMP','1','User'); [Environment]::SetEnvironmentVariable('MIAOJU_TMP',$null,'User')
3. 安装你自己的 skill（已存在用户自己的同名目录时不覆盖，会报"已存在同名目录，未覆盖"，请把这句话转告我）：
   - 如果你是 Claude Code：{claude}
   - 如果你是 Codex：{codex}
4. 运行 & "$env:USERPROFILE\.miaoju\bin\miaoju.exe" version 确认安装成功（新开的终端里可以直接运行 miaoju version），再阅读 {skill_md} 学会用法。
注意：以上命令会写入工作区以外的位置（%USERPROFILE%\.miaoju、用户 Path 环境变量、%USERPROFILE%\.claude 或 %USERPROFILE%\.codex）。{SANDBOX_NOTE}"#,
            claude = skill_cmd(".claude"),
            codex = skill_cmd(".codex"),
        )
    } else {
        let (cli_q, skill_q) = (sh_quote(bundled_cli), sh_quote(skill_dir));
        let skill_md = sh_quote(&format!("{skill_dir}/SKILL.md"));
        let skill_cmd = |dir: &str| {
            format!(
                r#"d=~/{dir}/skills/{SKILL_NAME}; if {{ [ -L "$d" ] && [ "$(readlink "$d")" != {skill_q} ] && [ "$(basename "$(readlink "$d")")" != {SKILL_SRC_NAME} ]; }} || {{ [ -e "$d" ] && [ ! -L "$d" ] && [ ! -e "$d/{MANAGED_MARK}" ]; }}; then echo "已存在同名目录，未覆盖：$d"; else mkdir -p ~/{dir}/skills && rm -rf "$d" && ln -sfn {skill_q} "$d"; fi"#
            )
        };
        format!(
            r#"请帮我安装喵剧命令行工具 miaoju，并学会使用它。依次执行：
1. mkdir -p ~/.miaoju/bin && ln -sf {cli_q} ~/.miaoju/bin/miaoju
2. 如果 ~/.zshrc 里没有 "{MARK_BEGIN}" 这一行，就在文件末尾原样追加下面三行（不带前导空格）：
{MARK_BEGIN}
{PATH_LINE}
{MARK_END}
3. 安装你自己的 skill（已存在用户自己的同名目录时不覆盖，会输出"已存在同名目录，未覆盖"，请把这句话转告我）：
   - 如果你是 Claude Code：{claude}
   - 如果你是 Codex：{codex}
4. 运行 ~/.miaoju/bin/miaoju version 确认安装成功，再阅读 {skill_md} 学会用法。
注意：以上命令会写入工作区以外的目录（~/.miaoju、~/.zshrc、~/.claude 或 ~/.codex）。{SANDBOX_NOTE}"#,
            claude = skill_cmd(".claude"),
            codex = skill_cmd(".codex"),
        )
    }
}

#[cfg_attr(not(unix), allow(dead_code))]
pub fn add_path_block(content: &str) -> String {
    if content.contains(MARK_BEGIN) {
        return content.to_string();
    }
    let mut s = content.to_string();
    if !s.is_empty() && !s.ends_with('\n') {
        s.push('\n');
    }
    s.push_str(&format!("{MARK_BEGIN}\n{PATH_LINE}\n{MARK_END}\n"));
    s
}

#[cfg_attr(not(unix), allow(dead_code))]
pub fn remove_path_block(content: &str) -> String {
    let Some(b) = content.find(MARK_BEGIN) else {
        return content.to_string();
    };
    let Some(rel) = content[b..].find(MARK_END) else {
        return content.to_string();
    };
    let mut end = b + rel + MARK_END.len();
    if content[end..].starts_with('\n') {
        end += 1;
    }
    format!("{}{}", &content[..b], &content[end..])
}

#[cfg_attr(not(windows), allow(dead_code))]
fn same_dir(a: &str, b: &str) -> bool {
    a.trim_end_matches('\\')
        .eq_ignore_ascii_case(b.trim_end_matches('\\'))
}

#[cfg_attr(not(windows), allow(dead_code))]
pub fn add_to_path_list(list: &str, dir: &str) -> String {
    if list.split(';').any(|p| same_dir(p, dir)) {
        return list.to_string();
    }
    if list.is_empty() {
        dir.to_string()
    } else {
        format!("{list};{dir}")
    }
}

#[cfg_attr(not(windows), allow(dead_code))]
pub fn remove_from_path_list(list: &str, dir: &str) -> String {
    list.split(';')
        .filter(|p| !p.is_empty() && !same_dir(p, dir))
        .collect::<Vec<_>>()
        .join(";")
}

/// 规范化 Windows Path 列表：去掉空项（含结尾的 `;`），用于判断新旧值是否语义相同。
#[cfg_attr(not(windows), allow(dead_code))]
pub fn normalize_path_list(list: &str) -> String {
    list.split(';')
        .filter(|p| !p.is_empty())
        .collect::<Vec<_>>()
        .join(";")
}

/// 运行命令，`timeout` 内没退出就杀掉并返回 None；退出码非 0 也返回 None。
pub fn output_with_timeout(
    cmd: &mut std::process::Command,
    timeout: std::time::Duration,
) -> Option<String> {
    use std::io::Read;
    use std::process::Stdio;
    use std::time::{Duration, Instant};

    let mut child = cmd
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .ok()?;
    // stdout 在单独线程里读：子进程再拉起的孙进程可能一直占着管道，主流程不能卡在 read 上。
    let mut stdout = child.stdout.take()?;
    let (tx, rx) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        let mut buf = Vec::new();
        let _ = stdout.read_to_end(&mut buf);
        let _ = tx.send(buf);
    });
    let deadline = Instant::now() + timeout;
    let exit = loop {
        match child.try_wait() {
            Ok(Some(st)) => break st,
            Ok(None) if Instant::now() < deadline => std::thread::sleep(Duration::from_millis(20)),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return None;
            }
        }
    };
    if !exit.success() {
        return None;
    }
    let rest = deadline
        .saturating_duration_since(Instant::now())
        .max(Duration::from_millis(200));
    let buf = rx.recv_timeout(rest).ok()?;
    Some(String::from_utf8_lossy(&buf).into_owned())
}

/// `miaoju version` 输出 `{"version":"x.y.z"}`（-o table 等也退回 JSON）。按字段精确比较：
/// 子串匹配会把 1.2.10 当成 1.2.1 的最新版（应用降级后不再提示重新安装）。
pub fn version_matches(output: &str, app_version: &str) -> bool {
    if app_version.is_empty() {
        return false;
    }
    let got = serde_json::from_str::<serde_json::Value>(output.trim())
        .ok()
        .and_then(|v| v.get("version")?.as_str().map(str::to_string))
        .unwrap_or_else(|| output.trim().to_string());
    got == app_version
}

fn command_file() -> &'static str {
    if cfg!(windows) {
        "miaoju.exe"
    } else {
        "miaoju"
    }
}

fn bundled_file() -> &'static str {
    if cfg!(windows) {
        "miaoju-cli.exe"
    } else {
        "miaoju-cli"
    }
}

fn home(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    app.path()
        .home_dir()
        .map_err(|e| format!("无法确定用户主目录：{e}"))
}

fn bin_dir(home: &Path) -> PathBuf {
    home.join(".miaoju").join("bin")
}

fn bundled_cli() -> Result<PathBuf, String> {
    let exe = tauri::utils::platform::current_exe().map_err(|e| format!("无法定位主程序：{e}"))?;
    let p = exe
        .parent()
        .ok_or("无法定位主程序所在目录")?
        .join(bundled_file());
    if p.exists() {
        Ok(p)
    } else {
        Err(format!("安装包里缺少命令行工具：{}", p.display()))
    }
}

fn skill_dir(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .resource_dir()
        .map_err(|e| format!("无法定位资源目录：{e}"))?;
    Ok(dir.join("cli-skill"))
}

fn skill_entry(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    Ok(skill_dir(app)?.join("SKILL.md"))
}

/// 已安装命令的 `version` 输出；最多等 3 秒（Gatekeeper / 杀毒扫描可能卡住），超时按"版本不一致"处理。
fn installed_output(cmd: &Path) -> Option<String> {
    let mut c = std::process::Command::new(cmd);
    c.arg("version");
    // 从 GUI 进程拉起 .cmd 会闪出控制台窗口
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        c.creation_flags(CREATE_NO_WINDOW);
    }
    output_with_timeout(&mut c, std::time::Duration::from_secs(3))
}

fn status(app: &tauri::AppHandle) -> Result<CliToolStatus, String> {
    status_with(app, &[])
}

/// `skill_errs`：本次安装 / 卸载各 agent 的 skill 错误，放进对应的 SkillStatus 里给卡片显示。
fn status_with(
    app: &tauri::AppHandle,
    skill_errs: &[(&str, String)],
) -> Result<CliToolStatus, String> {
    let home = home(app)?;
    let bin = bin_dir(&home);
    let cmd = bin.join(command_file());
    let installed = cmd.exists();
    let app_version = app.package_info().version.to_string();
    let up_to_date =
        installed && installed_output(&cmd).is_some_and(|o| version_matches(&o, &app_version));
    Ok(CliToolStatus {
        installed,
        up_to_date,
        command_path: cmd.display().to_string(),
        bin_dir: bin.display().to_string(),
        skill_entry: skill_entry(app)?.display().to_string(),
        skills: skill_statuses(&home, &skill_dir(app)?, &app_version, skill_errs),
        agent_prompt: agent_prompt(app)?,
    })
}

fn agent_prompt(app: &tauri::AppHandle) -> Result<String, String> {
    // 开发环境里包内 CLI 可能还没构建，提示词照样给出预期路径
    let cli = bundled_cli().or_else(|_| {
        let exe =
            tauri::utils::platform::current_exe().map_err(|e| format!("无法定位主程序：{e}"))?;
        Ok::<_, String>(exe.with_file_name(bundled_file()))
    })?;
    Ok(agent_install_prompt(
        cfg!(windows),
        &cli.display().to_string(),
        &skill_dir(app)?.display().to_string(),
        &app.package_info().version.to_string(),
    ))
}

/// 三个命令都是 async + spawn_blocking：同步命令在 Tauri v2 里跑在主线程，
/// 拉起子进程、改 rc 文件、Windows 广播 WM_SETTINGCHANGE 都可能卡住窗口。
async fn blocking(
    app: tauri::AppHandle,
    f: fn(&tauri::AppHandle) -> Result<CliToolStatus, String>,
) -> Result<CliToolStatus, String> {
    tauri::async_runtime::spawn_blocking(move || f(&app))
        .await
        .map_err(|e| format!("命令行工具操作失败：{e}"))?
}

#[tauri::command]
pub async fn cli_tool_status(app: tauri::AppHandle) -> Result<CliToolStatus, String> {
    blocking(app, status).await
}

#[tauri::command]
pub async fn install_cli_tool(app: tauri::AppHandle) -> Result<CliToolStatus, String> {
    blocking(app, install).await
}

#[tauri::command]
pub async fn uninstall_cli_tool(app: tauri::AppHandle) -> Result<CliToolStatus, String> {
    blocking(app, uninstall).await
}

fn install(app: &tauri::AppHandle) -> Result<CliToolStatus, String> {
    let home = home(app)?;
    let bin = bin_dir(&home);
    std::fs::create_dir_all(&bin).map_err(|e| format!("创建 {} 失败：{e}", bin.display()))?;
    let src = bundled_cli()?;
    let dst = bin.join(command_file());
    let _ = std::fs::remove_file(&dst);
    #[cfg(unix)]
    {
        std::os::unix::fs::symlink(&src, &dst).map_err(|e| format!("创建链接失败：{e}"))?;
        // zsh 是 macOS 默认 shell，.zshrc 不存在就新建；.bash_profile 只在已存在时追加，
        // 新建它会让 bash 跳过用户原有的 ~/.profile。
        edit_rc(&home.join(".zshrc"), true, add_path_block)?;
        edit_rc(&home.join(".bash_profile"), false, add_path_block)?;
    }
    #[cfg(windows)]
    {
        std::fs::copy(&src, &dst).map_err(|e| format!("复制命令行工具失败：{e}"))?;
        let dir = bin.display().to_string();
        win_path::update(|cur| add_to_path_list(cur, &dir))?;
    }
    let version = app.package_info().version.to_string();
    let errs = install_skills(&home, &skill_dir(app)?, cfg!(unix), &version);
    status_with(app, &errs)
}

fn uninstall(app: &tauri::AppHandle) -> Result<CliToolStatus, String> {
    let home = home(app)?;
    let bin = bin_dir(&home);
    let _ = std::fs::remove_file(bin.join(command_file()));
    #[cfg(unix)]
    {
        edit_rc(&home.join(".zshrc"), false, remove_path_block)?;
        edit_rc(&home.join(".bash_profile"), false, remove_path_block)?;
    }
    #[cfg(windows)]
    {
        let dir = bin.display().to_string();
        win_path::update(|cur| remove_from_path_list(cur, &dir))?;
    }
    let errs = uninstall_skills(&home, &skill_dir(app)?);
    status_with(app, &errs)
}

#[cfg(unix)]
fn edit_rc(path: &Path, create: bool, f: fn(&str) -> String) -> Result<(), String> {
    let cur = match std::fs::read_to_string(path) {
        Ok(s) => s,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            if !create {
                return Ok(());
            }
            String::new()
        }
        Err(e) => return Err(format!("读取 {} 失败：{e}", path.display())),
    };
    let next = f(&cur);
    if next != cur {
        write_rc(path, &next).map_err(|e| format!("写入 {} 失败：{e}", path.display()))?;
    }
    Ok(())
}

/// 改写已有的 shell 配置：写同目录临时文件、fsync 后改名。直接 fs::write 会先截断原文件，
/// 写到一半失败（磁盘满、配额）用户的 .zshrc 就只剩半截。~/.zshrc 常是指向 dotfiles 仓库的符号链接：
/// 改写链接指向的真实文件（不把链接换成普通文件），并沿用原文件权限。文件还不存在时直接写。
#[cfg(unix)]
fn write_rc(path: &Path, body: &str) -> std::io::Result<()> {
    use std::io::Write;
    let Ok(real) = std::fs::canonicalize(path) else {
        return std::fs::write(path, body);
    };
    let name = real.file_name().map(|n| n.to_string_lossy().into_owned()).unwrap_or_default();
    let tmp = real.with_file_name(format!(".{name}.miaoju-{}.tmp", uuid::Uuid::new_v4().simple()));
    let r = (|| {
        let mut f = std::fs::OpenOptions::new().write(true).create_new(true).open(&tmp)?;
        f.write_all(body.as_bytes())?;
        f.sync_all()?;
        std::fs::set_permissions(&tmp, std::fs::metadata(&real)?.permissions())?;
        std::fs::rename(&tmp, &real)
    })();
    if r.is_err() {
        let _ = std::fs::remove_file(&tmp);
    }
    r
}

#[cfg(windows)]
mod win_path {
    use winreg::{enums::*, RegKey, RegValue};

    /// 读写 HKCU\Environment\Path，保持原值类型（通常是 REG_EXPAND_SZ，里面可能有 %USERPROFILE%，
    /// 按 REG_SZ 写回会让这些变量失效），写完广播 WM_SETTINGCHANGE 让新开的终端生效。
    pub fn update(f: impl Fn(&str) -> String) -> Result<(), String> {
        let env = RegKey::predef(HKEY_CURRENT_USER)
            .open_subkey_with_flags("Environment", KEY_READ | KEY_WRITE)
            .map_err(|e| format!("打开注册表失败：{e}"))?;
        let (cur, vtype) = match env.get_raw_value("Path") {
            Ok(v) => (
                String::from_utf16_lossy(&utf16(&v.bytes))
                    .trim_end_matches('\0')
                    .to_string(),
                v.vtype,
            ),
            // 只有"值不存在"才当空 Path；其他读取错误若也当空，写回时会冲掉用户原有的 Path。
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => (String::new(), REG_EXPAND_SZ),
            Err(e) => return Err(format!("读取注册表 Path 失败：{e}")),
        };
        let next = f(&cur);
        // 语义相同（只差空项 / 结尾的 `;`）就不写，避免无谓改动用户的 Path。
        if super::normalize_path_list(&next) == super::normalize_path_list(&cur) {
            return Ok(());
        }
        let bytes: Vec<u8> = next
            .encode_utf16()
            .chain(std::iter::once(0))
            .flat_map(|u| u.to_le_bytes())
            .collect();
        env.set_raw_value(
            "Path",
            &RegValue {
                vtype,
                bytes: bytes.into(),
            },
        )
        .map_err(|e| format!("写入注册表失败：{e}"))?;
        broadcast();
        Ok(())
    }

    fn utf16(b: &[u8]) -> Vec<u16> {
        b.chunks_exact(2)
            .map(|c| u16::from_le_bytes([c[0], c[1]]))
            .collect()
    }

    fn broadcast() {
        use windows::core::w;
        use windows::Win32::Foundation::{LPARAM, WPARAM};
        use windows::Win32::UI::WindowsAndMessaging::{
            SendMessageTimeoutW, HWND_BROADCAST, SMTO_ABORTIFHUNG, WM_SETTINGCHANGE,
        };
        unsafe {
            let _ = SendMessageTimeoutW(
                HWND_BROADCAST,
                WM_SETTINGCHANGE,
                WPARAM(0),
                LPARAM(w!("Environment").as_ptr() as isize),
                SMTO_ABORTIFHUNG,
                2000,
                None,
            );
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn path_block_is_idempotent_and_removable() {
        let orig = "export FOO=1\n";
        let once = add_path_block(orig);
        assert!(once.starts_with(orig));
        assert!(once.contains(PATH_LINE));
        assert_eq!(add_path_block(&once), once, "重复安装不能重复追加");
        assert_eq!(remove_path_block(&once), orig);
        assert_eq!(remove_path_block(orig), orig);
    }

    #[test]
    fn path_block_handles_missing_trailing_newline() {
        let once = add_path_block("export FOO=1");
        assert!(once.contains("export FOO=1\n# >>> miaoju cli >>>"));
    }

    #[test]
    fn windows_path_list_dedupes_case_insensitively() {
        let dir = r"C:\Users\u\.miaoju\bin";
        assert_eq!(add_to_path_list("", dir), dir);
        assert_eq!(
            add_to_path_list(r"C:\a;C:\b", dir),
            format!(r"C:\a;C:\b;{dir}")
        );
        let with = format!(r"C:\a;{}", dir.to_uppercase());
        assert_eq!(add_to_path_list(&with, dir), with);
        assert_eq!(
            remove_from_path_list(&format!(r"C:\a;{dir};C:\b"), dir),
            r"C:\a;C:\b"
        );
        assert_eq!(remove_from_path_list(r"C:\a", dir), r"C:\a");
    }

    #[test]
    fn version_match_requires_app_version_in_output() {
        assert!(version_matches("1.2.0\n", "1.2.0"));
        assert!(version_matches("{\n  \"version\": \"1.2.0\"\n}\n", "1.2.0"));
        assert!(!version_matches("1.1.9", "1.2.0"));
        assert!(!version_matches("", "1.2.0"));
        assert!(!version_matches("{\"version\":\"1.2.0\"}", ""));
        // 子串不算：1.2.10 不是 1.2.1，11.2.0 也不是 1.2.0
        assert!(!version_matches("{\"version\":\"1.2.10\"}", "1.2.1"));
        assert!(!version_matches("{\"version\":\"11.2.0\"}", "1.2.0"));
    }

    #[test]
    fn normalize_path_list_ignores_empty_items() {
        assert_eq!(normalize_path_list(r"C:\a;;C:\b;"), r"C:\a;C:\b");
        assert_eq!(normalize_path_list(""), "");
        assert_eq!(normalize_path_list(";"), "");
        assert_eq!(normalize_path_list(r"C:\a"), r"C:\a");
    }

    // ---- AI 助手 skill ----

    fn skill_src(root: &Path) -> PathBuf {
        let src = root.join("res").join("cli-skill");
        std::fs::create_dir_all(src.join("examples")).unwrap();
        std::fs::write(src.join("SKILL.md"), "---\nname: miaoju-cli\n---\n").unwrap();
        std::fs::write(src.join("examples").join("a.md"), "a").unwrap();
        src
    }

    fn by_agent<'a>(v: &'a [SkillStatus], agent: &str) -> &'a SkillStatus {
        v.iter().find(|s| s.agent == agent).unwrap()
    }

    fn present(p: &Path) -> bool {
        std::fs::symlink_metadata(p).is_ok()
    }

    fn statuses(home: &Path, src: &Path) -> Vec<SkillStatus> {
        skill_statuses(home, src, "1.2.0", &[])
    }

    #[test]
    fn skill_skips_agent_without_home_dir() {
        let t = tempfile::tempdir().unwrap();
        let home = t.path().join("home");
        std::fs::create_dir_all(&home).unwrap();
        let src = skill_src(t.path());
        for link in [true, false] {
            assert!(install_skills(&home, &src, link, "1.2.0").is_empty());
            assert!(!present(&home.join(".claude")), "不能替用户建 ~/.claude");
            assert!(!present(&home.join(".codex")));
            let st = statuses(&home, &src);
            assert_eq!(st.len(), 2);
            for s in &st {
                assert!(!s.available && !s.installed && !s.conflict, "{s:?}");
            }
        }
        assert!(by_agent(&statuses(&home, &src), "claude")
            .path
            .ends_with(&format!(
                ".claude{0}skills{0}miaoju-cli",
                std::path::MAIN_SEPARATOR
            )));
    }

    fn install_twice_then_uninstall(link: bool) {
        let t = tempfile::tempdir().unwrap();
        let home = t.path().join("home");
        std::fs::create_dir_all(home.join(".claude")).unwrap();
        std::fs::create_dir_all(home.join(".codex")).unwrap();
        let src = skill_src(t.path());
        assert!(install_skills(&home, &src, link, "1.2.0").is_empty());
        assert!(install_skills(&home, &src, link, "1.2.0").is_empty()); // 幂等
        for agent in [".claude", ".codex"] {
            let dest = home.join(agent).join("skills").join("miaoju-cli");
            assert!(dest.join("SKILL.md").is_file());
            assert!(dest.join("examples").join("a.md").is_file());
            if link {
                assert!(std::fs::symlink_metadata(&dest)
                    .unwrap()
                    .file_type()
                    .is_symlink());
            } else {
                assert_eq!(
                    std::fs::read_to_string(dest.join(MANAGED_MARK)).unwrap(),
                    "1.2.0"
                );
            }
        }
        for s in statuses(&home, &src) {
            assert!(
                s.available && s.installed && !s.conflict && !s.outdated && s.error.is_none(),
                "{s:?}"
            );
        }
        assert!(uninstall_skills(&home, &src).is_empty());
        for agent in [".claude", ".codex"] {
            let dest = home.join(agent).join("skills").join("miaoju-cli");
            assert!(!present(&dest));
        }
        assert!(
            src.join("SKILL.md").is_file(),
            "卸载不能删到安装包里的 skill"
        );
        for s in statuses(&home, &src) {
            assert!(s.available && !s.installed && !s.conflict, "{s:?}");
        }
    }

    #[cfg(unix)]
    #[test]
    fn skill_symlink_install_is_idempotent_and_uninstallable() {
        install_twice_then_uninstall(true);
    }

    #[test]
    fn skill_copy_install_is_idempotent_and_uninstallable() {
        install_twice_then_uninstall(false);
    }

    #[test]
    fn skill_never_touches_user_owned_dir() {
        let t = tempfile::tempdir().unwrap();
        let home = t.path().join("home");
        let dest = home.join(".claude").join("skills").join("miaoju-cli");
        std::fs::create_dir_all(&dest).unwrap();
        std::fs::write(dest.join("SKILL.md"), "用户自己的").unwrap();
        let src = skill_src(t.path());
        for link in [true, false] {
            assert!(install_skills(&home, &src, link, "1.2.0").is_empty());
            assert_eq!(
                std::fs::read_to_string(dest.join("SKILL.md")).unwrap(),
                "用户自己的"
            );
            assert!(!present(&dest.join(MANAGED_MARK)));
            let st = statuses(&home, &src);
            let c = by_agent(&st, "claude");
            assert!(c.available && !c.installed && c.conflict, "{c:?}");
            assert!(uninstall_skills(&home, &src).is_empty());
            assert_eq!(
                std::fs::read_to_string(dest.join("SKILL.md")).unwrap(),
                "用户自己的"
            );
        }
    }

    #[test]
    fn skill_user_owned_file_is_conflict() {
        let t = tempfile::tempdir().unwrap();
        let home = t.path().join("home");
        let skills = home.join(".codex").join("skills");
        std::fs::create_dir_all(&skills).unwrap();
        std::fs::write(skills.join("miaoju-cli"), "x").unwrap();
        let src = skill_src(t.path());
        install_skills(&home, &src, false, "1.2.0");
        uninstall_skills(&home, &src);
        assert_eq!(
            std::fs::read_to_string(skills.join("miaoju-cli")).unwrap(),
            "x"
        );
        assert!(by_agent(&statuses(&home, &src), "codex").conflict);
    }

    #[cfg(unix)]
    #[test]
    fn skill_user_symlink_elsewhere_is_conflict() {
        let t = tempfile::tempdir().unwrap();
        let home = t.path().join("home");
        let skills = home.join(".claude").join("skills");
        std::fs::create_dir_all(&skills).unwrap();
        let mine = t.path().join("my-skill");
        std::fs::create_dir_all(&mine).unwrap();
        let dest = skills.join("miaoju-cli");
        std::os::unix::fs::symlink(&mine, &dest).unwrap();
        let src = skill_src(t.path());
        assert!(install_skills(&home, &src, true, "1.2.0").is_empty());
        assert_eq!(
            std::fs::read_link(&dest).unwrap(),
            mine,
            "用户的链接不能被替换"
        );
        let st = statuses(&home, &src);
        let c = by_agent(&st, "claude");
        assert!(c.conflict && !c.installed, "{c:?}");
        uninstall_skills(&home, &src);
        assert_eq!(
            std::fs::read_link(&dest).unwrap(),
            mine,
            "用户的链接不能被卸载"
        );
    }

    #[cfg(unix)]
    #[test]
    fn skill_dangling_symlink_is_replaced_and_uninstalled() {
        let t = tempfile::tempdir().unwrap();
        let home = t.path().join("home");
        let skills = home.join(".claude").join("skills");
        std::fs::create_dir_all(&skills).unwrap();
        let dest = skills.join("miaoju-cli");
        // 旧版应用被移走后留下的悬空链接（指向旧位置的 cli-skill）
        let old = t.path().join("old.app").join("cli-skill");
        std::os::unix::fs::symlink(&old, &dest).unwrap();
        let src = skill_src(t.path());
        let s = statuses(&home, &src);
        let c = by_agent(&s, "claude");
        assert!(c.available && !c.installed && !c.conflict, "{c:?}");
        assert!(install_skills(&home, &src, true, "1.2.0").is_empty());
        assert_eq!(std::fs::read_link(&dest).unwrap(), src);
        assert!(by_agent(&statuses(&home, &src), "claude").installed);

        std::fs::remove_file(&dest).unwrap();
        std::os::unix::fs::symlink(&old, &dest).unwrap();
        assert!(uninstall_skills(&home, &src).is_empty());
        assert!(!present(&dest));
    }

    #[test]
    fn skill_copy_reports_outdated_version() {
        let t = tempfile::tempdir().unwrap();
        let home = t.path().join("home");
        std::fs::create_dir_all(home.join(".codex")).unwrap();
        let src = skill_src(t.path());
        install_skills(&home, &src, false, "1.1.0");
        let st = skill_statuses(&home, &src, "1.2.0", &[]);
        let c = by_agent(&st, "codex");
        assert!(c.installed && c.outdated, "{c:?}");
        install_skills(&home, &src, false, "1.2.0");
        assert!(!by_agent(&skill_statuses(&home, &src, "1.2.0", &[]), "codex").outdated);
    }

    #[test]
    fn skill_failure_of_one_agent_does_not_block_other() {
        let t = tempfile::tempdir().unwrap();
        let home = t.path().join("home");
        std::fs::create_dir_all(home.join(".claude")).unwrap();
        std::fs::create_dir_all(home.join(".codex")).unwrap();
        // ~/.claude/skills 是个文件：建不了 skills 目录
        std::fs::write(home.join(".claude").join("skills"), "x").unwrap();
        let src = skill_src(t.path());
        let errs = install_skills(&home, &src, false, "1.2.0");
        assert_eq!(errs.len(), 1);
        assert_eq!(errs[0].0, "claude");
        let st = skill_statuses(&home, &src, "1.2.0", &errs);
        let c = by_agent(&st, "claude");
        assert!(c.error.is_some() && !c.installed, "{c:?}");
        let x = by_agent(&st, "codex");
        assert!(x.installed && x.error.is_none(), "{x:?}");
    }

    #[test]
    fn skill_status_serializes_camel_case() {
        let v = serde_json::to_value(SkillStatus {
            agent: "claude".into(),
            path: "/p".into(),
            available: true,
            installed: false,
            conflict: false,
            outdated: false,
            error: None,
        })
        .unwrap();
        assert_eq!(
            v,
            serde_json::json!({"agent":"claude","path":"/p","available":true,"installed":false,"conflict":false,"outdated":false,"error":null})
        );
    }

    // ---- 给 AI 助手的安装提示词 ----

    #[test]
    fn agent_prompt_macos_quotes_real_paths_and_uses_markers() {
        let cli = "/Applications/喵剧 Beta.app/Contents/MacOS/miaoju-cli";
        let skill = "/Applications/喵剧 Beta.app/Contents/Resources/cli-skill";
        let p = agent_install_prompt(false, cli, skill, "1.2.0");
        assert!(
            p.contains(&format!(r#"ln -sf '{cli}' ~/.miaoju/bin/miaoju"#)),
            "{p}"
        );
        for agent in [".claude", ".codex"] {
            assert!(
                p.contains(&format!(
                    r#"d=~/{agent}/skills/miaoju-cli; if {{ [ -L "$d" ] && [ "$(readlink "$d")" != '{skill}' ] && [ "$(basename "$(readlink "$d")")" != cli-skill ]; }} || {{ [ -e "$d" ] && [ ! -L "$d" ] && [ ! -e "$d/{MANAGED_MARK}" ]; }}; then echo "已存在同名目录，未覆盖：$d"; else mkdir -p ~/{agent}/skills && rm -rf "$d" && ln -sfn '{skill}' "$d"; fi"#
                )),
                "缺少 {agent} 的同名目录判断：{p}"
            );
        }
        assert!(p.contains(&format!(r#"'{skill}/SKILL.md'"#)), "{p}");
        assert!(p.contains(&format!(r#""{MARK_BEGIN}""#)), "{p}");
        for line in [MARK_BEGIN, PATH_LINE, MARK_END] {
            assert!(
                p.lines().any(|l| l == line),
                "缺少不带缩进的标记行 {line}：{p}"
            );
        }
        assert!(p.contains("~/.miaoju/bin/miaoju version"));
        assert!(p.contains(SANDBOX_NOTE), "{p}");
        assert!(p.contains("~/.zshrc"));
    }

    /// 提示词装出来的复制件，标记里必须是应用版本号，否则卡片会一直显示"需要重新安装"。
    #[test]
    fn agent_prompt_windows_marker_is_app_version_and_not_outdated() {
        let p = agent_install_prompt(true, r"C:\a\miaoju-cli.exe", r"C:\a\cli-skill", "1.3.0");
        let key = format!(r#"Set-Content -LiteralPath "$d\{MANAGED_MARK}" -Value ""#);
        let at = p.find(&key).expect("提示词里要写标记文件") + key.len();
        let written = &p[at..at + p[at..].find('"').unwrap()];
        assert_eq!(written, "1.3.0");
        assert!(!p.contains(" managed"), "{p}");

        // 按提示词的写法（Set-Content 会带换行）落一份复制件，状态不能是 outdated
        let t = tempfile::tempdir().unwrap();
        let home = t.path().join("home");
        let src = skill_src(t.path());
        let dest = home.join(".codex").join("skills").join("miaoju-cli");
        copy_dir_all(&src, &dest).unwrap();
        std::fs::write(dest.join(MANAGED_MARK), format!("{written}\r\n")).unwrap();
        let st = skill_statuses(&home, &src, "1.3.0", &[]);
        let c = by_agent(&st, "codex");
        assert!(c.installed && !c.outdated && !c.conflict, "{c:?}");
    }

    #[test]
    fn agent_prompt_windows_quotes_real_paths() {
        let cli = r"C:\Program Files\喵剧\miaoju-cli.exe";
        let skill = r"C:\Program Files\喵剧\cli-skill";
        let p = agent_install_prompt(true, cli, skill, "1.2.0");
        assert!(
            p.contains(&format!(
                r#"Copy-Item -LiteralPath '{cli}' "$env:USERPROFILE\.miaoju\bin\miaoju.exe" -Force"#
            )),
            "{p}"
        );
        assert!(
            p.contains(r#"New-Item -ItemType Directory -Force "$env:USERPROFILE\.miaoju\bin""#),
            "{p}"
        );
        // 用户 Path 走注册表：保留 REG_EXPAND_SZ 和未展开的 %USERPROFILE%
        assert!(
            p.contains("[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment',$true)"),
            "{p}"
        );
        assert!(
            p.contains("GetValue('Path','','DoNotExpandEnvironmentNames')"),
            "{p}"
        );
        assert!(p.contains("GetValueKind('Path')"), "{p}");
        assert!(p.contains("'ExpandString'"), "{p}");
        assert!(p.contains("$k.SetValue('Path'"), "{p}");
        assert!(
            !p.contains("GetEnvironmentVariable('Path'")
                && !p.contains("SetEnvironmentVariable('Path'"),
            "{p}"
        );
        assert!(
            p.contains("SetEnvironmentVariable('MIAOJU_TMP',$null,'User')"),
            "{p}"
        );
        for agent in [".claude", ".codex"] {
            assert!(
                p.contains(&format!(
                    r#"$d = "$env:USERPROFILE\{agent}\skills\miaoju-cli"; if ((Test-Path -LiteralPath $d) -and -not (Test-Path -LiteralPath "$d\{MANAGED_MARK}")) {{ Write-Error "已存在同名目录，未覆盖：$d" }} else {{ if (Test-Path -LiteralPath $d) {{ Remove-Item -LiteralPath $d -Recurse -Force }}; New-Item -ItemType Directory -Force "$env:USERPROFILE\{agent}\skills" | Out-Null; Copy-Item -LiteralPath '{skill}' -Destination $d -Recurse -Force; Set-Content -LiteralPath "$d\{MANAGED_MARK}" -Value "1.2.0" }}"#
                )),
                "缺少 {agent} 的同名目录判断：{p}"
            );
        }
        assert!(p.contains(&format!(r#"'{skill}\SKILL.md'"#)), "{p}");
        assert!(p.contains("miaoju version"), "{p}");
        assert!(p.contains(SANDBOX_NOTE), "{p}");
        assert!(!p.contains("zshrc") && !p.contains("ln -s"), "{p}");
    }

    /// 改 rc 文件：经符号链接改写真实文件（链接保留）、沿用权限、不留临时文件；不存在且 create=false 时不新建。
    #[cfg(unix)]
    #[test]
    fn edit_rc_follows_symlink_and_keeps_mode() {
        use std::os::unix::fs::PermissionsExt;
        let t = tempfile::tempdir().unwrap();
        let dotfiles = t.path().join("dotfiles");
        std::fs::create_dir_all(&dotfiles).unwrap();
        let real = dotfiles.join("zshrc");
        std::fs::write(&real, "export FOO=1\n").unwrap();
        std::fs::set_permissions(&real, std::fs::Permissions::from_mode(0o600)).unwrap();
        let home = t.path().join("home");
        std::fs::create_dir_all(&home).unwrap();
        let rc = home.join(".zshrc");
        std::os::unix::fs::symlink(&real, &rc).unwrap();

        edit_rc(&rc, true, add_path_block).unwrap();
        assert!(std::fs::symlink_metadata(&rc).unwrap().file_type().is_symlink(), "链接不能被换成普通文件");
        let body = std::fs::read_to_string(&real).unwrap();
        assert!(body.starts_with("export FOO=1\n") && body.contains(PATH_LINE), "{body}");
        assert_eq!(std::fs::metadata(&real).unwrap().permissions().mode() & 0o777, 0o600);
        assert_eq!(std::fs::read_dir(&dotfiles).unwrap().count(), 1, "不应留下临时文件");

        edit_rc(&rc, false, remove_path_block).unwrap();
        assert_eq!(std::fs::read_to_string(&real).unwrap(), "export FOO=1\n");

        let missing = home.join(".bash_profile");
        edit_rc(&missing, false, add_path_block).unwrap();
        assert!(!missing.exists(), "不存在的 .bash_profile 不能替用户新建");
        let fresh = t.path().join("fresh").join(".zshrc");
        std::fs::create_dir_all(fresh.parent().unwrap()).unwrap();
        edit_rc(&fresh, true, add_path_block).unwrap();
        assert!(std::fs::read_to_string(&fresh).unwrap().contains(PATH_LINE));
    }

    #[test]
    fn quote_helpers_make_literals() {
        assert_eq!(sh_quote("/a b/喵剧.app"), "'/a b/喵剧.app'");
        assert_eq!(sh_quote("it's"), r"'it'\''s'");
        assert_eq!(ps_quote(r"C:\x$y\`z"), r"'C:\x$y\`z'");
        assert_eq!(ps_quote("O'Brien\u{2019}s"), "'O''Brien\u{2019}\u{2019}s'");
    }

    /// 端到端：按提示词里 Claude Code 那一步真的跑一遍 sh，路径里的 $、反引号、单引号都按字面落地，
    /// 不会被展开成变量或命令。
    #[cfg(unix)]
    #[test]
    fn agent_prompt_macos_skill_step_runs_with_hostile_path() {
        let t = tempfile::tempdir().unwrap();
        let home = t.path().join("home");
        std::fs::create_dir_all(home.join(".claude")).unwrap();
        let src = t.path().join("喵剧 $HOME `id` it's").join("cli-skill");
        std::fs::create_dir_all(&src).unwrap();
        let skill = src.to_str().unwrap();
        let p = agent_install_prompt(false, "/x/miaoju-cli", skill, "1.2.0");
        let line = p.lines().find_map(|l| l.split_once("如果你是 Claude Code：")).unwrap().1;
        let st = std::process::Command::new("/bin/sh")
            .arg("-c")
            .arg(line)
            .env("HOME", &home)
            .status()
            .unwrap();
        assert!(st.success());
        let dest = home.join(".claude/skills/miaoju-cli");
        assert_eq!(std::fs::read_link(&dest).unwrap(), src);
        assert!(p.contains(&format!("ln -sf {} ~/.miaoju/bin/miaoju", sh_quote("/x/miaoju-cli"))), "{p}");
    }

    #[cfg(unix)]
    fn script(dir: &std::path::Path, body: &str) -> std::path::PathBuf {
        use std::os::unix::fs::PermissionsExt;
        let p = dir.join("fake-cli");
        std::fs::write(&p, format!("#!/bin/sh\n{body}\n")).unwrap();
        std::fs::set_permissions(&p, std::fs::Permissions::from_mode(0o755)).unwrap();
        p
    }

    #[cfg(unix)]
    #[test]
    fn output_with_timeout_returns_stdout() {
        let dir = tempfile::tempdir().unwrap();
        let p = script(dir.path(), "echo 1.2.0");
        let out = output_with_timeout(
            std::process::Command::new(&p).arg("version"),
            std::time::Duration::from_secs(3),
        );
        assert_eq!(out.as_deref(), Some("1.2.0\n"));
    }

    #[cfg(unix)]
    #[test]
    fn output_with_timeout_none_on_failure() {
        let dir = tempfile::tempdir().unwrap();
        let p = script(dir.path(), "echo 1.2.0; exit 3");
        assert!(output_with_timeout(
            &mut std::process::Command::new(&p),
            std::time::Duration::from_secs(3)
        )
        .is_none());
    }

    #[cfg(unix)]
    #[test]
    fn output_with_timeout_kills_hung_process() {
        let dir = tempfile::tempdir().unwrap();
        let p = script(dir.path(), "echo started; exec sleep 30");
        let t = std::time::Instant::now();
        let out = output_with_timeout(
            &mut std::process::Command::new(&p),
            std::time::Duration::from_millis(300),
        );
        assert!(out.is_none());
        assert!(
            t.elapsed() < std::time::Duration::from_secs(5),
            "超时后必须及时返回：{:?}",
            t.elapsed()
        );
    }
}
