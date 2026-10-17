//! Windows：建窗口前检测系统 WebView2 运行时。检测不到就弹窗给出微软官方下载链接，然后退出。
//! 检测用微软推荐的 GetAvailableCoreWebView2BrowserVersionString，常青版和固定版都能识别。

/// 微软官方“常青版引导程序”下载链接。
pub const WEBVIEW2_DOWNLOAD_URL: &str = "https://go.microsoft.com/fwlink/p/?LinkId=2124703";
/// 微软官方 WebView2 说明页。
pub const WEBVIEW2_INFO_URL: &str = "https://developer.microsoft.com/zh-cn/microsoft-edge/webview2/";

pub fn missing_message() -> String {
    format!(
        "喵剧AI 需要 Microsoft Edge WebView2 运行时，但这台电脑上没有检测到。\n\n\
         点击“确定”下载微软官方安装程序，安装完成后重新打开喵剧AI。\n\n\
         下载地址：{WEBVIEW2_DOWNLOAD_URL}\n说明页面：{WEBVIEW2_INFO_URL}"
    )
}

/// 版本字符串可用：非空且不是 "0.0.0.0"。
pub fn is_usable_version(v: Option<&str>) -> bool {
    matches!(v.map(str::trim), Some(s) if !s.is_empty() && s != "0.0.0.0")
}

#[cfg(windows)]
pub fn installed_version() -> Option<String> {
    use webview2_com::Microsoft::Web::WebView2::Win32::GetAvailableCoreWebView2BrowserVersionString;
    use windows::core::{PCWSTR, PWSTR};
    use windows::Win32::System::Com::CoTaskMemFree;

    let mut out = PWSTR::null();
    unsafe {
        GetAvailableCoreWebView2BrowserVersionString(PCWSTR::null(), &mut out).ok()?;
        if out.is_null() {
            return None;
        }
        let s = out.to_string().ok();
        CoTaskMemFree(Some(out.0 as *const _));
        s
    }
}

/// 检测不到 WebView2 就弹窗并退出进程。必须在创建 Tauri 应用之前调用。
#[cfg(windows)]
pub fn ensure_or_exit() {
    let fake_missing = cfg!(debug_assertions) && std::env::var_os("MIAOJU_FAKE_NO_WEBVIEW2").is_some();
    let version = if fake_missing { None } else { installed_version() };
    if is_usable_version(version.as_deref()) {
        return;
    }
    if crate::native_dialog::confirm("缺少 WebView2 运行时", &missing_message()) {
        crate::native_dialog::open_url(WEBVIEW2_DOWNLOAD_URL);
    }
    std::process::exit(1);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn usable_version_rules() {
        assert!(is_usable_version(Some("129.0.2792.65")));
        assert!(!is_usable_version(None));
        assert!(!is_usable_version(Some("")));
        assert!(!is_usable_version(Some("  ")));
        assert!(!is_usable_version(Some("0.0.0.0")));
    }

    #[test]
    fn message_contains_official_link() {
        let m = missing_message();
        assert!(m.contains(WEBVIEW2_DOWNLOAD_URL));
        assert!(WEBVIEW2_DOWNLOAD_URL.starts_with("https://go.microsoft.com/"));
        assert!(m.contains("WebView2"));
    }
}
