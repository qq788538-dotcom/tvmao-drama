//! Tauri 薄壳：拉起 Go sidecar、完成 token 握手、把端口和 token 交给前端。
//! 业务逻辑全部在 sidecar 里，这里不放任何业务代码。

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use serde::Serialize;
use tauri::async_runtime::Receiver;
use tauri::{Manager, RunEvent, State};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

#[derive(Clone, Serialize)]
pub struct ServerInfo {
    port: u16,
    token: String,
}

/// sidecar 的运行状态。`info` 为 Err 时保存启动失败原因，前端据此显示错误页。
struct Sidecar {
    info: Mutex<Result<ServerInfo, String>>,
    child: Mutex<Option<CommandChild>>,
    /// sidecar 进程已退出（日志转发任务收到 Terminated 时置位）；退出握手据此判断要不要强杀。
    exited: Arc<AtomicBool>,
}

#[tauri::command]
fn server_info(state: State<'_, Sidecar>) -> Result<ServerInfo, String> {
    state.info.lock().unwrap().clone()
}

/// 解析 sidecar 的就绪行 "READY <port>"。
pub fn parse_ready(line: &str) -> Option<u16> {
    line.trim().strip_prefix("READY ")?.trim().parse().ok()
}

/// 64 位十六进制随机 token（两个 UUIDv4，来自系统随机源）。
pub fn new_token() -> String {
    format!(
        "{}{}",
        uuid::Uuid::new_v4().simple(),
        uuid::Uuid::new_v4().simple()
    )
}

/// 等待 sidecar 就绪的最长时间。
const READY_TIMEOUT: Duration = Duration::from_secs(20);

/// 开发服务器的来源。只在 debug 构建里经 --origins 交给 sidecar；release 包不放行它（P0 遗留）。
pub const DEV_ORIGINS: &str = "tauri://localhost,http://tauri.localhost,http://localhost:5173";

/// 应用退出时等 sidecar 自己收尾（停 HTTP、停后台任务、关数据库）的最长时间，超时再强杀。
const SHUTDOWN_GRACE: Duration = Duration::from_secs(3);

/// sidecar 的命令行参数。debug 构建额外放行 Vite 开发服务器的来源。
pub fn sidecar_args(data_dir: &str, debug: bool) -> Vec<String> {
    let mut args = vec!["--data-dir".to_string(), data_dir.to_string()];
    if debug {
        args.push("--origins".to_string());
        args.push(DEV_ORIGINS.to_string());
    }
    args
}

/// 轮询等 flag 置位，最多等 timeout；返回是否等到。
pub fn wait_flag(flag: &AtomicBool, timeout: Duration) -> bool {
    let deadline = Instant::now() + timeout;
    loop {
        if flag.load(Ordering::SeqCst) {
            return true;
        }
        if Instant::now() >= deadline {
            return false;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
}

/// 数据目录只允许当前用户访问：data.db 里有 API Key（spec M3）。
/// Windows 的 %APPDATA% 本身就是用户私有目录，不另设 ACL。
fn make_private(dir: &std::path::Path) -> Result<(), String> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o700))
            .map_err(|e| format!("无法设置数据目录权限：{e}"))?;
    }
    #[cfg(not(unix))]
    let _ = dir;
    Ok(())
}

/// 等待 "READY <port>"。sidecar 先退出返回其最后一行 stderr，超时返回超时原因。
async fn wait_ready(rx: &mut Receiver<CommandEvent>, timeout: Duration) -> Result<u16, String> {
    let mut stderr_tail = String::new();
    let waited = tokio::time::timeout(timeout, async {
        while let Some(event) = rx.recv().await {
            match event {
                CommandEvent::Stdout(line) => {
                    if let Some(port) = parse_ready(&String::from_utf8_lossy(&line)) {
                        return Some(port);
                    }
                }
                CommandEvent::Stderr(line) => {
                    let text = String::from_utf8_lossy(&line);
                    eprintln!("[sidecar] {}", text.trim_end());
                    stderr_tail = text.trim().to_string();
                }
                CommandEvent::Terminated(_) | CommandEvent::Error(_) => return None,
                _ => {}
            }
        }
        None
    })
    .await;
    match waited {
        Ok(Some(port)) => Ok(port),
        Ok(None) if stderr_tail.is_empty() => Err("sidecar 未就绪即退出".to_string()),
        Ok(None) => Err(stderr_tail),
        Err(_) => Err(format!(
            "sidecar 启动超时（{} 秒内未就绪）",
            timeout.as_secs()
        )),
    }
}

/// 启动 sidecar 并等到 READY。任何一步失败都返回可读的中文原因。
fn start_sidecar(app: &tauri::AppHandle) -> Result<(ServerInfo, CommandChild, Arc<AtomicBool>), String> {
    let data_dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("无法确定数据目录：{e}"))?;
    std::fs::create_dir_all(&data_dir).map_err(|e| format!("无法创建数据目录：{e}"))?;
    make_private(&data_dir)?;

    let token = new_token();
    let (mut rx, mut child) = app
        .shell()
        .sidecar("sidecar")
        .map_err(|e| format!("找不到 sidecar 程序：{e}"))?
        .args(sidecar_args(
            &data_dir.to_string_lossy(),
            cfg!(debug_assertions),
        ))
        .spawn()
        .map_err(|e| format!("无法启动 sidecar：{e}"))?;

    // token 只走 stdin：参数和环境变量同机其他进程读得到。
    if let Err(e) = child.write(format!("{token}\n").as_bytes()) {
        let _ = child.kill();
        return Err(format!("无法向 sidecar 传递 token：{e}"));
    }

    let port = match tauri::async_runtime::block_on(wait_ready(&mut rx, READY_TIMEOUT)) {
        Ok(port) => port,
        Err(reason) => {
            // 失败路径显式结束子进程，不依赖 drop 或 stdin EOF。
            let _ = child.kill();
            return Err(reason);
        }
    };

    let exited = Arc::new(AtomicBool::new(false));
    let exited_flag = exited.clone();
    // 握手完成后继续把 sidecar 的日志转到壳的 stderr。
    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            match event {
                CommandEvent::Stderr(line) => {
                    eprintln!("[sidecar] {}", String::from_utf8_lossy(&line).trim_end());
                }
                // 握手后 sidecar 退出：只记一行日志，不改状态、不重启。
                CommandEvent::Terminated(payload) => {
                    exited_flag.store(true, Ordering::SeqCst);
                    eprintln!(
                        "[sidecar] 进程已退出，code={:?}，signal={:?}",
                        payload.code, payload.signal
                    );
                }
                _ => {}
            }
        }
    });

    Ok((ServerInfo { port, token }, child, exited))
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        // 单实例插件必须最先注册：第二次启动时只把已有窗口带到前台（spec S11）。
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.unminimize();
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            let state = match start_sidecar(app.handle()) {
                Ok((info, child, exited)) => Sidecar {
                    info: Mutex::new(Ok(info)),
                    child: Mutex::new(Some(child)),
                    exited,
                },
                // 启动失败不退出进程：让窗口照常打开，由前端显示失败原因。
                Err(reason) => Sidecar {
                    info: Mutex::new(Err(format!("本地服务启动失败：{reason}"))),
                    child: Mutex::new(None),
                    exited: Arc::new(AtomicBool::new(true)),
                },
            };
            app.manage(state);
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![server_info])
        .build(tauri::generate_context!())
        .expect("构建 Tauri 应用失败")
        .run(|app, event| {
            // 正常退出时先请 sidecar 自己收尾，等不到再强杀；崩溃场景由 sidecar 自己靠 stdin EOF 兜底。
            if let RunEvent::Exit = event {
                let state = app.state::<Sidecar>();
                let child = state.child.lock().unwrap().take();
                if let Some(mut child) = child {
                    let _ = child.write(b"SHUTDOWN\n");
                    if !wait_flag(&state.exited, SHUTDOWN_GRACE) {
                        let _ = child.kill();
                    }
                }
            }
        });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_ready_accepts_port_line() {
        assert_eq!(parse_ready("READY 54321"), Some(54321));
        assert_eq!(parse_ready("READY 54321\r\n"), Some(54321));
    }

    #[test]
    fn parse_ready_ignores_other_lines() {
        assert_eq!(parse_ready("listening..."), None);
        assert_eq!(parse_ready("READY"), None);
        assert_eq!(parse_ready("READY abc"), None);
        assert_eq!(parse_ready("READY 99999"), None); // 超出 u16
    }

    use std::time::{Duration, Instant};
    use tauri::async_runtime::{block_on, channel};
    use tauri_plugin_shell::process::TerminatedPayload;

    #[test]
    fn wait_ready_returns_port_on_ready_line() {
        let (tx, mut rx) = channel(8);
        block_on(tx.send(CommandEvent::Stdout(b"READY 54321\n".to_vec()))).unwrap();
        let r = block_on(wait_ready(&mut rx, Duration::from_secs(5)));
        assert_eq!(r, Ok(54321));
    }

    #[test]
    fn wait_ready_reports_last_stderr_when_terminated() {
        let (tx, mut rx) = channel(8);
        block_on(tx.send(CommandEvent::Stderr(b"boom\n".to_vec()))).unwrap();
        block_on(tx.send(CommandEvent::Terminated(TerminatedPayload {
            code: Some(1),
            signal: None,
        })))
        .unwrap();
        let err = block_on(wait_ready(&mut rx, Duration::from_secs(5))).unwrap_err();
        assert!(err.contains("boom"), "{err}");
    }

    #[test]
    fn wait_ready_reports_exit_when_sender_dropped() {
        let (tx, mut rx) = channel::<CommandEvent>(8);
        drop(tx);
        let err = block_on(wait_ready(&mut rx, Duration::from_secs(5))).unwrap_err();
        assert_eq!(err, "sidecar 未就绪即退出");
    }

    #[test]
    fn wait_ready_times_out_when_silent() {
        let (_tx, mut rx) = channel::<CommandEvent>(8); // 发送端保持存活
        let start = Instant::now();
        let err = block_on(wait_ready(&mut rx, Duration::from_millis(50))).unwrap_err();
        assert!(err.contains("启动超时"), "{err}");
        assert!(start.elapsed() < Duration::from_secs(2));
    }

    #[test]
    fn new_token_is_long_and_unique() {
        let a = new_token();
        let b = new_token();
        assert_eq!(a.len(), 64);
        assert!(a.chars().all(|c| c.is_ascii_hexdigit()));
        assert_ne!(a, b);
    }

    #[test]
    fn sidecar_args_adds_dev_origins_only_in_debug() {
        assert_eq!(sidecar_args("/d", false), vec!["--data-dir", "/d"]);
        let dbg = sidecar_args("/d", true);
        assert_eq!(dbg.len(), 4);
        assert_eq!(dbg[2], "--origins");
        assert!(dbg[3].contains("http://localhost:5173"));
        assert!(dbg[3].contains("tauri://localhost"));
    }

    #[test]
    fn wait_flag_returns_when_flag_set() {
        use std::sync::atomic::{AtomicBool, Ordering};
        use std::sync::Arc;
        let flag = Arc::new(AtomicBool::new(false));
        let setter = flag.clone();
        std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(100));
            setter.store(true, Ordering::SeqCst);
        });
        assert!(wait_flag(&flag, Duration::from_secs(2)));
    }

    #[test]
    fn wait_flag_times_out() {
        let flag = std::sync::atomic::AtomicBool::new(false);
        let start = Instant::now();
        assert!(!wait_flag(&flag, Duration::from_millis(100)));
        assert!(start.elapsed() < Duration::from_secs(1));
    }
}
