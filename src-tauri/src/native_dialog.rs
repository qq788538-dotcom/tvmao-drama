//! 不依赖 Tauri 的原生弹窗：用在 Tauri 还没起来（WebView2 缺失）或起不来（build 失败）的时候。
//! Windows release 构建没有控制台，不弹窗用户只会看到闪退。

#[cfg(windows)]
fn wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(std::iter::once(0)).collect()
}

#[cfg(windows)]
fn message_box(
    title: &str,
    msg: &str,
    style: windows::Win32::UI::WindowsAndMessaging::MESSAGEBOX_STYLE,
) -> windows::Win32::UI::WindowsAndMessaging::MESSAGEBOX_RESULT {
    use windows::core::PCWSTR;
    use windows::Win32::UI::WindowsAndMessaging::MessageBoxW;
    let (t, m) = (wide(title), wide(msg));
    unsafe { MessageBoxW(None, PCWSTR(m.as_ptr()), PCWSTR(t.as_ptr()), style) }
}

/// 显示错误提示（Windows 弹窗；其他平台写 stderr）。
pub fn show_error(title: &str, msg: &str) {
    #[cfg(windows)]
    {
        use windows::Win32::UI::WindowsAndMessaging::{MB_ICONERROR, MB_OK};
        message_box(title, msg, MB_OK | MB_ICONERROR);
    }
    #[cfg(not(windows))]
    eprintln!("{title}：{msg}");
}

/// “确定 / 取消”询问，确定返回 true。
#[cfg(windows)]
pub fn confirm(title: &str, msg: &str) -> bool {
    use windows::Win32::UI::WindowsAndMessaging::{IDOK, MB_ICONWARNING, MB_OKCANCEL};
    message_box(title, msg, MB_OKCANCEL | MB_ICONWARNING) == IDOK
}

/// 用默认浏览器打开链接。
#[cfg(windows)]
pub fn open_url(url: &str) {
    let _ = std::process::Command::new("explorer.exe").arg(url).spawn();
}
