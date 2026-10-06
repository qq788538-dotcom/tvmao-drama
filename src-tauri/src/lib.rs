//! Tauri 薄壳：拉起 Go sidecar、完成 token 握手、把端口和 token 交给前端。
//! 业务逻辑全部在 sidecar 里，这里不放任何业务代码。

use std::path::Path;
use std::sync::Mutex;
use std::time::Duration;

mod datadir;

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
}

#[tauri::command]
fn server_info(state: State<'_, Sidecar>) -> Result<ServerInfo, String> {
    state.info.lock().unwrap().clone()
}

/// 设置页“更改数据目录”：记下待迁移路径，结束 sidecar 后重启应用；迁移在下次启动时进行。
#[tauri::command]
fn apply_data_dir_change(
    app: tauri::AppHandle,
    sidecar: State<'_, Sidecar>,
    dirs: State<'_, datadir::DataDirState>,
    path: String,
) -> Result<(), String> {
    datadir::check_path_syntax(&path)?;
    if Path::new(&path) == dirs.current.as_path() {
        return Err("新目录与当前数据目录相同".into());
    }
    let mut loc = datadir::read_location(&dirs.pointer)?;
    loc.pending_data_dir = Some(path);
    datadir::write_location(&dirs.pointer, &loc)?;
    if let Some(child) = sidecar.child.lock().unwrap().take() {
        let _ = child.kill();
    }
    app.restart();
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
fn start_sidecar(app: &tauri::AppHandle, data_dir: &Path) -> Result<(ServerInfo, CommandChild), String> {
    let token = new_token();
    let (mut rx, mut child) = app
        .shell()
        .sidecar("sidecar")
        .map_err(|e| format!("找不到 sidecar 程序：{e}"))?
        .args(["--data-dir", &data_dir.to_string_lossy()])
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

    // 握手完成后继续把 sidecar 的日志转到壳的 stderr。
    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            match event {
                CommandEvent::Stderr(line) => {
                    eprintln!("[sidecar] {}", String::from_utf8_lossy(&line).trim_end());
                }
                // 握手后 sidecar 退出：只记一行日志，不改状态、不重启。
                CommandEvent::Terminated(payload) => {
                    eprintln!(
                        "[sidecar] 进程已退出，code={:?}，signal={:?}",
                        payload.code, payload.signal
                    );
                }
                _ => {}
            }
        }
    });

    Ok((ServerInfo { port, token }, child))
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
        .plugin(tauri_plugin_dialog::init())
        .setup(|app| {
            let (dirs, data_dir) = datadir::prepare(app.handle());
            let started = data_dir.and_then(|dir| start_sidecar(app.handle(), &dir));
            let state = match started {
                Ok((info, child)) => Sidecar {
                    info: Mutex::new(Ok(info)),
                    child: Mutex::new(Some(child)),
                },
                // 启动失败不退出进程：让窗口照常打开，由前端显示失败原因。
                Err(reason) => Sidecar {
                    info: Mutex::new(Err(format!("本地服务启动失败：{reason}"))),
                    child: Mutex::new(None),
                },
            };
            app.manage(state);
            app.manage(dirs);
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            server_info,
            apply_data_dir_change,
            datadir::data_dir_status,
            datadir::dismiss_data_dir_error
        ])
        .build(tauri::generate_context!())
        .expect("构建 Tauri 应用失败")
        .run(|app, event| {
            // 正常退出时主动结束 sidecar；崩溃场景由 sidecar 自己靠 stdin EOF 兜底。
            if let RunEvent::Exit = event {
                if let Some(child) = app.state::<Sidecar>().child.lock().unwrap().take() {
                    let _ = child.kill();
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
}
