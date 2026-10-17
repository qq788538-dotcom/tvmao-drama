//! 前端权限（SEC4）：capabilities/default.json 只放前端真正用到的权限。
//! core:default 带 core:image:allow-from-path（网页可按任意路径读图片像素）、core:path、core:menu、core:tray 等，
//! 一旦网页被注入脚本就能借这些能力读盘或伪造界面，所以不能整组授予。

fn permissions() -> Vec<String> {
    let v: serde_json::Value = serde_json::from_str(include_str!("../capabilities/default.json")).unwrap();
    v["permissions"]
        .as_array()
        .unwrap()
        .iter()
        .map(|p| match p {
            serde_json::Value::String(s) => s.clone(),
            other => other["identifier"].as_str().unwrap().to_string(),
        })
        .collect()
}

#[test]
fn frontend_capability_is_minimal() {
    let perms = permissions();
    for p in &perms {
        assert!(
            !p.ends_with(":default") || p == "core:event:default",
            "不要整组授予 {p}，按需列出具体权限"
        );
        for banned in ["core:image", "core:path", "core:menu", "core:tray", "core:window", "fs:", "shell:", "dialog:allow-save", "opener:allow-open-path", "opener:allow-reveal"] {
            assert!(!p.starts_with(banned), "前端不应有 {p}");
        }
    }
    for needed in ["core:event:default", "core:app:allow-version", "dialog:allow-open", "opener:allow-open-url"] {
        assert!(perms.iter().any(|p| p == needed), "缺少前端用到的 {needed}");
    }
}
