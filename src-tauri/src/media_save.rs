//! “保存到本地”：把媒体文件 / 前端生成的字节写到**用户在系统保存对话框里选的位置**。
//!
//! 为什么不靠锚点 download 属性：wry 0.57 的 WKWebView 导航代理在没有注册 download handler 时，
//! 对 `shouldPerformDownload` 的导航直接回 `Cancel`（见 wry src/wkwebview/navigation.rs），
//! Tauri 只有在 `WebviewBuilder::on_download` 被调用时才注册 handler。本应用没注册，
//! 所以 macOS 上点带 download 属性的锚点没有任何反应。
//!
//! 安全约束：
//! - 写盘目标路径只来自本模块在 Rust 里弹出的保存 / 选文件夹对话框，前端拿不到“往任意路径写”的能力，
//!   前端只有 dialog:allow-open（数据目录选择，只返回路径），不授予 dialog:allow-save / fs:*。
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
        return "download".into();
    }
    // Windows 设备名（CON、NUL.txt、COM1.png…）写进去会变成写设备，所有平台统一加前缀。
    let stem = out.split('.').next().unwrap_or("").trim_end().to_ascii_uppercase();
    let reserved = matches!(stem.as_str(), "CON" | "PRN" | "AUX" | "NUL" | "CONIN$" | "CONOUT$")
        || ((stem.starts_with("COM") || stem.starts_with("LPT"))
            && stem.len() == 4
            && stem.as_bytes()[3].is_ascii_digit());
    if reserved {
        out.insert(0, '_');
    }
    out
}

/// 按文件头魔数识别真实媒体类型，返回规范扩展名。识别不出返回 None。
/// 覆盖 png / jpg / webp / gif / mp4 / mov / m4a / webm / mp3 / wav。
pub fn sniff_media_ext(head: &[u8]) -> Option<&'static str> {
    let h = head;
    if h.starts_with(b"\x89PNG\r\n\x1a\n") {
        return Some("png");
    }
    if h.starts_with(&[0xFF, 0xD8, 0xFF]) {
        return Some("jpg");
    }
    if h.starts_with(b"GIF87a") || h.starts_with(b"GIF89a") {
        return Some("gif");
    }
    if h.len() >= 12 && &h[0..4] == b"RIFF" {
        return match &h[8..12] {
            b"WEBP" => Some("webp"),
            b"WAVE" => Some("wav"),
            _ => None,
        };
    }
    if h.len() >= 12 && &h[4..8] == b"ftyp" {
        return match &h[8..12] {
            b"qt  " => Some("mov"),
            b"M4A " | b"M4B " | b"M4P " => Some("m4a"),
            // HEIF / AVIF 图片也是 ftyp 容器，不当成视频。
            b"heic" | b"heix" | b"heim" | b"heis" | b"hevc" | b"mif1" | b"msf1" | b"avif" | b"avis" => None,
            _ => Some("mp4"),
        };
    }
    if h.starts_with(&[0x1A, 0x45, 0xDF, 0xA3]) {
        // EBML：DocType 为 webm 才认（matroska 不认）。
        let n = h.len().min(64);
        return h[..n].windows(4).any(|w| w == b"webm").then_some("webm");
    }
    if h.starts_with(b"ID3") {
        return Some("mp3");
    }
    // MPEG 音频帧同步：11 位 1 + Layer III（排除 AAC ADTS 的 layer=00）。
    if h.len() >= 2 && h[0] == 0xFF && (h[1] & 0xE0) == 0xE0 && (h[1] & 0x06) == 0x02 && (h[1] & 0x18) != 0x08 {
        return Some("mp3");
    }
    None
}

/// 可接受的同义扩展名（第一个是规范写法）。
fn ext_aliases(ext: &str) -> &'static [&'static str] {
    match ext {
        "png" => &["png"],
        "jpg" => &["jpg", "jpeg", "jpe", "jfif"],
        "webp" => &["webp"],
        "gif" => &["gif"],
        "mp4" => &["mp4", "m4v"],
        "mov" => &["mov", "qt"],
        "webm" => &["webm"],
        "mp3" => &["mp3"],
        "wav" => &["wav", "wave"],
        "m4a" => &["m4a"],
        _ => &[],
    }
}

/// 名字里已有的扩展名若是“某种媒体扩展名”，换掉它；否则（没有或是别的后缀）直接追加。
const KNOWN_MEDIA_EXTS: &[&str] = &[
    "png", "jpg", "jpeg", "jpe", "jfif", "webp", "gif", "bmp", "avif", "heic", "mp4", "m4v", "mov", "qt", "webm",
    "mkv", "avi", "mp3", "wav", "wave", "m4a", "aac", "ogg", "flac", "bin",
];

/// 让文件名的扩展名与真实类型一致。`detected` 为 None 时原样返回（保留调用方的兜底扩展名）。
pub fn fix_extension(name: &str, detected: Option<&str>) -> String {
    let Some(ext) = detected else { return name.to_string() };
    let path = Path::new(name);
    let cur = path.extension().and_then(|e| e.to_str()).map(|e| e.to_ascii_lowercase());
    match cur.as_deref() {
        Some(c) if ext_aliases(ext).contains(&c) => name.to_string(),
        Some(c) if KNOWN_MEDIA_EXTS.contains(&c) => {
            let stem = path.file_stem().and_then(|s| s.to_str()).unwrap_or("download");
            format!("{stem}.{ext}")
        }
        _ => format!("{name}.{ext}"),
    }
}

/// 读文件开头若干字节用于识别类型。
fn read_head(path: &Path) -> Vec<u8> {
    use std::io::Read;
    let mut buf = vec![0u8; 64];
    let n = std::fs::File::open(path).and_then(|mut f| f.read(&mut buf)).unwrap_or(0);
    buf.truncate(n);
    buf
}

/// 目标路径的规范形式（文件可能还不存在，所以规范化父目录再拼文件名）。
fn canonical_target(dest: &Path) -> Result<PathBuf, String> {
    let parent = dest.parent().filter(|p| !p.as_os_str().is_empty()).unwrap_or(Path::new("."));
    let name = dest.file_name().ok_or_else(|| "保存位置无效".to_string())?;
    let parent = parent.canonicalize().map_err(|e| format!("保存位置不可用：{e}"))?;
    Ok(parent.join(name))
}

/// 拒绝写进媒体根目录（会污染 / 覆盖 sidecar 管理的文件），也拒绝覆盖数据目录里的 data.db（及 -wal / -shm）。
fn check_target(dest: &Path, media_root: Option<&Path>) -> Result<PathBuf, String> {
    let target = canonical_target(dest)?;
    if let Some(root) = media_root.and_then(|r| r.canonicalize().ok()) {
        let in_data_dir = root.parent().is_some_and(|d| target.parent() == Some(d));
        let is_db = target
            .file_name()
            .and_then(|n| n.to_str())
            .is_some_and(|n| n.to_ascii_lowercase().starts_with("data.db"));
        if target.starts_with(&root) || (in_data_dir && is_db) {
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

/// 候选名：`a.png`、`a (1).png`、`a (2).png` …
fn candidate_name(name: &str, i: usize) -> String {
    if i == 0 {
        return name.to_string();
    }
    let p = Path::new(name);
    let stem = p.file_stem().and_then(|s| s.to_str()).unwrap_or(name);
    let ext = p.extension().and_then(|e| e.to_str()).map(|e| format!(".{e}")).unwrap_or_default();
    format!("{stem} ({i}){ext}")
}

const MAX_NAME_TRIES: usize = 10_000;

/// 在目标文件夹里**原子地占住**一个不冲突的文件名（`create_new` = O_EXCL），返回占位文件路径。
/// 先查 exists 再写会有竞争窗口，最后的 rename 会静默覆盖期间出现的同名文件；占位后 rename 只会替换自己的空占位文件。
pub fn reserve_unique_in_dir(dir: &Path, name: &str) -> Result<PathBuf, String> {
    for i in 0..MAX_NAME_TRIES {
        let p = dir.join(candidate_name(name, i));
        match std::fs::OpenOptions::new().write(true).create_new(true).open(&p) {
            Ok(_) => return Ok(p),
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(e) => return Err(format!("写入失败：{e}")),
        }
    }
    Err("同名文件太多，请换一个文件夹".into())
}

/// 复制到一个已占住的路径；失败时删掉占位文件。
fn copy_into_reserved(src: &Path, reserved: &Path, media_root: Option<&Path>) -> Result<PathBuf, String> {
    let res = copy_media_to(src, reserved, media_root);
    if res.is_err() {
        let _ = std::fs::remove_file(reserved);
    }
    res
}

fn write_into_reserved(bytes: &[u8], reserved: &Path, media_root: Option<&Path>) -> Result<PathBuf, String> {
    let res = write_bytes_to(bytes, reserved, media_root);
    if res.is_err() {
        let _ = std::fs::remove_file(reserved);
    }
    res
}

/// 用户在对话框里选的路径，扩展名与真实类型不符时改正。
/// 改名后的路径用户没在对话框里确认过，所以不许覆盖：已存在就加序号，并原子占位。
/// 返回 (最终路径, 是否为占位文件)。
fn corrected_dest(dest: PathBuf, detected: Option<&str>, media_root: Option<&Path>) -> Result<(PathBuf, bool), String> {
    let Some(name) = dest.file_name().and_then(|n| n.to_str()).map(str::to_string) else {
        return Ok((dest, false));
    };
    let fixed = fix_extension(&name, detected);
    if fixed == name {
        return Ok((dest, false));
    }
    // 先检查位置，免得在媒体根目录里留下占位文件。
    let dest = check_target(&dest, media_root)?;
    let dir = dest.parent().map(Path::to_path_buf).unwrap_or_default();
    Ok((reserve_unique_in_dir(&dir, &fixed)?, true))
}

/// 批量最多保存多少个文件。
pub const MAX_BATCH_ITEMS: usize = 5_000;

/// save_bytes 接受的最大字节数（2 GiB）。前端同样限制，超出不发送。
pub const MAX_SAVE_BYTES: usize = 2 * 1024 * 1024 * 1024;

pub fn check_bytes_len(len: usize) -> Result<(), String> {
    if len == 0 {
        return Err("没有可保存的内容".into());
    }
    if len > MAX_SAVE_BYTES {
        return Err("文件过大（超过 2 GB），无法保存".into());
    }
    Ok(())
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
    let detected = sniff_media_ext(&read_head(&src));
    let suggested = fix_extension(&sanitize_file_name(&suggested_name), detected);
    let Some(dest) = ask_save_path(&app, &suggested).await else {
        return Ok(None);
    };
    let saved = blocking(move || {
        let (dest, reserved) = corrected_dest(dest, detected, Some(&root))?;
        if reserved {
            copy_into_reserved(&src, &dest, Some(&root))
        } else {
            copy_media_to(&src, &dest, Some(&root))
        }
    })
    .await?;
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
    if items.len() > MAX_BATCH_ITEMS {
        return Err(format!("一次最多保存 {MAX_BATCH_ITEMS} 个文件"));
    }
    let mut plan = Vec::with_capacity(items.len());
    for it in &items {
        let src = resolve_media_file(&root, &it.url)?;
        let name = fix_extension(&sanitize_file_name(&it.name), sniff_media_ext(&read_head(&src)));
        plan.push((src, name));
    }
    if plan.is_empty() {
        return Ok(Some(0));
    }
    let Some(dir) = ask_folder(&app).await else {
        return Ok(None);
    };
    blocking(move || save_batch_into(&plan, &dir, &root)).await.map(Some)
}

/// 批量复制进用户选的文件夹：文件名已清洗（不含路径分隔符），每个目标原子占位，绝不覆盖已有文件。
pub fn save_batch_into(plan: &[(PathBuf, String)], dir: &Path, media_root: &Path) -> Result<usize, String> {
    // 先整体检查文件夹本身（不在媒体根目录里），免得复制了一半才报错。
    check_target(&dir.join("probe"), Some(media_root))?;
    for (src, name) in plan {
        let dest = reserve_unique_in_dir(dir, name)?;
        copy_into_reserved(src, &dest, Some(media_root))?;
    }
    Ok(plan.len())
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
    // IPC 层已经把整个 body 收进内存；这里至少不再复制超大内容、也不弹对话框。
    check_bytes_len(bytes.len())?;
    let bytes = bytes.clone();
    let name = request
        .headers()
        .get(FILE_NAME_HEADER)
        .and_then(|v| v.to_str().ok())
        .and_then(percent_decode)
        .unwrap_or_default();
    // 数据目录未知时拒绝，不能跳过“不能写进数据目录 / 覆盖 data.db”的保护。
    let root = Some(media_root_required(&root)?);
    let detected = sniff_media_ext(&bytes[..bytes.len().min(64)]);
    let suggested = fix_extension(&sanitize_file_name(&name), detected);
    let Some(dest) = ask_save_path(&app, &suggested).await else {
        return Ok(None);
    };
    let saved = blocking(move || {
        let (dest, reserved) = corrected_dest(dest, detected, root.as_deref())?;
        if reserved {
            write_into_reserved(&bytes, &dest, root.as_deref())
        } else {
            write_bytes_to(&bytes, &dest, root.as_deref())
        }
    })
    .await?;
    Ok(Some(saved.to_string_lossy().into_owned()))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmpdir() -> PathBuf {
        let d = std::env::temp_dir().join(format!("miaoju-save-{}", uuid::Uuid::new_v4().simple()));
        std::fs::create_dir_all(&d).unwrap();
        d.canonicalize().unwrap()
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
    fn reserve_unique_appends_counter_and_never_reuses() {
        let d = tmpdir();
        assert_eq!(reserve_unique_in_dir(&d, "a.png").unwrap(), d.join("a.png"));
        // 已占住的名字（含刚才的占位文件）不会再被分配
        std::fs::write(d.join("a (1).png"), b"keep").unwrap();
        assert_eq!(reserve_unique_in_dir(&d, "a.png").unwrap(), d.join("a (2).png"));
        std::fs::write(d.join("noext"), b"").unwrap();
        assert_eq!(reserve_unique_in_dir(&d, "noext").unwrap(), d.join("noext (1)"));
        assert_eq!(std::fs::read(d.join("a (1).png")).unwrap(), b"keep");
    }

    #[test]
    fn batch_never_overwrites_and_stays_in_folder() {
        let base = tmpdir();
        let root = base.join("media");
        std::fs::create_dir_all(&root).unwrap();
        let a = root.join("a");
        std::fs::write(&a, b"\x89PNG\r\n\x1a\nAAAA").unwrap();
        let out = base.join("out");
        std::fs::create_dir_all(&out).unwrap();
        std::fs::write(out.join("x.png"), b"user").unwrap();
        // 恶意名字经 sanitize 后只剩最后一段
        let evil = sanitize_file_name("../../escape.png");
        let plan = vec![(a.clone(), "x.png".to_string()), (a.clone(), "x.png".to_string()), (a.clone(), evil)];
        assert_eq!(save_batch_into(&plan, &out, &root).unwrap(), 3);
        assert_eq!(std::fs::read(out.join("x.png")).unwrap(), b"user");
        assert!(out.join("x (1).png").is_file() && out.join("x (2).png").is_file());
        assert!(out.join("escape.png").is_file());
        assert!(!base.join("escape.png").exists());
        // 选的文件夹在媒体根目录里：直接拒绝，不留文件
        let inner = root.join("sub");
        std::fs::create_dir_all(&inner).unwrap();
        assert!(save_batch_into(&plan, &inner, &root).is_err());
        assert_eq!(std::fs::read_dir(&inner).unwrap().count(), 0);
    }

    #[test]
    fn sanitize_guards_windows_device_names() {
        assert_eq!(sanitize_file_name("CON"), "_CON");
        assert_eq!(sanitize_file_name("nul.txt"), "_nul.txt");
        assert_eq!(sanitize_file_name("com1.png"), "_com1.png");
        assert_eq!(sanitize_file_name("LPT9"), "_LPT9");
        assert_eq!(sanitize_file_name("console.png"), "console.png");
        assert_eq!(sanitize_file_name("com10.png"), "com10.png");
        // 末尾的点 / 空格去掉后不能剩下 ".."
        assert_eq!(sanitize_file_name(".."), "download");
        assert_eq!(sanitize_file_name("a/.."), "download");
    }

    #[test]
    fn refuses_overwriting_data_db() {
        let base = tmpdir();
        let root = base.join("media");
        std::fs::create_dir_all(&root).unwrap();
        std::fs::write(base.join("data.db"), b"secret").unwrap();
        for n in ["data.db", "data.db-wal", "DATA.DB-shm"] {
            assert!(write_bytes_to(b"x", &base.join(n), Some(&root)).is_err(), "{n}");
        }
        assert_eq!(std::fs::read(base.join("data.db")).unwrap(), b"secret");
        // 数据目录里别的名字（用户自己选的）不拦
        assert!(write_bytes_to(b"x", &base.join("ok.png"), Some(&root)).is_ok());
    }

    #[test]
    fn bytes_len_limit() {
        assert!(check_bytes_len(0).is_err());
        assert!(check_bytes_len(1).is_ok());
        assert!(check_bytes_len(MAX_SAVE_BYTES).is_ok());
        assert!(check_bytes_len(MAX_SAVE_BYTES + 1).is_err());
    }

    fn ftyp(brand: &[u8; 4]) -> Vec<u8> {
        let mut v = vec![0, 0, 0, 0x20];
        v.extend_from_slice(b"ftyp");
        v.extend_from_slice(brand);
        v.extend_from_slice(&[0; 8]);
        v
    }

    #[test]
    fn sniff_detects_real_types() {
        let riff = |kind: &[u8; 4]| [b"RIFF".as_slice(), &[0, 0, 0, 0], kind].concat();
        let cases: Vec<(Vec<u8>, Option<&str>)> = vec![
            (b"\x89PNG\r\n\x1a\n....".to_vec(), Some("png")),
            (vec![0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10], Some("jpg")),
            (riff(b"WEBP"), Some("webp")),
            (b"GIF89a....".to_vec(), Some("gif")),
            (b"GIF87a....".to_vec(), Some("gif")),
            (ftyp(b"isom"), Some("mp4")),
            (ftyp(b"mp42"), Some("mp4")),
            (ftyp(b"qt  "), Some("mov")),
            (ftyp(b"M4A "), Some("m4a")),
            (ftyp(b"avif"), None),
            (ftyp(b"heic"), None),
            ([&[0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x82, 0x84][..], b"webm"].concat(), Some("webm")),
            ([&[0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x82, 0x88][..], b"matroska"].concat(), None),
            (b"ID3\x04\x00".to_vec(), Some("mp3")),
            (vec![0xFF, 0xFB, 0x90, 0x64], Some("mp3")),
            (vec![0xFF, 0xF1, 0x50, 0x80], None), // AAC ADTS 不是 mp3
            (riff(b"WAVE"), Some("wav")),
            (riff(b"AVI "), None),
            (b"PK\x03\x04".to_vec(), None),
            (b"{\"a\":1}".to_vec(), None),
            (vec![], None),
        ];
        for (head, want) in cases {
            assert_eq!(sniff_media_ext(&head), want, "{head:?}");
        }
    }

    #[test]
    fn fix_extension_rules() {
        assert_eq!(fix_extension("out.png", Some("jpg")), "out.jpg");
        assert_eq!(fix_extension("out.jpeg", Some("jpg")), "out.jpeg");
        assert_eq!(fix_extension("out.PNG", Some("png")), "out.PNG");
        assert_eq!(fix_extension("clip.mp4", Some("webm")), "clip.webm");
        assert_eq!(fix_extension("voice.mp3", Some("wav")), "voice.wav");
        assert_eq!(fix_extension("out", Some("webp")), "out.webp");
        assert_eq!(fix_extension("镜头1.第二版", Some("png")), "镜头1.第二版.png");
        assert_eq!(fix_extension("pack.zip", None), "pack.zip");
        assert_eq!(fix_extension("out.png", None), "out.png");
    }

    #[test]
    fn corrected_dest_never_overwrites_unconfirmed_file() {
        let d = tmpdir();
        // 扩展名正确：保持用户在对话框里选的路径（对话框已确认覆盖）
        let (p, reserved) = corrected_dest(d.join("a.jpg"), Some("jpg"), None).unwrap();
        assert_eq!((p, reserved), (d.join("a.jpg"), false));
        // 需要改名且改后的文件已存在：加序号，不覆盖
        std::fs::write(d.join("b.jpg"), b"user").unwrap();
        let (p, reserved) = corrected_dest(d.join("b.png"), Some("jpg"), None).unwrap();
        assert_eq!((p.clone(), reserved), (d.join("b (1).jpg"), true));
        write_into_reserved(b"\xFF\xD8\xFFdata", &p, None).unwrap();
        assert_eq!(std::fs::read(d.join("b.jpg")).unwrap(), b"user");
        assert!(!d.join("b.png").exists());
        // 识别不出：原样
        let (p, reserved) = corrected_dest(d.join("c.zip"), None, None).unwrap();
        assert_eq!((p, reserved), (d.join("c.zip"), false));
        // 改名目标在媒体根目录里：拒绝，且不留占位文件
        let root = d.join("media");
        std::fs::create_dir_all(&root).unwrap();
        assert!(corrected_dest(root.join("e.png"), Some("jpg"), Some(&root)).is_err());
        assert_eq!(std::fs::read_dir(&root).unwrap().count(), 0);
    }

    #[test]
    fn failed_copy_into_reserved_removes_placeholder() {
        let d = tmpdir();
        let p = reserve_unique_in_dir(&d, "x.png").unwrap();
        assert!(copy_into_reserved(&d.join("missing"), &p, None).is_err());
        assert!(!p.exists());
    }

    #[test]
    fn media_root_state_roundtrip() {
        let s = MediaRoot::default();
        assert!(s.get().is_none());
        // 数据目录未知（迁移中 / sidecar 未启动）时拒绝保存，不能跳过“不能写进数据目录”的保护
        assert!(media_root_required(&s).is_err());
        s.set(media_root_of(Path::new("/data")));
        assert_eq!(s.get().unwrap(), Path::new("/data/media"));
    }
}
