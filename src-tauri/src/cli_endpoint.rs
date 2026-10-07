//! CLI 地址文件（spec 3.2）：sidecar 就绪后写入默认数据目录，CLI 每次执行时读取。
//! 端口每次启动随机，所以 CLI 不能记端口，只能读这个文件。

use crate::lifecycle::ServerInfo;
use std::io;
use std::path::Path;

pub const FILE_NAME: &str = "cli-endpoint.json";

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
struct EndpointFile<'a> {
    version: u32,
    port: u16,
    token: &'a str,
    app_version: &'a str,
}

/// 原子写入：tmp（unix 下 0600）→ fsync → 改名 → fsync 目录。与 datadir::write_durable 同一顺序，
/// 但必须在创建时就设 0600，不能先按 umask 建好再 chmod（中间会有一段时间别人可读）。
pub fn write(dir: &Path, info: &ServerInfo, app_version: &str) -> io::Result<()> {
    use std::io::Write;
    let body = serde_json::to_vec(&EndpointFile {
        version: 1,
        port: info.port,
        token: &info.cli_token,
        app_version,
    })
    .map_err(io::Error::other)?;
    let path = dir.join(FILE_NAME);
    let tmp = dir.join(format!("{FILE_NAME}.tmp"));
    // 上次崩溃可能留下 0644 的 tmp；OpenOptions::mode 不改已存在文件的权限，所以先删。
    let _ = std::fs::remove_file(&tmp);
    let mut opts = std::fs::OpenOptions::new();
    opts.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        opts.mode(0o600);
    }
    let mut f = opts.open(&tmp)?;
    f.write_all(&body)?;
    f.sync_all()?;
    drop(f);
    rename_with_retry(&tmp, &path, |a, b| std::fs::rename(a, b))?;
    #[cfg(not(windows))]
    std::fs::File::open(dir)?.sync_all()?;
    Ok(())
}

/// Windows 上 CLI 正在读目标文件时 rename 会失败（共享冲突），隔 50ms 重试，最多 3 次。
fn rename_with_retry(
    from: &Path,
    to: &Path,
    mut rename: impl FnMut(&Path, &Path) -> io::Result<()>,
) -> io::Result<()> {
    const RETRIES: u32 = 3;
    let mut attempt = 0;
    loop {
        match rename(from, to) {
            Ok(()) => return Ok(()),
            Err(e) if attempt >= RETRIES => return Err(e),
            Err(_) => {
                attempt += 1;
                std::thread::sleep(std::time::Duration::from_millis(50));
            }
        }
    }
}

/// 应用退出时删除。文件不存在不算错（幂等）。
pub fn remove(dir: &Path) {
    let _ = std::fs::remove_file(dir.join(FILE_NAME));
}

#[cfg(test)]
mod tests {
    use super::*;

    fn info() -> ServerInfo {
        ServerInfo { port: 52817, token: "web".into(), cli_token: "cli-abc".into() }
    }

    #[test]
    fn writes_expected_json_without_web_token() {
        let d = tempfile::tempdir().unwrap();
        write(d.path(), &info(), "1.2.0").unwrap();
        let v: serde_json::Value =
            serde_json::from_slice(&std::fs::read(d.path().join(FILE_NAME)).unwrap()).unwrap();
        assert_eq!(v, serde_json::json!({"version":1,"port":52817,"token":"cli-abc","appVersion":"1.2.0"}));
        assert!(!d.path().join("cli-endpoint.json.tmp").exists());
    }

    #[cfg(unix)]
    #[test]
    fn file_is_owner_only() {
        use std::os::unix::fs::PermissionsExt;
        let d = tempfile::tempdir().unwrap();
        write(d.path(), &info(), "1.2.0").unwrap();
        let mode = std::fs::metadata(d.path().join(FILE_NAME)).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode, 0o600);
    }

    #[test]
    fn rename_retries_up_to_three_times() {
        let mut calls = 0;
        let r = rename_with_retry(Path::new("a"), Path::new("b"), |_, _| {
            calls += 1;
            if calls < 3 { Err(io::Error::from(io::ErrorKind::PermissionDenied)) } else { Ok(()) }
        });
        assert!(r.is_ok());
        assert_eq!(calls, 3);

        let mut calls = 0;
        let r = rename_with_retry(Path::new("a"), Path::new("b"), |_, _| {
            calls += 1;
            Err(io::Error::from(io::ErrorKind::PermissionDenied))
        });
        assert!(r.is_err());
        assert_eq!(calls, 4, "首次 + 重试 3 次");
    }

    #[test]
    fn rewrite_replaces_and_remove_is_idempotent() {
        let d = tempfile::tempdir().unwrap();
        write(d.path(), &info(), "1.2.0").unwrap();
        let mut i2 = info();
        i2.port = 60000;
        write(d.path(), &i2, "1.2.0").unwrap();
        let s = std::fs::read_to_string(d.path().join(FILE_NAME)).unwrap();
        assert!(s.contains("60000"));
        remove(d.path());
        remove(d.path());
        assert!(!d.path().join(FILE_NAME).exists());
    }
}
