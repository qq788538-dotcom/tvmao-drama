//! Tauri 薄壳：拉起 Go sidecar、完成 token 握手、把端口和 token 交给前端。
//! 业务逻辑全部在 sidecar 里，这里不放任何业务代码。

use std::sync::Mutex;

use serde::Serialize;
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

/// 启动 sidecar 并等到 READY。任何一步失败都返回可读的中文原因。
fn start_sidecar(app: &tauri::AppHandle) -> Result<(ServerInfo, CommandChild), String> {
    let data_dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("无法确定数据目录：{e}"))?;
    std::fs::create_dir_all(&data_dir).map_err(|e| format!("无法创建数据目录：{e}"))?;

    let token = new_token();
    let (mut rx, mut child) = app
        .shell()
        .sidecar("sidecar")
        .map_err(|e| format!("找不到 sidecar 程序：{e}"))?
        .args(["--data-dir", &data_dir.to_string_lossy()])
        .spawn()
        .map_err(|e| format!("无法启动 sidecar：{e}"))?;

    // token 只走 stdin：参数和环境变量同机其他进程读得到。
    child
        .write(format!("{token}\n").as_bytes())
        .map_err(|e| format!("无法向 sidecar 传递 token：{e}"))?;

    let mut stderr_tail = String::new();
    let port = tauri::async_runtime::block_on(async {
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
    });
    let Some(port) = port else {
        return Err(if stderr_tail.is_empty() {
            "sidecar 未就绪即退出".to_string()
        } else {
            stderr_tail
        });
    };

    // 握手完成后继续把 sidecar 的日志转到壳的 stderr。
    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            if let CommandEvent::Stderr(line) = event {
                eprintln!("[sidecar] {}", String::from_utf8_lossy(&line).trim_end());
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
        .setup(|app| {
            let state = match start_sidecar(app.handle()) {
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
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![server_info])
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

    #[test]
    fn new_token_is_long_and_unique() {
        let a = new_token();
        let b = new_token();
        assert_eq!(a.len(), 64);
        assert!(a.chars().all(|c| c.is_ascii_hexdigit()));
        assert_ne!(a, b);
    }
}
