//! “保存到本地”：把媒体文件 / 前端生成的字节写到**用户在系统保存对话框里选的位置**。
//!
//! 为什么不靠 `<a download>`：wry 0.57 的 WKWebView 导航代理在没有注册 download handler 时，
//! 对 `shouldPerformDownload` 的导航直接回 `Cancel`（见 wry src/wkwebview/navigation.rs），
//! Tauri 只有在 `WebviewBuilder::on_download` 被调用时才注册 handler。本应用没注册，
//! 所以 macOS 上点 `<a download>` 没有任何反应。
//!
//! 安全约束（spec SEC4）：
//! - 写盘目标路径只来自本模块在 Rust 里弹出的保存 / 选文件夹对话框，前端拿不到“往任意路径写”的能力，
//!   也不需要授予前端任何 dialog / fs 权限。
//! - 复制媒体时，源只接受 sidecar 发出的本地媒体 URL（或 `media://<key>`），key 规则与
//!   server/internal/assets/local.go 的 ValidKey 一致，并在 canonicalize 之后再确认仍在媒体根目录内
//!   （防符号链接逃逸；媒体根目录的上一级就是 data.db）。
//! - 不允许把文件存进媒体根目录本身，也不允许覆盖源文件。

use std::path::{Path, PathBuf};
use std::sync::Mutex;

use serde::Deserialize;
use tauri::{AppHandle, Manager, Runtime, State};
use tauri_plugin_dialog::DialogExt;

/// 本次 sidecar 启动所用的媒体根目录（`<data-dir>/media`）。每次生成 LaunchSpec 时更新，
/// 所以切换数据目录但还没重启 sidecar 时，仍指向正在服务的那个目录。
#[derive(Default)]
pub struct MediaRoot(Mutex<Option<PathBuf>>);

impl MediaRoot {
    pub fn set(&self, root: PathBuf) {
        *self.0.lock().unwrap_or_else(|e| e.into_inner()) = Some(root);
    }
    pub fn get(&self) -> Option<PathBuf> {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).clone()
    }
}

/// 与 sidecar `--data-dir` 下的媒体目录约定一致（server/cmd/sidecar/main.go）。
pub fn media_root_of(data_dir: &Path) -> PathBuf {
    data_dir.join("media")
}

const MEDIA_SCHEME: &str = "media://";
const LOCAL_PREFIX: &str = "http://127.0.0.1:";

/// 从 sidecar 媒体 URL（`http://127.0.0.1:<port>/media/<escaped key>?token=…`）或 `media://<key>`
/// 里取出 object key。不是这两种形式、或转义非法时返回 None。只做解析，不校验 key。
pub fn extract_key(url: &str) -> Option<String> {
    let raw = if let Some(rest) = url.strip_prefix(MEDIA_SCHEME) {
        rest
    } else {
        let rest = url.strip_prefix(LOCAL_PREFIX)?;
        let digits = rest.bytes().take_while(|b| b.is_ascii_digit()).count();
        if digits == 0 || digits > 5 {
            return None;
        }
        rest[digits..].strip_prefix("/media/")?
    };
    let raw = raw.split(['?', '#']).next().unwrap_or("");
    if url.starts_with(MEDIA_SCHEME) {
        // media:// 里存的是原始 key，不做转义。
        return Some(raw.to_string());
    }
    percent_decode(raw)
}

fn percent_decode(s: &str) -> Option<String> {
    let bytes = s.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' {
            let hex = s.get(i + 1..i + 3)?;
            out.push(u8::from_str_radix(hex, 16).ok()?);
            i += 3;
        } else {
            out.push(bytes[i]);
            i += 1;
        }
    }
    String::from_utf8(out).ok()
}

/// 与 server/internal/assets/local.go 的 ValidKey 同规则。
pub fn valid_key(key: &str) -> bool {
    if key.is_empty() || key.len() > 1024 || key.starts_with('/') {
        return false;
    }
    if key
        .chars()
        .any(|c| (c as u32) < 0x20 || c as u32 == 0x7f || "\\:*?\"<>|#%".contains(c))
    {
        return false;
    }
    key.split('/')
        .all(|seg| !(seg.is_empty() || seg == "." || seg == ".." || seg.ends_with('.') || seg.ends_with(' ')))
}

/// 把媒体 URL 换算成媒体根目录内一个真实存在的普通文件。
pub fn resolve_media_file(root: &Path, url: &str) -> Result<PathBuf, String> {
    let key = extract_key(url).ok_or_else(|| "只能保存本地生成或上传的媒体文件".to_string())?;
    if !valid_key(&key) {
        return Err("无法识别这个媒体".into());
    }
    let root_c = root.canonicalize().map_err(|e| format!("媒体目录不可用：{e}"))?;
    let mut p = root_c.clone();
    for seg in key.split('/') {
        p.push(seg);
    }
    let p = p.canonicalize().map_err(|_| "文件不存在，可能已被删除".to_string())?;
    if !p.starts_with(&root_c) || p == root_c {
        return Err("无法识别这个媒体".into());
    }
    if !p.is_file() {
        return Err("文件不存在，可能已被删除".into());
    }
    Ok(p)
}

/// 前端给的建议文件名只用作对话框默认值 / 批量保存时的文件名：去掉路径成分和各平台非法字符。
pub fn sanitize_file_name(name: &str) -> String {
    // 只取最后一段，杜绝 "../x" 或 "C:\\x" 这类路径。
    let last = name.rsplit(['/', '\\']).next().unwrap_or("");
    let mut out: String = last
        .chars()
        .map(|c| if (c as u32) < 0x20 || c as u32 == 0x7f || ":*?\"<>|".contains(c) { '_' } else { c })
        .collect();
    let trimmed = out.trim_matches(|c: char| c == '.' || c == ' ' || c.is_whitespace()).to_string();
    out = trimmed;
    const MAX: usize = 150;
    if out.len() > MAX {
        let ext = Path::new(&out)
            .extension()
            .and_then(|e| e.to_str())
            .filter(|e| e.len() <= 16)
            .map(|e| format!(".{e}"))
            .unwrap_or_default();
        let mut cut = MAX - ext.len();
        while !out.is_char_boundary(cut) {
            cut -= 1;
        }
        out = format!("{}{}", &out[..cut], ext);
    }
    if out.is_empty() {
        "download".into()
    } else {
        out
    }
}

/// 目标路径的规范形式（文件可能还不存在，所以规范化父目录再拼文件名）。
fn canonical_target(dest: &Path) -> Result<PathBuf, String> {
    let parent = dest.parent().filter(|p| !p.as_os_str().is_empty()).unwrap_or(Path::new("."));
    let name = dest.file_name().ok_or_else(|| "保存位置无效".to_string())?;
    let parent = parent.canonicalize().map_err(|e| format!("保存位置不可用：{e}"))?;
    Ok(parent.join(name))
}

/// 拒绝写进媒体根目录（会污染 / 覆盖 sidecar 管理的文件）。
fn check_target(dest: &Path, media_root: Option<&Path>) -> Result<PathBuf, String> {
    let target = canonical_target(dest)?;
    if let Some(root) = media_root.and_then(|r| r.canonicalize().ok()) {
        if target.starts_with(&root) {
            return Err("不能保存到喵剧的数据目录里，请换一个位置".into());
        }
    }
    Ok(target)
}

fn temp_sibling(target: &Path) -> PathBuf {
    let name = target.file_name().map(|n| n.to_string_lossy().into_owned()).unwrap_or_default();
    target.with_file_name(format!(".{name}.miaoju-{}.part", uuid::Uuid::new_v4().simple()))
}

/// 先写同目录临时文件再 rename：中途失败不会留下半截文件，也不会先截断已存在的目标。
fn write_via_temp(target: &Path, write: impl FnOnce(&Path) -> std::io::Result<()>) -> Result<(), String> {
    let tmp = temp_sibling(target);
    let res = write(&tmp).and_then(|_| std::fs::rename(&tmp, target));
    if let Err(e) = res {
        let _ = std::fs::remove_file(&tmp);
        return Err(format!("写入失败：{e}"));
    }
    Ok(())
}

/// 把媒体文件复制到用户选定的目标。
pub fn copy_media_to(src: &Path, dest: &Path, media_root: Option<&Path>) -> Result<PathBuf, String> {
    let target = check_target(dest, media_root)?;
    if src.canonicalize().ok().as_deref() == Some(target.as_path()) {
        return Err("不能覆盖原文件".into());
    }
    write_via_temp(&target, |tmp| std::fs::copy(src, tmp).map(|_| ()))?;
    Ok(target)
}

/// 把前端生成的字节（ZIP、截图、导出的 JSON 等）写到用户选定的目标。
pub fn write_bytes_to(bytes: &[u8], dest: &Path, media_root: Option<&Path>) -> Result<PathBuf, String> {
    let target = check_target(dest, media_root)?;
    write_via_temp(&target, |tmp| std::fs::write(tmp, bytes))?;
    Ok(target)
}

/// 批量保存时在目标文件夹里找一个不冲突的文件名：`a.png` → `a (1).png` …
pub fn unique_in_dir(dir: &Path, name: &str) -> PathBuf {
    let first = dir.join(name);
    if !first.exists() {
        return first;
    }
    let p = Path::new(name);
    let stem = p.file_stem().and_then(|s| s.to_str()).unwrap_or(name);
    let ext = p.extension().and_then(|e| e.to_str()).map(|e| format!(".{e}")).unwrap_or_default();
    (1..)
        .map(|i| dir.join(format!("{stem} ({i}){ext}")))
        .find(|c| !c.exists())
        .expect("unbounded range")
}

fn media_root_required(state: &MediaRoot) -> Result<PathBuf, String> {
    state.get().ok_or_else(|| "本地服务尚未启动".to_string())
}

async fn ask_save_path<R: Runtime>(app: &AppHandle<R>, suggested: &str) -> Option<PathBuf> {
    let (tx, rx) = tokio::sync::oneshot::channel();
    let mut builder = app.dialog().file().set_title("保存到本地").set_file_name(suggested);
    if let Some(w) = app.get_webview_window("main") {
        builder = builder.set_parent(&w);
    }
    builder.save_file(move |p| {
        let _ = tx.send(p);
    });
    rx.await.ok().flatten().and_then(|p| p.into_path().ok())
}

async fn ask_folder<R: Runtime>(app: &AppHandle<R>) -> Option<PathBuf> {
    let (tx, rx) = tokio::sync::oneshot::channel();
    let mut builder = app.dialog().file().set_title("选择保存文件夹").set_can_create_directories(true);
    if let Some(w) = app.get_webview_window("main") {
        builder = builder.set_parent(&w);
    }
    builder.pick_folder(move |p| {
        let _ = tx.send(p);
    });
    rx.await.ok().flatten().and_then(|p| p.into_path().ok())
}

async fn blocking<T: Send + 'static>(f: impl FnOnce() -> Result<T, String> + Send + 'static) -> Result<T, String> {
    tauri::async_runtime::spawn_blocking(f).await.map_err(|e| format!("保存失败：{e}"))?
}

/// 弹保存对话框，把本地媒体复制过去。返回保存后的路径；用户取消返回 None。
#[tauri::command]
pub async fn save_media<R: Runtime>(
    app: AppHandle<R>,
    root: State<'_, MediaRoot>,
    url: String,
    suggested_name: String,
) -> Result<Option<String>, String> {
    let root = media_root_required(&root)?;
    // 先校验源，非法 / 已删除就不弹对话框。
    let src = resolve_media_file(&root, &url)?;
    let Some(dest) = ask_save_path(&app, &sanitize_file_name(&suggested_name)).await else {
        return Ok(None);
    };
    let saved = blocking(move || copy_media_to(&src, &dest, Some(&root))).await?;
    Ok(Some(saved.to_string_lossy().into_owned()))
}

#[derive(Debug, Deserialize)]
pub struct BatchItem {
    pub url: String,
    pub name: String,
}

/// 弹“选择文件夹”，把多个本地媒体复制进去（重名自动加序号，不覆盖）。返回保存的文件数；取消返回 None。
#[tauri::command]
pub async fn save_media_batch<R: Runtime>(
    app: AppHandle<R>,
    root: State<'_, MediaRoot>,
    items: Vec<BatchItem>,
) -> Result<Option<usize>, String> {
    let root = media_root_required(&root)?;
    let mut plan = Vec::with_capacity(items.len());
    for it in &items {
        plan.push((resolve_media_file(&root, &it.url)?, sanitize_file_name(&it.name)));
    }
    if plan.is_empty() {
        return Ok(Some(0));
    }
    let Some(dir) = ask_folder(&app).await else {
        return Ok(None);
    };
    blocking(move || {
        for (src, name) in &plan {
            let dest = unique_in_dir(&dir, name);
            copy_media_to(src, &dest, Some(&root))?;
        }
        Ok(Some(plan.len()))
    })
    .await
}

/// 文件名请求头：值是 encodeURIComponent 后的建议文件名。
pub const FILE_NAME_HEADER: &str = "x-file-name";

/// 原始字节走 IPC raw body（不经 JSON / base64），文件名放在请求头里。
#[tauri::command]
pub async fn save_bytes<R: Runtime>(
    app: AppHandle<R>,
    root: State<'_, MediaRoot>,
    request: tauri::ipc::Request<'_>,
) -> Result<Option<String>, String> {
    let tauri::ipc::InvokeBody::Raw(bytes) = request.body() else {
        return Err("请求格式错误".into());
    };
    let bytes = bytes.clone();
    let name = request
        .headers()
        .get(FILE_NAME_HEADER)
        .and_then(|v| v.to_str().ok())
        .and_then(percent_decode)
        .unwrap_or_default();
    let root = root.get();
    let Some(dest) = ask_save_path(&app, &sanitize_file_name(&name)).await else {
        return Ok(None);
    };
    let saved = blocking(move || write_bytes_to(&bytes, &dest, root.as_deref())).await?;
    Ok(Some(saved.to_string_lossy().into_owned()))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmpdir() -> PathBuf {
        let d = std::env::temp_dir().join(format!("miaoju-save-{}", uuid::Uuid::new_v4().simple()));
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    #[test]
    fn extract_key_from_local_url() {
        assert_eq!(
            extract_key("http://127.0.0.1:54321/media/uploads/a%20b/%E5%9B%BE.png?token=x#f").as_deref(),
            Some("uploads/a b/图.png")
        );
        assert_eq!(extract_key("media://gen/1/out?x").as_deref(), Some("gen/1/out"));
    }

    #[test]
    fn extract_key_rejects_foreign_urls() {
        for u in [
            "https://example.com/media/a.png",
            "http://localhost:5173/media/a.png",
            "http://127.0.0.1/media/a.png",
            "http://127.0.0.1:123456/media/a.png",
            "http://127.0.0.1:80/api/v1/x",
            "http://127.0.0.1:80.evil.com/media/a",
            "file:///etc/passwd",
            "blob:http://x/1",
        ] {
            assert_eq!(extract_key(u), None, "{u}");
        }
        assert_eq!(extract_key("http://127.0.0.1:1/media/%zz"), None);
    }

    #[test]
    fn valid_key_matches_go_rules() {
        assert!(valid_key("uploads/2026/a.png"));
        assert!(valid_key("gen/中文 名/out"));
        for k in ["", "/abs", "a/../b", "..", "a/./b", "a//b", "a/b.", "a/b ", "a\\b", "C:/x", "a#b", "a%2e", "a\nb"] {
            assert!(!valid_key(k), "{k:?}");
        }
    }

    #[test]
    fn resolve_rejects_traversal_and_missing() {
        let base = tmpdir();
        let root = base.join("media");
        std::fs::create_dir_all(root.join("u")).unwrap();
        std::fs::write(root.join("u/a.png"), b"img").unwrap();
        std::fs::write(base.join("data.db"), b"secret").unwrap();

        let ok = resolve_media_file(&root, "http://127.0.0.1:9/media/u/a.png?token=t").unwrap();
        assert_eq!(ok, root.join("u/a.png").canonicalize().unwrap());
        // 逃出根目录：编码的 ..、%2F 拼出的 ..
        assert!(resolve_media_file(&root, "http://127.0.0.1:9/media/..%2Fdata.db").is_err());
        assert!(resolve_media_file(&root, "http://127.0.0.1:9/media/%2E%2E/data.db").is_err());
        assert!(resolve_media_file(&root, "media://../data.db").is_err());
        // 目录、不存在
        assert!(resolve_media_file(&root, "media://u").is_err());
        assert!(resolve_media_file(&root, "media://u/none.png").is_err());
        // 非本地 URL
        assert!(resolve_media_file(&root, "https://x.com/media/u/a.png").is_err());
    }

    #[cfg(unix)]
    #[test]
    fn resolve_rejects_symlink_escape() {
        let base = tmpdir();
        let root = base.join("media");
        std::fs::create_dir_all(&root).unwrap();
        std::fs::write(base.join("data.db"), b"secret").unwrap();
        std::os::unix::fs::symlink(base.join("data.db"), root.join("link")).unwrap();
        assert!(resolve_media_file(&root, "media://link").is_err());
    }

    #[test]
    fn sanitize_strips_paths_and_bad_chars() {
        assert_eq!(sanitize_file_name("../../etc/passwd"), "passwd");
        assert_eq!(sanitize_file_name("C:\\Windows\\x.dll"), "x.dll");
        assert_eq!(sanitize_file_name("a:b*c?.png"), "a_b_c_.png");
        assert_eq!(sanitize_file_name("..."), "download");
        assert_eq!(sanitize_file_name(""), "download");
        assert_eq!(sanitize_file_name(".hidden "), "hidden");
        let long = format!("{}.mp4", "长".repeat(100));
        let s = sanitize_file_name(&long);
        assert!(s.len() <= 150 && s.ends_with(".mp4"), "{s}");
    }

    #[test]
    fn copy_and_write_go_to_target_atomically() {
        let base = tmpdir();
        let root = base.join("media");
        std::fs::create_dir_all(&root).unwrap();
        let src = root.join("a.png");
        std::fs::write(&src, b"hello").unwrap();
        let out = base.join("out");
        std::fs::create_dir_all(&out).unwrap();

        let dest = out.join("x.png");
        std::fs::write(&dest, b"old").unwrap();
        copy_media_to(&src, &dest, Some(&root)).unwrap();
        assert_eq!(std::fs::read(&dest).unwrap(), b"hello");

        let z = out.join("h.zip");
        write_bytes_to(b"PK", &z, Some(&root)).unwrap();
        assert_eq!(std::fs::read(&z).unwrap(), b"PK");
        // 没留下临时文件
        let leftovers: Vec<_> = std::fs::read_dir(&out)
            .unwrap()
            .filter_map(|e| e.ok())
            .filter(|e| e.file_name().to_string_lossy().ends_with(".part"))
            .collect();
        assert!(leftovers.is_empty());
    }

    #[test]
    fn refuses_writing_into_media_root_or_over_source() {
        let base = tmpdir();
        let root = base.join("media");
        std::fs::create_dir_all(root.join("sub")).unwrap();
        let src = root.join("a.png");
        std::fs::write(&src, b"hello").unwrap();
        assert!(copy_media_to(&src, &src, Some(&root)).is_err());
        assert!(copy_media_to(&src, &root.join("sub/b.png"), Some(&root)).is_err());
        assert!(write_bytes_to(b"x", &root.join("c.zip"), Some(&root)).is_err());
        assert_eq!(std::fs::read(&src).unwrap(), b"hello");
        // 不存在的父目录
        assert!(write_bytes_to(b"x", &base.join("nope/c.zip"), Some(&root)).is_err());
    }

    #[test]
    fn unique_in_dir_appends_counter() {
        let d = tmpdir();
        assert_eq!(unique_in_dir(&d, "a.png"), d.join("a.png"));
        std::fs::write(d.join("a.png"), b"").unwrap();
        std::fs::write(d.join("a (1).png"), b"").unwrap();
        assert_eq!(unique_in_dir(&d, "a.png"), d.join("a (2).png"));
        std::fs::write(d.join("noext"), b"").unwrap();
        assert_eq!(unique_in_dir(&d, "noext"), d.join("noext (1)"));
    }

    #[test]
    fn media_root_state_roundtrip() {
        let s = MediaRoot::default();
        assert!(s.get().is_none());
        s.set(media_root_of(Path::new("/data")));
        assert_eq!(s.get().unwrap(), Path::new("/data/media"));
    }
}
