//! 数据目录（spec S1、S2）。默认在系统应用数据目录；用户在设置里改过则从 location.json 读取。
//! location.json 永远放在默认数据目录里、和数据库分开：壳在启动 sidecar 之前就要知道数据在哪。
//! 这个文件只由壳读写（Tauri 命令 get_data_dir / set_data_dir），P4 不要直接写。

use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

pub const LOCATION_FILE: &str = "location.json";

#[derive(Debug, Serialize, Deserialize)]
struct LocationFile {
    version: u32,
    #[serde(rename = "dataDir")]
    data_dir: PathBuf,
}

#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DataDirInfo {
    /// 实际使用的数据目录。
    pub current: PathBuf,
    /// 系统默认数据目录（location.json 所在处）。
    pub default: PathBuf,
    /// 是否为用户自定义的目录。
    pub custom: bool,
}

fn default_info(default_dir: &Path) -> DataDirInfo {
    DataDirInfo { current: default_dir.into(), default: default_dir.into(), custom: false }
}

/// 读取 location.json。文件不存在 → 默认目录；文件损坏或路径非法 → 报错（绝不悄悄回退到默认目录，
/// 否则用户会看到一个空的应用，以为数据丢了）。
pub fn resolve(default_dir: &Path) -> Result<DataDirInfo, String> {
    let file = default_dir.join(LOCATION_FILE);
    let text = match std::fs::read_to_string(&file) {
        Ok(t) => t,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(default_info(default_dir)),
        Err(e) => return Err(format!("无法读取数据目录配置 {}：{e}", file.display())),
    };
    let loc: LocationFile = serde_json::from_str(&text).map_err(|e| {
        format!("数据目录配置文件 {} 已损坏（{e}）。删除该文件即可恢复使用默认数据目录。", file.display())
    })?;
    if loc.version != 1 {
        return Err(format!("数据目录配置文件 {} 的版本 {} 不受支持", file.display(), loc.version));
    }
    if !loc.data_dir.is_absolute() {
        return Err(format!("数据目录配置文件 {} 里的路径必须是绝对路径：{}", file.display(), loc.data_dir.display()));
    }
    if loc.data_dir == default_dir {
        return Ok(default_info(default_dir));
    }
    Ok(DataDirInfo { current: loc.data_dir, default: default_dir.into(), custom: true })
}

/// 创建目录并把权限设为仅当前用户可读写（Unix 0o700；Windows 的 %APPDATA% 本身按用户隔离）。
/// 只用于默认数据目录（应用自己的目录）；用户自选的目录不改权限，见 prepare。
pub fn ensure_private_dir(dir: &Path) -> Result<(), String> {
    std::fs::create_dir_all(dir).map_err(|e| format!("无法创建数据目录 {}：{e}", dir.display()))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o700))
            .map_err(|e| format!("无法设置数据目录权限 {}：{e}", dir.display()))?;
    }
    Ok(())
}

/// 在目录里新建并删除一个临时文件，确认确实可写（只读卷、没有写权限、网络盘断开都会在这里暴露）。
/// 不改目录本身的权限。
pub fn probe_writable(dir: &Path) -> Result<(), String> {
    let probe = dir.join(format!(".miaoju-write-test-{}", uuid::Uuid::new_v4().simple()));
    std::fs::write(&probe, b"ok").map_err(|e| format!("无法写入数据目录 {}：{e}", dir.display()))?;
    let _ = std::fs::remove_file(&probe);
    Ok(())
}

/// 自定义目录出问题时，失败页上的出路（SidecarGate 的“改用默认数据目录”按钮）。
const FALLBACK_HINT: &str = "也可以点“改用默认数据目录”先用起来（原目录里的数据不会被删除）。";

/// 启动 sidecar 前调用。
/// - 默认目录：创建并收紧为 0700。
/// - 自定义目录：用户自己选的（可能是 ~/Documents、/Users/Shared、外接盘或网络盘），**不改它的权限**，
///   只检查存在且可写；不存在时报错，不替用户新建空目录（外接盘没插的情况）。
pub fn prepare(info: &DataDirInfo) -> Result<(), String> {
    if !info.custom {
        return ensure_private_dir(&info.current);
    }
    if !info.current.is_dir() {
        return Err(format!(
            "数据目录不可用：{}。如果它在外接磁盘上，请先连接磁盘再点“重试”。{FALLBACK_HINT}",
            info.current.display()
        ));
    }
    probe_writable(&info.current).map_err(|e| format!("{e}。{FALLBACK_HINT}"))
}

/// 记录“下次用哪个数据目录”。只写配置，不搬数据（搬数据由 P4 完成）。target 为 None 或等于默认目录时删除配置。
pub fn write_location(default_dir: &Path, target: Option<&Path>) -> Result<DataDirInfo, String> {
    ensure_private_dir(default_dir)?;
    let file = default_dir.join(LOCATION_FILE);
    match target {
        Some(t) if t != default_dir => {
            if !t.is_absolute() {
                return Err(format!("数据目录必须是绝对路径：{}", t.display()));
            }
            if !t.is_dir() {
                return Err(format!("目标目录不存在：{}", t.display()));
            }
            // 先试写，不可写就拒绝：否则要到重启 sidecar 时才失败。
            probe_writable(t)?;
            let json = serde_json::to_string_pretty(&LocationFile { version: 1, data_dir: t.into() })
                .map_err(|e| format!("无法生成数据目录配置：{e}"))?;
            let tmp = default_dir.join(format!("{LOCATION_FILE}.tmp"));
            std::fs::write(&tmp, json).map_err(|e| format!("无法写入 {}：{e}", tmp.display()))?;
            std::fs::rename(&tmp, &file).map_err(|e| format!("无法写入 {}：{e}", file.display()))?;
        }
        _ => match std::fs::remove_file(&file) {
            Ok(()) => {}
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => return Err(format!("无法删除 {}：{e}", file.display())),
        },
    }
    resolve(default_dir)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp() -> PathBuf {
        let d = std::env::temp_dir().join(format!("miaoju-test-{}", uuid::Uuid::new_v4().simple()));
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    #[test]
    fn missing_file_means_default() {
        let d = tmp();
        let info = resolve(&d).unwrap();
        assert_eq!(info, DataDirInfo { current: d.clone(), default: d, custom: false });
    }

    #[test]
    fn valid_file_points_to_custom_dir() {
        let d = tmp();
        let target = tmp();
        std::fs::write(
            d.join(LOCATION_FILE),
            format!(r#"{{"version":1,"dataDir":{}}}"#, serde_json::to_string(&target).unwrap()),
        )
        .unwrap();
        let info = resolve(&d).unwrap();
        assert_eq!(info.current, target);
        assert!(info.custom);
    }

    #[test]
    fn custom_equal_to_default_is_not_custom() {
        let d = tmp();
        write_location(&d, Some(&d)).unwrap();
        assert!(!resolve(&d).unwrap().custom);
        assert!(!d.join(LOCATION_FILE).exists());
    }

    #[test]
    fn resolve_rejects_corrupt_file() {
        let d = tmp();
        std::fs::write(d.join(LOCATION_FILE), "{not json").unwrap();
        let err = resolve(&d).unwrap_err();
        assert!(err.contains("location.json"), "{err}");
    }

    #[test]
    fn resolve_rejects_relative_path() {
        let d = tmp();
        std::fs::write(d.join(LOCATION_FILE), r#"{"version":1,"dataDir":"relative/dir"}"#).unwrap();
        assert!(resolve(&d).unwrap_err().contains("绝对路径"));
    }

    #[test]
    fn write_then_reset_location() {
        let d = tmp();
        let target = tmp();
        let info = write_location(&d, Some(&target)).unwrap();
        assert_eq!(info.current, target);
        assert!(info.custom);
        let info = write_location(&d, None).unwrap();
        assert_eq!(info.current, d);
        assert!(!d.join(LOCATION_FILE).exists());
    }

    #[test]
    fn write_location_rejects_relative_and_missing() {
        let d = tmp();
        assert!(write_location(&d, Some(Path::new("rel"))).unwrap_err().contains("绝对路径"));
        let missing = d.join("nope");
        assert!(write_location(&d, Some(&missing)).unwrap_err().contains("不存在"));
    }

    #[test]
    fn prepare_rejects_missing_custom_dir() {
        let d = tmp();
        let gone = d.join("外接盘 未连接");
        let info = DataDirInfo { current: gone.clone(), default: d, custom: true };
        let err = prepare(&info).unwrap_err();
        assert!(err.contains("数据目录不可用"), "{err}");
        assert!(!gone.exists(), "不能替用户新建空目录");
    }

    #[test]
    fn prepare_creates_default_dir() {
        let d = tmp().join("新目录");
        let info = DataDirInfo { current: d.clone(), default: d.clone(), custom: false };
        prepare(&info).unwrap();
        assert!(d.is_dir());
    }

    // I2：失败页“改用默认数据目录”调用 set_data_dir(null)，location.json 损坏时也必须能恢复。
    #[test]
    fn reset_recovers_from_corrupt_location_file() {
        let d = tmp();
        std::fs::write(d.join(LOCATION_FILE), "{not json").unwrap();
        assert!(resolve(&d).is_err());
        let info = write_location(&d, None).unwrap();
        assert_eq!(info.current, d);
        assert!(!info.custom);
    }

    #[cfg(unix)]
    fn mode(p: &Path) -> u32 {
        use std::os::unix::fs::PermissionsExt;
        std::fs::metadata(p).unwrap().permissions().mode() & 0o777
    }

    #[cfg(unix)]
    fn set_mode(p: &Path, m: u32) {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(p, std::fs::Permissions::from_mode(m)).unwrap();
    }

    /// root 无视权限位，以下“不可写”用例在 root 下没有意义。
    #[cfg(unix)]
    fn is_root() -> bool {
        std::process::Command::new("id").arg("-u").output().map(|o| o.stdout.starts_with(b"0\n")).unwrap_or(false)
    }

    // I1：用户选的目录（例如 ~/Documents、/Users/Shared）不能被改权限。
    #[cfg(unix)]
    #[test]
    fn prepare_leaves_custom_dir_permissions_alone() {
        let d = tmp();
        let target = tmp();
        set_mode(&target, 0o755);
        let info = DataDirInfo { current: target.clone(), default: d, custom: true };
        prepare(&info).unwrap();
        assert_eq!(mode(&target), 0o755);
        assert!(std::fs::read_dir(&target).unwrap().next().is_none(), "试写文件要删掉");
    }

    #[cfg(unix)]
    #[test]
    fn write_location_leaves_target_permissions_alone() {
        let d = tmp();
        let target = tmp();
        set_mode(&target, 0o751);
        write_location(&d, Some(&target)).unwrap();
        assert_eq!(mode(&target), 0o751);
        assert!(std::fs::read_dir(&target).unwrap().next().is_none(), "试写文件要删掉");
    }

    // I1：写配置前先试写，不可写的目录直接拒绝，不能等到重启才发现。
    #[cfg(unix)]
    #[test]
    fn write_location_rejects_unwritable_dir() {
        if is_root() {
            return;
        }
        let d = tmp();
        let target = tmp();
        set_mode(&target, 0o500);
        let err = write_location(&d, Some(&target)).unwrap_err();
        set_mode(&target, 0o700);
        assert!(err.contains("无法写入"), "{err}");
        assert!(!d.join(LOCATION_FILE).exists(), "试写失败不能写入配置");
    }

    #[cfg(unix)]
    #[test]
    fn prepare_reports_unwritable_custom_dir_with_fallback_hint() {
        if is_root() {
            return;
        }
        let d = tmp();
        let target = tmp();
        set_mode(&target, 0o500);
        let info = DataDirInfo { current: target.clone(), default: d, custom: true };
        let err = prepare(&info).unwrap_err();
        set_mode(&target, 0o700);
        assert!(err.contains("无法写入") && err.contains("改用默认数据目录"), "{err}");
    }

    #[test]
    fn missing_custom_dir_message_offers_default_fallback() {
        let d = tmp();
        let info = DataDirInfo { current: d.join("gone"), default: d, custom: true };
        assert!(prepare(&info).unwrap_err().contains("改用默认数据目录"));
    }

    #[cfg(unix)]
    #[test]
    fn ensure_private_dir_sets_0700() {
        use std::os::unix::fs::PermissionsExt;
        let d = tmp();
        std::fs::set_permissions(&d, std::fs::Permissions::from_mode(0o755)).unwrap();
        ensure_private_dir(&d).unwrap();
        assert_eq!(std::fs::metadata(&d).unwrap().permissions().mode() & 0o777, 0o700);
        let nested = d.join("a/b");
        ensure_private_dir(&nested).unwrap();
        assert_eq!(std::fs::metadata(&nested).unwrap().permissions().mode() & 0o777, 0o700);
    }
}
