// Windows release 构建不弹出控制台窗口。
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    miaoju_desktop_lib::run()
}
