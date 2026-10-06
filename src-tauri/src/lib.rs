//! Tauri 薄壳：管理 Go sidecar 的生命周期、把端口和 token 交给前端。
//! 业务逻辑全部在 sidecar 里，这里不放任何业务代码。

pub mod data_dir;
pub mod datadir;
pub mod lifecycle;
pub mod native_dialog;
pub mod sidecar;
pub mod webview2;

use std::path::{Path, PathBuf};
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::Duration;

use tauri::{Emitter as TauriEmitter, Manager, RunEvent, State};

use lifecycle::{Emitter, ServerInfo, SidecarManager, SidecarStatus, SpecFactory, Timeouts};

/// sidecar 状态变化事件名，与 web/src/shared/server.ts 的 SIDECAR_STATE_EVENT 一致。
pub const SIDECAR_STATE_EVENT: &str = "sidecar-state";

const RELEASE_ORIGINS: &str = "tauri://localhost,http://tauri.localhost";
/// Vite 开发服务器，只在 debug 构建放行。
const DEV_ORIGIN: &str = "http://localhost:5173";

/// 传给 sidecar 的参数。token 不在这里：它只走 stdin（SEC3）。
pub fn sidecar_args(data_dir: &Path, debug: bool) -> Vec<String> {
    let mut origins = RELEASE_ORIGINS.to_string();
    if debug {
        origins.push(',');
        origins.push_str(DEV_ORIGIN);
    }
    vec![
        "--data-dir".into(),
        data_dir.to_string_lossy().into_owned(),
        "--origins".into(),
        origins,
    ]
}

/// Tauri 把 externalBin 放在主程序同目录（dev：target/debug；macOS：Contents/MacOS；Windows zip：exe 同目录）。
pub fn locate_sidecar(exe_dir: &Path) -> Result<PathBuf, String> {
    let path = exe_dir.join(format!("sidecar{}", std::env::consts::EXE_SUFFIX));
    if path.is_file() {
        Ok(path)
    } else {
        Err(format!(
            "找不到 sidecar 程序：{}。如果是 Windows 版，请先把 zip 完整解压到一个文件夹，再运行其中的 miaoju.exe。",
            path.display()
        ))
    }
}

fn sidecar_program() -> Result<PathBuf, String> {
    let exe = tauri::utils::platform::current_exe().map_err(|e| format!("无法定位主程序：{e}"))?;
    let dir = exe.parent().ok_or_else(|| "无法定位主程序所在目录".to_string())?;
    locate_sidecar(dir)
}

pub(crate) fn default_data_dir(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    app.path().app_data_dir().map_err(|e| format!("无法确定数据目录：{e}"))
}

#[tauri::command]
async fn server_info(m: State<'_, Arc<SidecarManager>>) -> Result<ServerInfo, String> {
    m.wait_info().await
}

#[tauri::command]
fn sidecar_status(m: State<'_, Arc<SidecarManager>>, dd: State<'_, datadir::DataDirState>) -> SidecarStatus {
    if dd.migrating.load(Ordering::SeqCst) {
        return SidecarStatus::Migrating;
    }
    m.status()
}

#[tauri::command]
async fn restart_sidecar(
    m: State<'_, Arc<SidecarManager>>,
    dd: State<'_, datadir::DataDirState>,
) -> Result<ServerInfo, String> {
    // 迁移进行中不能拉起 sidecar：它会打开正在被复制的数据库。迁移结束后异步启动任务会自己拉起。
    if dd.migrating.load(Ordering::SeqCst) {
        return Err("正在迁移数据目录，请稍候".into());
    }
    m.inner().restart().await
}

/// 设置页“更改数据目录”（P4 T7）：记下待迁移目录，优雅关闭 sidecar，重启应用；迁移在下次启动时、
/// 拉起 sidecar 之前进行（见 datadir::run_pending）。path 是 /settings/data-dir/check 返回的路径
/// （用户所选目录下的 MiaojuData 子目录，或默认目录）。成功时不返回。
#[tauri::command]
async fn apply_data_dir_change(
    app: tauri::AppHandle,
    m: State<'_, Arc<SidecarManager>>,
    path: String,
) -> Result<(), String> {
    let default = default_data_dir(&app)?;
    let target = datadir::check_target_shape(&path, &default)?;
    if data_dir::resolve(&default)?.current == target {
        return Err("新目录与当前数据目录相同".into());
    }
    let pointer = datadir::pointer_path(&default);
    let mut loc = datadir::read_location(&pointer)?;
    loc.pending_data_dir = Some(target.to_string_lossy().into_owned());
    datadir::write_location(&pointer, &loc)?;
    // app.restart() 直接结束进程、不走 RunEvent::Exit，所以先在这里优雅关闭 sidecar（写 SHUTDOWN、关 stdin、等待）。
    let manager = m.inner().clone();
    let _ = tokio::time::timeout(Duration::from_secs(6), manager.shutdown()).await;
    app.restart();
}

#[tauri::command]
fn get_data_dir(app: tauri::AppHandle) -> Result<data_dir::DataDirInfo, String> {
    data_dir::resolve(&default_data_dir(&app)?)
}

/// 只改“下次启动用哪个数据目录”，不搬数据。P4 迁移完数据后调用，再调 restart_sidecar 生效。
/// path 为 null 表示恢复默认目录。
#[tauri::command]
fn set_data_dir(app: tauri::AppHandle, path: Option<String>) -> Result<data_dir::DataDirInfo, String> {
    let default = default_data_dir(&app)?;
    data_dir::write_location(&default, path.as_deref().map(Path::new))
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // E8：建窗口前检测 WebView2，缺失则弹窗并退出。
    #[cfg(windows)]
    webview2::ensure_or_exit();

    let built = tauri::Builder::default()
        // 单实例插件必须最先注册：第二次启动时只把已有窗口带到前台（spec S11）。
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.unminimize();
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .plugin(tauri_plugin_dialog::init())
        .setup(|app| {
            let handle = app.handle().clone();

            let spec_app = handle.clone();
            let make_spec: SpecFactory = Arc::new(move || {
                let info = data_dir::resolve(&default_data_dir(&spec_app)?)?;
                data_dir::prepare(&info)?;
                Ok(sidecar::LaunchSpec {
                    program: sidecar_program()?,
                    args: sidecar_args(&info.current, cfg!(debug_assertions)),
                })
            });

            let emit_app = handle.clone();
            let emit: Emitter = Arc::new(move |status| {
                let _ = emit_app.emit(SIDECAR_STATE_EVENT, status);
            });

            let manager = SidecarManager::new(make_spec, emit.clone(), Timeouts::default());
            app.manage(manager.clone());
            app.manage(datadir::DataDirState::default());
            // 异步启动：窗口立即出现，前端显示“正在启动本地服务…”。
            // 有数据目录迁移待办（P4）时先在这里做完，再拉起 sidecar；期间状态为 migrating。
            let task_app = handle.clone();
            tauri::async_runtime::spawn(async move {
                if let Ok(default) = default_data_dir(&task_app) {
                    if datadir::has_pending(&default) {
                        let dd = task_app.state::<datadir::DataDirState>();
                        dd.migrating.store(true, Ordering::SeqCst);
                        emit(SidecarStatus::Migrating);
                        let r = tauri::async_runtime::spawn_blocking(move || {
                            let program = sidecar_program()?;
                            datadir::run_pending(&default, &mut |args| datadir::run_sidecar_once(&program, &args))
                        })
                        .await
                        .map_err(|e| format!("数据目录迁移中断：{e}"))
                        .and_then(|r| r);
                        if let Err(e) = r {
                            *dd.startup_error.lock().unwrap() = Some(e);
                        }
                        dd.migrating.store(false, Ordering::SeqCst);
                    }
                }
                let _ = manager.start().await;
            });
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            server_info,
            sidecar_status,
            restart_sidecar,
            get_data_dir,
            set_data_dir,
            apply_data_dir_change,
            datadir::data_dir_status,
            datadir::dismiss_data_dir_error
        ])
        .build(tauri::generate_context!());

    let app = match built {
        Ok(app) => app,
        Err(e) => {
            // 兜底：Windows release 没有控制台，必须弹窗，否则用户只看到闪退。
            native_dialog::show_error("喵剧AI 启动失败", &format!("{e}"));
            std::process::exit(1);
        }
    };

    app.run(|app, event| {
        if let RunEvent::Exit = event {
            let manager = app.state::<Arc<SidecarManager>>().inner().clone();
            // 关 stdin → 等 sidecar 收尾（最多 3 秒）→ 强杀。正在启动时 shutdown 会立即取消启动，不会等 READY。
            // 整体再加上限兜底；即便超时，壳退出后 stdin 管道关闭，sidecar 也会自行退出。
            let _ = tauri::async_runtime::block_on(async move {
                tokio::time::timeout(Duration::from_secs(6), manager.shutdown()).await
            });
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn release_args_exclude_dev_origin() {
        let args = sidecar_args(Path::new("/data"), false);
        assert_eq!(
            args,
            vec!["--data-dir", "/data", "--origins", "tauri://localhost,http://tauri.localhost"]
        );
    }

    #[test]
    fn debug_args_include_dev_origin() {
        let args = sidecar_args(Path::new("/data"), true);
        assert_eq!(args[3], "tauri://localhost,http://tauri.localhost,http://localhost:5173");
    }

    #[test]
    fn sidecar_args_keep_cjk_and_spaces() {
        let args = sidecar_args(Path::new("/Users/张 三/Library/Application Support/com.miaoju.desktop"), false);
        assert_eq!(args[1], "/Users/张 三/Library/Application Support/com.miaoju.desktop");
    }

    #[test]
    fn locate_sidecar_reports_unzip_hint() {
        let dir = std::env::temp_dir().join(format!("miaoju-test-{}", uuid::Uuid::new_v4().simple()));
        std::fs::create_dir_all(&dir).unwrap();
        let err = locate_sidecar(&dir).unwrap_err();
        assert!(err.contains("找不到 sidecar 程序") && err.contains("完整解压"), "{err}");
        let name = format!("sidecar{}", std::env::consts::EXE_SUFFIX);
        std::fs::write(dir.join(&name), b"").unwrap();
        assert_eq!(locate_sidecar(&dir).unwrap(), dir.join(name));
    }
}
