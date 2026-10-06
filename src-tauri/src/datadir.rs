//! 数据目录的位置记录与启动前迁移（spec S2）。
//! 迁移本身由 sidecar 子命令完成（Go 侧有 SQLite 的 VACUUM INTO），这里只负责：
//! 读写指针文件、按顺序调用子命令、把结果写回指针文件、提供设置页用的命令。

use std::path::{Path, PathBuf};
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use tauri::{Manager, State};
use tauri_plugin_shell::ShellExt;

/// 与 Go `datadir.PointerFileName` 一致。
pub const POINTER_FILE: &str = "data-location.json";

#[derive(Default, Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Location {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data_dir: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pending_data_dir: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cleanup_data_dir: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_error: Option<String>,
}

/// 指针文件不存在 → 默认值；存在但损坏 → 报错（不能悄悄回到默认目录，用户会以为数据丢了）。
pub fn read_location(path: &Path) -> Result<Location, String> {
    match std::fs::read_to_string(path) {
        Ok(s) => serde_json::from_str(&s)
            .map_err(|e| format!("数据目录配置文件已损坏（{}）：{e}", path.display())),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(Location::default()),
        Err(e) => Err(format!("无法读取数据目录配置文件（{}）：{e}", path.display())),
    }
}

/// 先写临时文件再改名，避免写一半断电留下损坏的指针文件。
pub fn write_location(path: &Path, loc: &Location) -> Result<(), String> {
    let tmp = path.with_extension("json.tmp");
    let body = serde_json::to_string_pretty(loc).map_err(|e| e.to_string())?;
    std::fs::write(&tmp, body).map_err(|e| format!("无法写入数据目录配置：{e}"))?;
    std::fs::rename(&tmp, path).map_err(|e| format!("无法写入数据目录配置：{e}"))
}

/// 与 Go `db.Open` / `datadir.ValidateTarget` 相同的语法限制（完整校验在 sidecar 的 /data-dir/check）。
pub fn check_path_syntax(p: &str) -> Result<(), String> {
    if p.trim().is_empty() {
        return Err("请选择新的数据目录".into());
    }
    if p.contains('?') || p.starts_with("file:") {
        return Err(format!("数据目录路径不能包含 \"?\"，也不能以 \"file:\" 开头：{p}"));
    }
    if !Path::new(p).is_absolute() {
        return Err(format!("请选择绝对路径：{p}"));
    }
    Ok(())
}

pub fn current_dir(default: &Path, loc: &Location) -> PathBuf {
    match loc.data_dir.as_deref() {
        Some(d) if !d.is_empty() => PathBuf::from(d),
        _ => default.to_path_buf(),
    }
}

#[derive(Debug, PartialEq)]
pub enum Step {
    Migrate { from: PathBuf, to: PathBuf },
    Cleanup { old: PathBuf, new: PathBuf },
}

/// 启动时下一步要做什么。after_* 总会清掉对应字段，所以循环必然结束。
pub fn next_step(default: &Path, loc: &Location) -> Option<Step> {
    if let Some(to) = loc.pending_data_dir.as_deref().filter(|s| !s.is_empty()) {
        return Some(Step::Migrate { from: current_dir(default, loc), to: PathBuf::from(to) });
    }
    if let Some(old) = loc.cleanup_data_dir.as_deref().filter(|s| !s.is_empty()) {
        return Some(Step::Cleanup { old: PathBuf::from(old), new: current_dir(default, loc) });
    }
    None
}

pub fn after_migrate(loc: &Location, from: &Path, result: Result<(), String>) -> Location {
    let mut next = loc.clone();
    let to = next.pending_data_dir.take();
    match result {
        Ok(()) => {
            next.data_dir = to;
            next.cleanup_data_dir = Some(from.to_string_lossy().into_owned());
            next.last_error = None;
        }
        Err(e) => next.last_error = Some(format!("数据目录迁移失败，仍在使用原目录：{e}")),
    }
    next
}

pub fn after_cleanup(loc: &Location, result: Result<(), String>) -> Location {
    let mut next = loc.clone();
    let old = next.cleanup_data_dir.take().unwrap_or_default();
    if let Err(e) = result {
        next.last_error = Some(format!("旧数据目录 {old} 未能清理干净，可以手动删除：{e}"));
    }
    next
}

/// 自定义数据目录不存在时报错，绝不自动新建（可能是移动硬盘没插）；默认目录照常创建。
pub fn ensure_current_exists(default: &Path, loc: &Location) -> Result<PathBuf, String> {
    let cur = current_dir(default, loc);
    if cur.as_path() != default && !cur.is_dir() {
        return Err(format!(
            "数据目录不存在：{}。如果它在移动硬盘上，请连接后重新打开应用。",
            cur.display()
        ));
    }
    Ok(cur)
}

pub fn last_line(s: &str) -> Option<String> {
    s.lines().rev().map(str::trim).find(|l| !l.is_empty()).map(String::from)
}

/// 设置页需要的数据目录状态，启动时放进 Tauri 托管状态。
pub struct DataDirState {
    pub default_dir: PathBuf,
    pub current: PathBuf,
    pub pointer: PathBuf,
    pub last_error: Mutex<Option<String>>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DataDirStatus {
    current: String,
    default_dir: String,
    last_error: Option<String>,
}

#[tauri::command]
pub fn data_dir_status(state: State<'_, DataDirState>) -> DataDirStatus {
    DataDirStatus {
        current: state.current.to_string_lossy().into_owned(),
        default_dir: state.default_dir.to_string_lossy().into_owned(),
        last_error: state.last_error.lock().unwrap().clone(),
    }
}

#[tauri::command]
pub fn dismiss_data_dir_error(state: State<'_, DataDirState>) -> Result<(), String> {
    let mut loc = read_location(&state.pointer)?;
    loc.last_error = None;
    write_location(&state.pointer, &loc)?;
    *state.last_error.lock().unwrap() = None;
    Ok(())
}

/// 调一次 sidecar 子命令并等它结束。
fn run_sidecar_once(app: &tauri::AppHandle, args: Vec<String>) -> Result<(), String> {
    let cmd = app
        .shell()
        .sidecar("sidecar")
        .map_err(|e| format!("找不到 sidecar 程序：{e}"))?
        .args(args);
    let out = tauri::async_runtime::block_on(cmd.output())
        .map_err(|e| format!("无法启动 sidecar：{e}"))?;
    if out.status.success() {
        return Ok(());
    }
    Err(last_line(&String::from_utf8_lossy(&out.stderr))
        .unwrap_or_else(|| format!("sidecar 退出码 {:?}", out.status.code())))
}

/// 启动 sidecar 之前调用：执行待办的迁移 / 清理，返回本次要用的数据目录。
/// 无论成功与否都返回 DataDirState（命令需要它）；Err 里是给用户看的原因。
pub fn prepare(app: &tauri::AppHandle) -> (DataDirState, Result<PathBuf, String>) {
    let default = match app.path().app_data_dir() {
        Ok(d) => d,
        Err(e) => {
            let state = DataDirState { default_dir: PathBuf::new(), current: PathBuf::new(), pointer: PathBuf::new(), last_error: Mutex::new(None) };
            return (state, Err(format!("无法确定数据目录：{e}")));
        }
    };
    let pointer = app
        .path()
        .app_config_dir()
        .unwrap_or_else(|_| default.clone())
        .join(POINTER_FILE);
    let result = (|| -> Result<(PathBuf, Location), String> {
        std::fs::create_dir_all(&default).map_err(|e| format!("无法创建数据目录：{e}"))?;
        if let Some(dir) = pointer.parent() {
            std::fs::create_dir_all(dir).map_err(|e| format!("无法创建配置目录：{e}"))?;
        }
        let mut loc = read_location(&pointer)?;
        while let Some(step) = next_step(&default, &loc) {
            loc = match step {
                Step::Migrate { from, to } => {
                    let r = run_sidecar_once(app, vec![
                        "migrate-data-dir".into(), "--from".into(), from.to_string_lossy().into_owned(),
                        "--to".into(), to.to_string_lossy().into_owned(),
                    ]);
                    after_migrate(&loc, &from, r)
                }
                Step::Cleanup { old, new } => {
                    let r = run_sidecar_once(app, vec![
                        "cleanup-data-dir".into(), "--old".into(), old.to_string_lossy().into_owned(),
                        "--new".into(), new.to_string_lossy().into_owned(),
                    ]);
                    after_cleanup(&loc, r)
                }
            };
            write_location(&pointer, &loc)?;
        }
        Ok((ensure_current_exists(&default, &loc)?, loc))
    })();
    match result {
        Ok((current, loc)) => {
            let state = DataDirState { default_dir: default, current: current.clone(), pointer, last_error: Mutex::new(loc.last_error) };
            (state, Ok(current))
        }
        Err(e) => {
            let state = DataDirState { default_dir: default.clone(), current: default, pointer, last_error: Mutex::new(Some(e.clone())) };
            (state, Err(e))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    fn tmp() -> PathBuf {
        let p = std::env::temp_dir().join(format!("miaoju-datadir-{}", uuid::Uuid::new_v4().simple()));
        std::fs::create_dir_all(&p).unwrap();
        p
    }

    #[test]
    fn missing_pointer_is_default() {
        let dir = tmp();
        assert_eq!(read_location(&dir.join(POINTER_FILE)).unwrap(), Location::default());
    }

    #[test]
    fn corrupt_pointer_is_error() {
        let dir = tmp();
        let p = dir.join(POINTER_FILE);
        std::fs::write(&p, "{not json").unwrap();
        let err = read_location(&p).unwrap_err();
        assert!(err.contains("损坏"), "{err}");
    }

    #[test]
    fn write_then_read_roundtrip_and_camel_case() {
        let dir = tmp();
        let p = dir.join(POINTER_FILE);
        let loc = Location { data_dir: Some("/a".into()), pending_data_dir: Some("/b".into()), ..Default::default() };
        write_location(&p, &loc).unwrap();
        let raw = std::fs::read_to_string(&p).unwrap();
        assert!(raw.contains("pendingDataDir") && !raw.contains("lastError"), "{raw}");
        assert_eq!(read_location(&p).unwrap(), loc);
        assert!(!dir.join("data-location.json.tmp").exists());
    }

    #[test]
    fn check_path_syntax_rules() {
        assert!(check_path_syntax("").is_err());
        assert!(check_path_syntax("/tmp/a?b").is_err());
        assert!(check_path_syntax("file:/tmp/x").is_err());
        assert!(check_path_syntax("relative/x").is_err());
        let abs = std::env::temp_dir().join("ok");
        assert!(check_path_syntax(&abs.to_string_lossy()).is_ok());
    }

    #[test]
    fn next_step_migrate_then_cleanup_then_none() {
        let def = PathBuf::from("/default");
        let mut loc = Location { pending_data_dir: Some("/new".into()), ..Default::default() };
        assert_eq!(
            next_step(&def, &loc),
            Some(Step::Migrate { from: def.clone(), to: PathBuf::from("/new") })
        );
        loc = after_migrate(&loc, &def, Ok(()));
        assert_eq!(loc.data_dir.as_deref(), Some("/new"));
        assert_eq!(loc.pending_data_dir, None);
        assert_eq!(
            next_step(&def, &loc),
            Some(Step::Cleanup { old: def.clone(), new: PathBuf::from("/new") })
        );
        loc = after_cleanup(&loc, Ok(()));
        assert_eq!(next_step(&def, &loc), None);
        assert_eq!(current_dir(&def, &loc), PathBuf::from("/new"));
    }

    #[test]
    fn next_step_failed_migration_keeps_old_dir() {
        let def = PathBuf::from("/default");
        let loc = Location { data_dir: Some("/old".into()), pending_data_dir: Some("/new".into()), ..Default::default() };
        let after = after_migrate(&loc, &PathBuf::from("/old"), Err("所选文件夹不是空的".into()));
        assert_eq!(after.pending_data_dir, None);
        assert_eq!(current_dir(&def, &after), PathBuf::from("/old"));
        assert!(after.last_error.unwrap().contains("所选文件夹不是空的"));
        assert_eq!(next_step(&def, &after_migrate(&loc, &PathBuf::from("/old"), Err("x".into()))), None);
    }

    #[test]
    fn failed_cleanup_is_not_fatal() {
        let loc = Location { data_dir: Some("/new".into()), cleanup_data_dir: Some("/old".into()), ..Default::default() };
        let after = after_cleanup(&loc, Err("media 未能删除".into()));
        assert_eq!(after.cleanup_data_dir, None);
        assert!(after.last_error.unwrap().contains("手动删除"));
    }

    #[test]
    fn missing_custom_dir_is_error() {
        let def = tmp();
        let gone = def.join("unplugged-drive");
        let loc = Location { data_dir: Some(gone.to_string_lossy().into_owned()), ..Default::default() };
        let err = ensure_current_exists(&def, &loc).unwrap_err();
        assert!(err.contains("不存在"), "{err}");
        assert!(!gone.exists(), "不得自动创建自定义数据目录");
        assert!(ensure_current_exists(&def, &Location::default()).is_ok());
    }

    #[test]
    fn last_line_picks_last_non_empty() {
        assert_eq!(last_line("a\nb\n\n").as_deref(), Some("b"));
        assert_eq!(last_line("  \n"), None);
    }
}
