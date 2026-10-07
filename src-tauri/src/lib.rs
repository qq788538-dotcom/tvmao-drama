//! Tauri 薄壳：管理 Go sidecar 的生命周期、把端口和 token 交给前端。
//! 业务逻辑全部在 sidecar 里，这里不放任何业务代码。

pub mod data_dir;
pub mod datadir;
pub mod cli_endpoint;
mod cli_install;
mod lifecycle;
pub mod media_save;
pub mod native_dialog;
pub mod sidecar;
pub mod webview2;

use std::path::{Path, PathBuf};
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::Duration;

use tauri::{Emitter as TauriEmitter, Manager, RunEvent, State, WindowEvent};

use lifecycle::{Emitter, ServerInfo, SidecarManager, SidecarStatus, SpecFactory, Timeouts};

/// sidecar 状态变化事件名，与 web/src/shared/server.ts 的 SIDECAR_STATE_EVENT 一致。
pub const SIDECAR_STATE_EVENT: &str = "sidecar-state";

const RELEASE_ORIGINS: &str = "tauri://localhost,http://tauri.localhost";
/// Vite 开发服务器，只在 debug 构建放行。
const DEV_ORIGIN: &str = "http://localhost:5173";

/// 传给 sidecar 的参数。token 不在这里：它只走 stdin（SEC3）。
pub fn sidecar_args(data_dir: &Path, debug: bool, python_home: Option<&Path>) -> Vec<String> {
    let mut origins = RELEASE_ORIGINS.to_string();
    if debug {
        origins.push(',');
        origins.push_str(DEV_ORIGIN);
    }
    let mut args = vec![
        "--data-dir".into(),
        data_dir.to_string_lossy().into_owned(),
        "--origins".into(),
        origins,
    ];
    if let Some(home) = python_home {
        args.push("--python-home".into());
        args.push(home.to_string_lossy().into_owned());
    }
    args
}

/// 内置 Python 的解释器相对路径（占位目录里只有 README，不算“存在”）。
fn python_interpreter(home: &Path) -> PathBuf {
    if cfg!(windows) {
        home.join("python.exe")
    } else {
        home.join("bin").join("python3")
    }
}

/// 在候选的 resources 根目录里按顺序找 `python/`，返回第一个带解释器的绝对路径。
/// 候选顺序：Tauri resource_dir（macOS .app 的 Contents/Resources、Windows zip 的 exe 同目录、dev 的 target/debug）；
/// debug 构建再退到源码里的 src-tauri/resources（fetch-python.sh 的产出位置）。
pub fn locate_python_home(roots: &[PathBuf]) -> Option<PathBuf> {
    roots
        .iter()
        .map(|r| r.join("python"))
        .find(|home| python_interpreter(home).is_file())
        .map(|home| {
            let p = std::fs::canonicalize(&home).unwrap_or(home);
            // M7：Windows 的 canonicalize 返回 \\?\C:\... 形式，Python getpath 和下游路径比较未必认，去掉前缀。
            if cfg!(windows) {
                PathBuf::from(strip_verbatim(&p.to_string_lossy()))
            } else {
                p
            }
        })
}

/// 去掉 Windows 扩展长度路径前缀：`\\?\C:\x` → `C:\x`，`\\?\UNC\srv\share` → `\\srv\share`。
pub fn strip_verbatim(p: &str) -> String {
    if let Some(rest) = p.strip_prefix(r"\\?\UNC\") {
        format!(r"\\{rest}")
    } else if let Some(rest) = p.strip_prefix(r"\\?\") {
        rest.to_string()
    } else {
        p.to_string()
    }
}

fn python_home(app: &tauri::AppHandle) -> Option<PathBuf> {
    let mut roots = Vec::new();
    if let Ok(dir) = app.path().resource_dir() {
        roots.push(dir);
    }
    if cfg!(debug_assertions) {
        roots.push(Path::new(env!("CARGO_MANIFEST_DIR")).join("resources"));
    }
    let found = locate_python_home(&roots);
    match &found {
        Some(p) => eprintln!("[miaoju] 内置 Python：{}", p.display()),
        None => eprintln!(
            "[miaoju] 未找到内置 Python（已查找：{:?}），不传 --python-home，脚本节点将不可用",
            roots
        ),
    }
    found
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
        return SidecarStatus::Migrating { waiting: dd.waiting_lock.load(Ordering::SeqCst) };
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
    dd: State<'_, datadir::DataDirState>,
    path: String,
) -> Result<(), String> {
    // M1：迁移进行中不接受新的待办（会被迁移循环的内存副本覆盖），也不能重启（会中断迁移）。
    if dd.migrating.load(Ordering::SeqCst) {
        return Err("正在迁移数据目录，请稍候".into());
    }
    let default = default_data_dir(&app)?;
    let target = datadir::check_target_shape(&path, &default)?;
    if data_dir::resolve(&default)?.current == target {
        return Err("新目录与当前数据目录相同".into());
    }
    let pointer = datadir::pointer_path(&default);
    let mut loc = datadir::read_location(&pointer)?;
    loc.pending_data_dir = Some(target.to_string_lossy().into_owned());
    datadir::write_location(&pointer, &loc)?;
    // 先在这里优雅关闭 sidecar（写 SHUTDOWN、关 stdin、等待），sidecar 的收尾不依赖退出事件的时序。
    // 从异步命令（非主线程）调用 app.restart() 会经 request_exit 触发 ExitRequested / Exit，再重启进程；
    // 那里的 shutdown 是幂等的，此时直接返回。
    let manager = m.inner().clone();
    let _ = tokio::time::timeout(Duration::from_secs(6), manager.shutdown()).await;
    app.restart();
}

#[tauri::command]
fn get_data_dir(app: tauri::AppHandle) -> Result<data_dir::DataDirInfo, String> {
    data_dir::resolve(&default_data_dir(&app)?)
}

/// 只改“用哪个数据目录”，不搬数据；前端随后调 restart_sidecar 生效。
/// - path 为 null：恢复默认目录（失败页“改用默认数据目录”）。
/// - path 为已有喵剧数据的目录（设置页“直接使用该目录（不迁移）”，I1）：必须是“所选文件夹/MiaojuData”，
///   含有效 data.db 且没有未完成的迁移标记（M2：不接受任意路径）。
/// 用户手动切换后，尚未执行的迁移 / 清理待办作废。
#[tauri::command]
fn set_data_dir(
    app: tauri::AppHandle,
    dd: State<'_, datadir::DataDirState>,
    path: Option<String>,
) -> Result<data_dir::DataDirInfo, String> {
    if dd.migrating.load(Ordering::SeqCst) {
        return Err("正在迁移数据目录，请稍候".into());
    }
    let default = default_data_dir(&app)?;
    let target = match path.as_deref() {
        None => None,
        Some(p) => Some(datadir::check_existing_data_dir(p, &default)?),
    };
    let info = data_dir::write_location(&default, target.as_deref())?;
    datadir::forget_pending(&default)?;
    Ok(info)
}

/// 迁移进行中拦下关闭 / 退出时的提示（非阻塞；同一时刻只开一个）。
fn notify_exit_blocked(app: &tauri::AppHandle) {
    use tauri_plugin_dialog::{DialogExt, MessageDialogKind};
    let Some(dd) = app.try_state::<datadir::DataDirState>() else { return };
    if dd.exit_notice_open.swap(true, Ordering::SeqCst) {
        return;
    }
    let handle = app.clone();
    app.dialog()
        .message("正在迁移数据目录，迁移完成后应用会自动继续启动。为保证数据完整，迁移期间不能关闭应用，请稍候。")
        .title("正在迁移数据目录")
        .kind(MessageDialogKind::Warning)
        .show(move |_| {
            if let Some(dd) = handle.try_state::<datadir::DataDirState>() {
                dd.exit_notice_open.store(false, Ordering::SeqCst);
            }
        });
}

fn migrating(app: &tauri::AppHandle) -> bool {
    app.try_state::<datadir::DataDirState>().is_some_and(|dd| dd.migrating.load(Ordering::SeqCst))
}

/// 关 stdin → 等 sidecar 收尾（最多 3 秒）→ 强杀。正在启动时 shutdown 会立即取消启动，不会等 READY。
/// 整体再加上限兜底；即便超时，壳退出后 stdin 管道关闭，sidecar 也会自行退出。幂等。
fn shutdown_sidecar(app: &tauri::AppHandle) {
    let Some(manager) = app.try_state::<Arc<SidecarManager>>() else { return };
    let manager = manager.inner().clone();
    let _ = tauri::async_runtime::block_on(async move {
        tokio::time::timeout(Duration::from_secs(6), manager.shutdown()).await
    });
    if let Ok(dir) = default_data_dir(app) {
        cli_endpoint::remove(&dir);
    }
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
        // 前端只有 dialog:allow-open（数据目录选择，只返回路径）；保存对话框只走 Rust 侧 API（media_save），前端不授予 dialog:allow-save / fs:*，写盘路径只能来自用户在 Rust 对话框里的选择。
        .plugin(tauri_plugin_dialog::init())
        .manage(media_save::MediaRoot::default())
        .setup(|app| {
            let handle = app.handle().clone();

            let spec_app = handle.clone();
            let make_spec: SpecFactory = Arc::new(move || {
                let info = data_dir::resolve(&default_data_dir(&spec_app)?)?;
                data_dir::prepare(&info)?;
                let py = python_home(&spec_app);
                spec_app
                    .state::<media_save::MediaRoot>()
                    .set(media_save::media_root_of(&info.current));
                Ok(sidecar::LaunchSpec {
                    program: sidecar_program()?,
                    args: sidecar_args(&info.current, cfg!(debug_assertions), py.as_deref()),
                })
            });

            let emit_app = handle.clone();
            let emit: Emitter = Arc::new(move |status| {
                let _ = emit_app.emit(SIDECAR_STATE_EVENT, status);
            });

            let manager = SidecarManager::new(make_spec, emit.clone(), Timeouts::default());
            {
                let app_version = app.package_info().version.to_string();
                let dir = default_data_dir(&handle);
                manager.set_ready_hook(Arc::new(move |info: &ServerInfo| {
                    let Ok(dir) = &dir else { return };
                    if let Err(e) = cli_endpoint::write(dir, info, &app_version) {
                        // 写失败只影响 CLI，不影响应用本身，记日志即可
                        eprintln!("写 CLI 地址文件失败：{e}");
                    }
                }));
            }
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
                        emit(SidecarStatus::Migrating { waiting: false });
                        let slot = dd.child.clone();
                        let (line_app, line_emit) = (task_app.clone(), emit.clone());
                        let r = tauri::async_runtime::spawn_blocking(move || {
                            let program = sidecar_program()?;
                            // 子命令在等上一次迁移的锁时打一行提示（I3），界面据此区分“等待”与“迁移中”。
                            let mut on_line = |line: &str| {
                                if let Some(waiting) = datadir::lock_line_status(line) {
                                    line_app.state::<datadir::DataDirState>().waiting_lock.store(waiting, Ordering::SeqCst);
                                    line_emit(SidecarStatus::Migrating { waiting });
                                }
                            };
                            datadir::run_pending(&default, &mut |args| {
                                datadir::run_sidecar_once(&program, &args, &slot, &mut on_line)
                            })
                        })
                        .await
                        .map_err(|e| format!("数据目录迁移中断：{e}"))
                        .and_then(|r| r);
                        if let Err(e) = r {
                            *dd.startup_error.lock().unwrap() = Some(e);
                        }
                        dd.waiting_lock.store(false, Ordering::SeqCst);
                        dd.migrating.store(false, Ordering::SeqCst);
                    }
                }
                let _ = manager.start().await;
            });
            Ok(())
        })
        // I2：迁移期间拦下关窗（否则窗口没了、进程还在）。macOS 的 Cmd+Q 拦不住，走 Exit 里结束子进程那条路。
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                if migrating(window.app_handle()) {
                    api.prevent_close();
                    notify_exit_blocked(window.app_handle());
                }
            }
        })
        .invoke_handler(tauri::generate_handler![
            server_info,
            sidecar_status,
            restart_sidecar,
            get_data_dir,
            set_data_dir,
            apply_data_dir_change,
            datadir::data_dir_status,
            datadir::dismiss_data_dir_error,
            media_save::save_media,
            media_save::save_media_batch,
            media_save::save_bytes,
            cli_install::cli_tool_status,
            cli_install::install_cli_tool,
            cli_install::uninstall_cli_tool
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

    app.run(|app, event| match event {
        RunEvent::ExitRequested { code, api, .. } => {
            // I2：迁移期间不退出（restart 不可拦，也不会发生：迁移期间 apply_data_dir_change 被拒绝）。
            if code != Some(tauri::RESTART_EXIT_CODE) && migrating(app) {
                api.prevent_exit();
                notify_exit_blocked(app);
                return;
            }
            // I4：单实例插件在 Exit 时释放锁，这里（Exit 之前）先把 sidecar 关掉，新实例不会和它同时打开数据目录。
            // macOS Cmd+Q 不经过 ExitRequested，靠 sidecar 自己的数据目录锁兜底。
            shutdown_sidecar(app);
        }
        RunEvent::Exit => {
            // 结束仍在跑的迁移子进程（Cmd+Q 等拦不住的退出）；中途被杀是安全的，下次启动重做。
            if let Some(dd) = app.try_state::<datadir::DataDirState>() {
                dd.child.kill();
            }
            shutdown_sidecar(app);
        }
        _ => {}
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn release_args_exclude_dev_origin() {
        let args = sidecar_args(Path::new("/data"), false, None);
        assert_eq!(
            args,
            vec!["--data-dir", "/data", "--origins", "tauri://localhost,http://tauri.localhost"]
        );
    }

    #[test]
    fn debug_args_include_dev_origin() {
        let args = sidecar_args(Path::new("/data"), true, None);
        assert_eq!(args[3], "tauri://localhost,http://tauri.localhost,http://localhost:5173");
    }

    #[test]
    fn sidecar_args_keep_cjk_and_spaces() {
        let args = sidecar_args(Path::new("/Users/张 三/Library/Application Support/com.miaoju.desktop"), false, None);
        assert_eq!(args[1], "/Users/张 三/Library/Application Support/com.miaoju.desktop");
    }

    #[test]
    fn args_append_python_home_when_present() {
        let args = sidecar_args(Path::new("/data"), false, Some(Path::new("/res/python")));
        assert_eq!(&args[4..], ["--python-home", "/res/python"]);
        assert_eq!(args.len(), 6);
    }

    fn fake_python(root: &Path) {
        let exe = python_interpreter(&root.join("python"));
        std::fs::create_dir_all(exe.parent().unwrap()).unwrap();
        std::fs::write(exe, b"").unwrap();
    }

    fn tmp() -> PathBuf {
        let d = std::env::temp_dir().join(format!("miaoju-py-{}", uuid::Uuid::new_v4().simple()));
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    #[test]
    fn locate_python_home_found_feeds_args() {
        let (empty, full) = (tmp(), tmp());
        fake_python(&full);
        let home = locate_python_home(&[empty, full.clone()]).expect("应找到 python");
        assert!(home.is_absolute() && home.ends_with("python"));
        let args = sidecar_args(Path::new("/data"), false, Some(&home));
        assert_eq!(args[4], "--python-home");
        assert_eq!(args[5], home.to_string_lossy());
    }

    #[test]
    fn locate_python_home_missing_or_placeholder_only_omits_flag() {
        let root = tmp();
        assert!(locate_python_home(&[root.clone()]).is_none());
        // 只有占位 README，没有解释器，也视为不存在
        std::fs::create_dir_all(root.join("python")).unwrap();
        std::fs::write(root.join("python/README.md"), b"x").unwrap();
        let py = locate_python_home(&[root]);
        assert!(py.is_none());
        let args = sidecar_args(Path::new("/data"), false, py.as_deref());
        assert!(!args.iter().any(|a| a == "--python-home"));
    }

    /// M7：Windows 上 canonicalize 产生的 \\?\ 前缀要去掉（UNC 形式保留为 \\server\share）。
    #[test]
    fn strip_verbatim_prefix_rules() {
        assert_eq!(strip_verbatim(r"\\?\C:\Program Files\miaoju\python"), r"C:\Program Files\miaoju\python");
        assert_eq!(strip_verbatim(r"\\?\UNC\server\share\python"), r"\\server\share\python");
        assert_eq!(strip_verbatim(r"C:\x"), r"C:\x");
        assert_eq!(strip_verbatim("/Applications/喵剧AI.app/Contents/Resources/python"), "/Applications/喵剧AI.app/Contents/Resources/python");
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
