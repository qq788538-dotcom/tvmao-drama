//! 数据目录迁移的待办记录与启动前执行（spec S2；P4 T7，整合时并入 P8 的异步启动）。
//!
//! 职责划分：
//! - **当前用哪个数据目录**只由 data_dir.rs 的 location.json 决定（P8，get_data_dir / set_data_dir）。
//! - 本文件的 data-location.json 只记迁移待办：待迁移目录、待清理的旧目录、上次的错误。
//!   两个文件都放在系统默认数据目录里；Go `datadir` 的 ignorable 认这两个名字，迁移时不复制、清理时不删除。
//!
//! 流程：设置页校验目标（sidecar 的 /settings/data-dir/check，返回用户所选目录下的专用子目录）→
//! apply_data_dir_change 记下 pendingDataDir、关掉 sidecar、重启应用 → 下次启动时，在 P8 的异步启动里、
//! 拉起 sidecar 之前执行 run_pending：建 0700 的专用子目录 → `sidecar migrate-data-dir` →
//! data_dir::write_location（与 set_data_dir 同一个函数）切到新目录 → `sidecar cleanup-data-dir` 清理旧目录。

use std::path::{Path, PathBuf};
use std::sync::atomic::AtomicBool;
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use tauri::State;

use crate::data_dir;

/// 与 Go `datadir.PointerFileName` 一致。
pub const POINTER_FILE: &str = "data-location.json";

/// 迁移 / 清理子命令持有的排他锁文件，与 Go `datadir.MigrateLockFileName` 一致，放在默认数据目录。
/// 锁由 sidecar 子进程自己拿（flock / LockFileEx），进程退出即释放：壳在迁移中途退出时子进程可能作为孤儿
/// 继续跑，下次启动的子进程会等它结束（最多 10 分钟）再开始，两个迁移不会并行。
pub const MIGRATE_LOCK_FILE: &str = ".miaoju-migrate.lock";

pub fn migrate_lock_path(default_dir: &Path) -> PathBuf {
    default_dir.join(MIGRATE_LOCK_FILE)
}

/// 用户所选目录下的专用子目录名，与 Go `datadir.DedicatedSubdir` 一致。
pub const DEDICATED_SUBDIR: &str = "MiaojuData";

#[derive(Default, Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Location {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pending_data_dir: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cleanup_data_dir: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_error: Option<String>,
}

/// 待办文件不存在 → 默认值；存在但损坏 → 报错。
pub fn read_location(path: &Path) -> Result<Location, String> {
    match std::fs::read_to_string(path) {
        Ok(s) => serde_json::from_str(&s)
            .map_err(|e| format!("数据目录迁移记录已损坏（{}）：{e}", path.display())),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(Location::default()),
        Err(e) => Err(format!("无法读取数据目录迁移记录（{}）：{e}", path.display())),
    }
}

/// 持久化原子写入（写临时文件并 fsync → 改名 → fsync 父目录，Windows 跳过最后一步）。全部字段为空时删掉文件。
pub fn write_location(path: &Path, loc: &Location) -> Result<(), String> {
    if *loc == Location::default() {
        return match std::fs::remove_file(path) {
            Ok(()) => Ok(()),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Err(e) => Err(format!("无法写入数据目录迁移记录：{e}")),
        };
    }
    let body = serde_json::to_string_pretty(loc).map_err(|e| e.to_string())?;
    write_durable(&mut RealFs, path, body.as_bytes())
        .map_err(|e| format!("无法写入数据目录迁移记录：{e}"))
}

pub fn pointer_path(default_dir: &Path) -> PathBuf {
    default_dir.join(POINTER_FILE)
}

/// write_durable 依赖的文件操作，测试里用记录调用顺序的假实现替换。
pub(crate) trait DurableFs {
    /// 写入并 fsync 文件内容。
    fn write_synced(&mut self, path: &Path, body: &[u8]) -> std::io::Result<()>;
    fn rename(&mut self, from: &Path, to: &Path) -> std::io::Result<()>;
    /// fsync 目录，让改名后的目录项落盘。
    fn sync_dir(&mut self, dir: &Path) -> std::io::Result<()>;
}

struct RealFs;

impl DurableFs for RealFs {
    fn write_synced(&mut self, path: &Path, body: &[u8]) -> std::io::Result<()> {
        use std::io::Write;
        let mut f = std::fs::File::create(path)?;
        f.write_all(body)?;
        f.sync_all()
    }
    fn rename(&mut self, from: &Path, to: &Path) -> std::io::Result<()> {
        std::fs::rename(from, to)
    }
    fn sync_dir(&mut self, dir: &Path) -> std::io::Result<()> {
        std::fs::File::open(dir)?.sync_all()
    }
}

/// 真实文件系统上的 write_durable（data_dir.rs 的 location.json 也用它）。
pub(crate) fn write_durable_file(path: &Path, body: &[u8]) -> std::io::Result<()> {
    write_durable(&mut RealFs, path, body)
}

pub(crate) fn write_durable(fs: &mut dyn DurableFs, path: &Path, body: &[u8]) -> std::io::Result<()> {
    let tmp = path.with_extension("json.tmp");
    fs.write_synced(&tmp, body)?;
    fs.rename(&tmp, path)?;
    // Windows 不能对目录句柄 fsync（需要 FILE_FLAG_BACKUP_SEMANTICS），跳过。
    if cfg!(not(windows)) {
        if let Some(dir) = path.parent().filter(|d| !d.as_os_str().is_empty()) {
            fs.sync_dir(dir)?;
        }
    }
    Ok(())
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

/// apply_data_dir_change 的形状检查：目标要么是默认目录，要么是“用户所选目录/MiaojuData”，且所选目录存在。
/// 完整校验（空目录、互相包含、可写）由 sidecar 的 /settings/data-dir/check 做过一遍，迁移时 Go 还会再做一遍。
pub fn check_target_shape(p: &str, default_dir: &Path) -> Result<PathBuf, String> {
    check_path_syntax(p)?;
    let path = PathBuf::from(p);
    if path == default_dir {
        return Ok(path);
    }
    if path.file_name().and_then(|n| n.to_str()) != Some(DEDICATED_SUBDIR) {
        return Err(format!("数据目录必须是所选文件夹下的 {DEDICATED_SUBDIR} 子目录：{p}"));
    }
    match path.parent() {
        Some(parent) if parent.is_dir() => Ok(path),
        _ => Err(format!("所选文件夹不存在：{p}")),
    }
}

/// 迁移前建好目标目录并收紧为 0700（Unix）。默认目录走 data_dir::ensure_private_dir。
/// 专用子目录是应用自己建的，可以改权限；它的上级（用户所选目录）不存在时报错，不替用户新建。
pub fn ensure_dedicated_dir(to: &Path, default_dir: &Path) -> Result<(), String> {
    if to == default_dir {
        return data_dir::ensure_private_dir(to);
    }
    match to.parent() {
        Some(parent) if parent.is_dir() => {}
        _ => return Err(format!("所选文件夹不存在：{}", to.display())),
    }
    match std::fs::create_dir(to) {
        Ok(()) => {}
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists && to.is_dir() => {}
        Err(e) => return Err(format!("无法创建数据目录 {}：{e}", to.display())),
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(to, std::fs::Permissions::from_mode(0o700))
            .map_err(|e| format!("无法设置数据目录权限 {}：{e}", to.display()))?;
    }
    Ok(())
}

#[derive(Debug, PartialEq)]
pub enum Step {
    Migrate { from: PathBuf, to: PathBuf },
    Cleanup { old: PathBuf, new: PathBuf },
    /// 待迁移目录已经是当前目录：上次迁移成功、切换了 location.json，但没来得及清掉待办就中断了。
    /// 只丢掉待办（旧目录没有记下，不清理，最多留一份多余的旧数据）。
    DropPending,
}

/// 启动时下一步要做什么。每一步之后对应字段都会被清掉，所以循环必然结束。
pub fn next_step(current: &Path, loc: &Location) -> Option<Step> {
    if let Some(to) = loc.pending_data_dir.as_deref().filter(|s| !s.is_empty()) {
        let to = PathBuf::from(to);
        if to == current {
            return Some(Step::DropPending);
        }
        return Some(Step::Migrate { from: current.to_path_buf(), to });
    }
    if let Some(old) = loc.cleanup_data_dir.as_deref().filter(|s| !s.is_empty()) {
        return Some(Step::Cleanup { old: PathBuf::from(old), new: current.to_path_buf() });
    }
    None
}

pub fn after_migrate(loc: &Location, from: &Path, result: Result<(), String>) -> Location {
    let mut next = loc.clone();
    next.pending_data_dir = None;
    match result {
        Ok(()) => {
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

pub fn last_line(s: &str) -> Option<String> {
    s.lines().rev().map(str::trim).find(|l| !l.is_empty()).map(String::from)
}

/// 有没有待办（读不出来也算有：交给 run_pending 报错）。
pub fn has_pending(default_dir: &Path) -> bool {
    match read_location(&pointer_path(default_dir)) {
        Ok(loc) => loc.pending_data_dir.is_some() || loc.cleanup_data_dir.is_some(),
        Err(_) => true,
    }
}

/// 执行全部待办。run 负责调用一次 sidecar 子命令（测试里注入假实现）。
/// 返回 Err 只代表待办记录本身读写失败；迁移 / 清理失败写进 lastError，原目录照常可用。
pub fn run_pending(
    default_dir: &Path,
    run: &mut dyn FnMut(Vec<String>) -> Result<(), String>,
) -> Result<(), String> {
    let pointer = pointer_path(default_dir);
    let lock = migrate_lock_path(default_dir).to_string_lossy().into_owned();
    let mut loc = read_location(&pointer)?;
    loop {
        let current = data_dir::resolve(default_dir)?.current;
        let Some(step) = next_step(&current, &loc) else { break };
        loc = match step {
            Step::Migrate { from, to } => {
                let r = ensure_dedicated_dir(&to, default_dir).and_then(|_| {
                    run(vec![
                        "migrate-data-dir".into(),
                        "--from".into(),
                        from.to_string_lossy().into_owned(),
                        "--to".into(),
                        to.to_string_lossy().into_owned(),
                        "--lock".into(),
                        lock.clone(),
                    ])
                });
                // 复制校验成功后才切换 location.json（与 set_data_dir 同一个函数），再记待清理。
                let r = r.and_then(|_| {
                    data_dir::write_location(default_dir, Some(&to))
                        .map(|_| ())
                        .map_err(|e| format!("数据已复制，但无法切换到新目录：{e}"))
                });
                after_migrate(&loc, &from, r)
            }
            Step::Cleanup { old, new } => {
                let r = run(vec![
                    "cleanup-data-dir".into(),
                    "--old".into(),
                    old.to_string_lossy().into_owned(),
                    "--new".into(),
                    new.to_string_lossy().into_owned(),
                    "--lock".into(),
                    lock.clone(),
                ]);
                after_cleanup(&loc, r)
            }
            Step::DropPending => Location { pending_data_dir: None, ..loc.clone() },
        };
        write_location(&pointer, &loc)?;
    }
    Ok(())
}

/// 调一次 sidecar 子命令并等它结束，失败时返回 stderr 最后一行。
pub fn run_sidecar_once(program: &Path, args: &[String]) -> Result<(), String> {
    let mut cmd = std::process::Command::new(program);
    cmd.args(args).stdin(std::process::Stdio::null());
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        cmd.creation_flags(0x0800_0000); // CREATE_NO_WINDOW
    }
    let out = cmd.output().map_err(|e| format!("无法启动 sidecar：{e}"))?;
    if out.status.success() {
        return Ok(());
    }
    Err(last_line(&String::from_utf8_lossy(&out.stderr))
        .unwrap_or_else(|| format!("sidecar 退出码 {:?}", out.status.code())))
}

/// 托管状态：迁移是否进行中（期间拒绝 restart_sidecar），以及待办记录本身读写失败的原因。
#[derive(Default)]
pub struct DataDirState {
    pub migrating: AtomicBool,
    pub startup_error: Mutex<Option<String>>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DataDirStatus {
    current: String,
    default_dir: String,
    last_error: Option<String>,
}

#[tauri::command]
pub fn data_dir_status(app: tauri::AppHandle, state: State<'_, DataDirState>) -> Result<DataDirStatus, String> {
    let default = crate::default_data_dir(&app)?;
    let current = data_dir::resolve(&default).map(|i| i.current).unwrap_or_else(|_| default.clone());
    let last_error = state
        .startup_error
        .lock()
        .unwrap()
        .clone()
        .or_else(|| read_location(&pointer_path(&default)).ok().and_then(|l| l.last_error));
    Ok(DataDirStatus {
        current: current.to_string_lossy().into_owned(),
        default_dir: default.to_string_lossy().into_owned(),
        last_error,
    })
}

#[tauri::command]
pub fn dismiss_data_dir_error(app: tauri::AppHandle, state: State<'_, DataDirState>) -> Result<(), String> {
    let default = crate::default_data_dir(&app)?;
    let pointer = pointer_path(&default);
    let mut loc = read_location(&pointer).unwrap_or_default();
    loc.last_error = None;
    write_location(&pointer, &loc)?;
    *state.startup_error.lock().unwrap() = None;
    Ok(())
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
        let loc = Location { pending_data_dir: Some("/b".into()), ..Default::default() };
        write_location(&p, &loc).unwrap();
        let raw = std::fs::read_to_string(&p).unwrap();
        assert!(raw.contains("pendingDataDir") && !raw.contains("lastError"), "{raw}");
        assert_eq!(read_location(&p).unwrap(), loc);
        assert!(!dir.join("data-location.json.tmp").exists());
        write_location(&p, &Location::default()).unwrap();
        assert!(!p.exists(), "全空时删掉文件");
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
    fn target_shape_requires_dedicated_subdir() {
        let def = tmp();
        let picked = tmp();
        assert!(check_target_shape(&def.to_string_lossy(), &def).is_ok());
        let sub = picked.join(DEDICATED_SUBDIR);
        assert_eq!(check_target_shape(&sub.to_string_lossy(), &def).unwrap(), sub);
        assert!(check_target_shape(&picked.to_string_lossy(), &def).unwrap_err().contains(DEDICATED_SUBDIR));
        let gone = picked.join("gone").join(DEDICATED_SUBDIR);
        assert!(check_target_shape(&gone.to_string_lossy(), &def).unwrap_err().contains("不存在"));
    }

    #[cfg(unix)]
    #[test]
    fn ensure_dedicated_dir_creates_0700_and_never_creates_parent() {
        use std::os::unix::fs::PermissionsExt;
        let def = tmp();
        let picked = tmp();
        std::fs::set_permissions(&picked, std::fs::Permissions::from_mode(0o755)).unwrap();
        let sub = picked.join(DEDICATED_SUBDIR);
        ensure_dedicated_dir(&sub, &def).unwrap();
        assert_eq!(std::fs::metadata(&sub).unwrap().permissions().mode() & 0o777, 0o700);
        assert_eq!(std::fs::metadata(&picked).unwrap().permissions().mode() & 0o777, 0o755, "不改用户所选目录的权限");
        ensure_dedicated_dir(&sub, &def).unwrap(); // 已存在（上次中断）也可以
        let orphan = picked.join("unplugged").join(DEDICATED_SUBDIR);
        assert!(ensure_dedicated_dir(&orphan, &def).is_err());
        assert!(!orphan.parent().unwrap().exists());
    }

    #[test]
    fn next_step_migrate_then_cleanup_then_none() {
        let def = PathBuf::from("/default");
        let mut loc = Location { pending_data_dir: Some("/new".into()), ..Default::default() };
        assert_eq!(next_step(&def, &loc), Some(Step::Migrate { from: def.clone(), to: PathBuf::from("/new") }));
        loc = after_migrate(&loc, &def, Ok(()));
        assert_eq!(loc.pending_data_dir, None);
        let new = PathBuf::from("/new");
        assert_eq!(next_step(&new, &loc), Some(Step::Cleanup { old: def.clone(), new: new.clone() }));
        loc = after_cleanup(&loc, Ok(()));
        assert_eq!(next_step(&new, &loc), None);
    }

    #[test]
    fn pending_equal_to_current_is_dropped() {
        let loc = Location { pending_data_dir: Some("/new".into()), ..Default::default() };
        assert_eq!(next_step(Path::new("/new"), &loc), Some(Step::DropPending));
    }

    #[test]
    fn failed_migration_records_error() {
        let loc = Location { pending_data_dir: Some("/new".into()), ..Default::default() };
        let after = after_migrate(&loc, &PathBuf::from("/old"), Err("所选文件夹不是空的".into()));
        assert_eq!(after.pending_data_dir, None);
        assert_eq!(after.cleanup_data_dir, None);
        assert!(after.last_error.unwrap().contains("所选文件夹不是空的"));
    }

    #[test]
    fn failed_cleanup_is_not_fatal() {
        let loc = Location { cleanup_data_dir: Some("/old".into()), ..Default::default() };
        let after = after_cleanup(&loc, Err("media 未能删除".into()));
        assert_eq!(after.cleanup_data_dir, None);
        assert!(after.last_error.unwrap().contains("手动删除"));
    }

    #[test]
    fn last_line_picks_last_non_empty() {
        assert_eq!(last_line("a\nb\n\n").as_deref(), Some("b"));
        assert_eq!(last_line("  \n"), None);
    }

    /// 端到端：默认目录 → 所选目录/MiaojuData，子命令用假实现；检查 location.json 切换、待办清空、调用顺序。
    #[test]
    fn run_pending_migrates_switches_location_then_cleans_up() {
        let def = tmp();
        let picked = tmp();
        let to = picked.join(DEDICATED_SUBDIR);
        write_location(&pointer_path(&def), &Location { pending_data_dir: Some(to.to_string_lossy().into_owned()), ..Default::default() }).unwrap();
        assert!(has_pending(&def));
        let mut calls: Vec<String> = vec![];
        run_pending(&def, &mut |args| {
            calls.push(args[0].clone());
            // 每个子命令都带默认数据目录下的迁移锁，由子进程持有（防止两个迁移并行）
            let i = args.iter().position(|a| a == "--lock").expect("子命令应带 --lock");
            assert_eq!(Path::new(&args[i + 1]), def.join(MIGRATE_LOCK_FILE));
            if args[0] == "migrate-data-dir" {
                assert!(Path::new(&args[4]).is_dir(), "子命令之前已建好专用子目录");
                assert_eq!(data_dir::resolve(&def).unwrap().current, def, "复制完成前不切换");
            }
            Ok(())
        })
        .unwrap();
        assert_eq!(calls, vec!["migrate-data-dir", "cleanup-data-dir"]);
        let info = data_dir::resolve(&def).unwrap();
        assert_eq!(info.current, to);
        assert!(info.custom);
        assert!(!has_pending(&def));
        assert!(!pointer_path(&def).exists());
    }

    #[test]
    fn run_pending_failed_migration_keeps_current_dir() {
        let def = tmp();
        let to = tmp().join(DEDICATED_SUBDIR);
        write_location(&pointer_path(&def), &Location { pending_data_dir: Some(to.to_string_lossy().into_owned()), ..Default::default() }).unwrap();
        run_pending(&def, &mut |_| Err("磁盘已满".into())).unwrap();
        assert_eq!(data_dir::resolve(&def).unwrap().current, def);
        let loc = read_location(&pointer_path(&def)).unwrap();
        assert!(loc.pending_data_dir.is_none() && loc.cleanup_data_dir.is_none());
        assert!(loc.last_error.unwrap().contains("磁盘已满"));
    }

    #[derive(Default)]
    struct RecordingFs {
        calls: Vec<String>,
        fail_write: bool,
    }

    impl DurableFs for RecordingFs {
        fn write_synced(&mut self, path: &Path, _body: &[u8]) -> std::io::Result<()> {
            self.calls.push(format!("write_synced {}", path.file_name().unwrap().to_string_lossy()));
            if self.fail_write {
                return Err(std::io::Error::other("disk full"));
            }
            Ok(())
        }
        fn rename(&mut self, from: &Path, to: &Path) -> std::io::Result<()> {
            self.calls.push(format!(
                "rename {} {}",
                from.file_name().unwrap().to_string_lossy(),
                to.file_name().unwrap().to_string_lossy()
            ));
            Ok(())
        }
        fn sync_dir(&mut self, dir: &Path) -> std::io::Result<()> {
            self.calls.push(format!("sync_dir {}", dir.display()));
            Ok(())
        }
    }

    #[test]
    fn write_durable_syncs_tmp_then_renames_then_syncs_parent() {
        let dir = PathBuf::from("/cfg");
        let mut fs = RecordingFs::default();
        write_durable(&mut fs, &dir.join(POINTER_FILE), b"{}").unwrap();
        let mut want = vec![
            "write_synced data-location.json.tmp".to_string(),
            "rename data-location.json.tmp data-location.json".to_string(),
        ];
        if cfg!(not(windows)) {
            want.push("sync_dir /cfg".to_string());
        }
        assert_eq!(fs.calls, want);
    }

    #[test]
    fn write_durable_does_not_rename_when_write_fails() {
        let mut fs = RecordingFs { fail_write: true, ..Default::default() };
        assert!(write_durable(&mut fs, &PathBuf::from("/cfg").join(POINTER_FILE), b"{}").is_err());
        assert_eq!(fs.calls, vec!["write_synced data-location.json.tmp".to_string()]);
    }
}
