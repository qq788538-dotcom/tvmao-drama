//! 版本号单一来源：tauri.conf.json 的 version 指向根 package.json，
//! Cargo.toml 的版本由 scripts/version.sh 同步。这里确认 Tauri 实际读到的版本与 Cargo 版本一致。

#[test]
fn tauri_version_matches_cargo_version() {
    let ctx: tauri::Context<tauri::Wry> = tauri::generate_context!();
    assert_eq!(
        ctx.package_info().version.to_string(),
        env!("CARGO_PKG_VERSION")
    );
}

#[test]
fn tauri_conf_points_version_at_package_json() {
    let conf = include_str!("../tauri.conf.json");
    assert!(
        conf.contains(r#""version": "../package.json""#),
        "tauri.conf.json 的 version 必须是 \"../package.json\""
    );
}
