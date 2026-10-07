//! 设置 → 配置 → 命令行工具（CLI spec 7.2）：把安装包里的 miaoju-cli 装成终端里的 `miaoju`。
//! macOS：~/.miaoju/bin/miaoju 符号链接到包内文件，~/.zshrc（及已存在的 ~/.bash_profile）追加带标记的 PATH 行。
//! Windows：复制到 %USERPROFILE%\.miaoju\bin\miaoju.exe（普通用户建不了符号链接），写用户级 Path。

use serde::Serialize;
use std::path::{Path, PathBuf};
use tauri::Manager;

#[cfg_attr(not(unix), allow(dead_code))]
const MARK_BEGIN: &str = "# >>> miaoju cli >>>";
#[cfg_attr(not(unix), allow(dead_code))]
const MARK_END: &str = "# <<< miaoju cli <<<";
#[cfg_attr(not(unix), allow(dead_code))]
const PATH_LINE: &str = r#"export PATH="$HOME/.miaoju/bin:$PATH""#;

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CliToolStatus {
    installed: bool,
    up_to_date: bool,
    command_path: String,
    bin_dir: String,
    skill_entry: String,
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

pub fn version_matches(output: &str, app_version: &str) -> bool {
    !app_version.is_empty() && output.contains(app_version)
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

fn skill_entry(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .resource_dir()
        .map_err(|e| format!("无法定位资源目录：{e}"))?;
    Ok(dir.join("cli-skill").join("SKILL.md"))
}

fn installed_output(cmd: &Path) -> Option<String> {
    let out = std::process::Command::new(cmd)
        .arg("version")
        .output()
        .ok()?;
    out.status
        .success()
        .then(|| String::from_utf8_lossy(&out.stdout).into_owned())
}

fn status(app: &tauri::AppHandle) -> Result<CliToolStatus, String> {
    let bin = bin_dir(&home(app)?);
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
    })
}

#[tauri::command]
pub fn cli_tool_status(app: tauri::AppHandle) -> Result<CliToolStatus, String> {
    status(&app)
}

#[tauri::command]
pub fn install_cli_tool(app: tauri::AppHandle) -> Result<CliToolStatus, String> {
    let home = home(&app)?;
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
    status(&app)
}

#[tauri::command]
pub fn uninstall_cli_tool(app: tauri::AppHandle) -> Result<CliToolStatus, String> {
    let home = home(&app)?;
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
    status(&app)
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
        std::fs::write(path, next).map_err(|e| format!("写入 {} 失败：{e}", path.display()))?;
    }
    Ok(())
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
            Err(_) => (String::new(), REG_EXPAND_SZ),
        };
        let next = f(&cur);
        if next == cur {
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
        assert!(!version_matches("1.1.9", "1.2.0"));
        assert!(!version_matches("", "1.2.0"));
    }
}
