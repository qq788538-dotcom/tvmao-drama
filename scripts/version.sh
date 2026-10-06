#!/usr/bin/env bash
# 版本号单一来源：根 package.json 的 version。
# - tauri.conf.json 直接引用它（"version": "../package.json"）；
# - build-sidecar.sh 从它注入 sidecar 的 main.version；
# - 前端用 @tauri-apps/api/app 的 getVersion() 读取（来自 tauri.conf.json）。
# Cargo.toml 和 web/package.json 的 version 只是镜像，由本脚本同步：
#   bash scripts/version.sh check        # 不一致则失败（发版脚本第一步）
#   bash scripts/version.sh set 0.2.0    # 同时改三处并校验
set -euo pipefail
cd "$(dirname "$0")/.."

root_v()  { node -p "require('./package.json').version"; }
web_v()   { node -p "require('./web/package.json').version"; }
conf_v()  { node -p "require('./src-tauri/tauri.conf.json').version"; }
cargo_v() { sed -n 's/^version = "\(.*\)"$/\1/p' src-tauri/Cargo.toml | head -1; }

set_json_version() {
  node -e '
    const fs = require("fs");
    const [file, v] = process.argv.slice(1);
    const j = JSON.parse(fs.readFileSync(file, "utf8"));
    j.version = v;
    fs.writeFileSync(file, JSON.stringify(j, null, 2) + "\n");
  ' "$1" "$2"
}

case "${1:-}" in
  check)
    R="$(root_v)"; ok=1
    [ "$(conf_v)" = "../package.json" ] || { echo "tauri.conf.json 的 version 应为 \"../package.json\"，实际：$(conf_v)" >&2; ok=0; }
    [ "$(web_v)" = "$R" ] || { echo "web/package.json 版本 $(web_v) 与根 package.json $R 不一致" >&2; ok=0; }
    [ "$(cargo_v)" = "$R" ] || { echo "src-tauri/Cargo.toml 版本 $(cargo_v) 与根 package.json $R 不一致" >&2; ok=0; }
    if [ "$ok" = 1 ]; then echo "版本一致：$R"; else exit 1; fi
    ;;
  set)
    V="${2:?用法: bash scripts/version.sh set <x.y.z>}"
    [[ "$V" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "版本号必须是 x.y.z 三段数字：$V" >&2; exit 1; }
    set_json_version package.json "$V"
    set_json_version web/package.json "$V"
    # 只改 [package] 段的第一行 version（依赖写成 { version = ... }，不以 version 开头）
    sed -i '' "1,/^version = /s/^version = \".*\"$/version = \"$V\"/" src-tauri/Cargo.toml
    # 刷新 Cargo.lock 里本包的版本
    ( cd src-tauri && cargo update --workspace --offline >/dev/null 2>&1 )
    bash scripts/version.sh check
    ;;
  *)
    echo "用法: bash scripts/version.sh check | set <x.y.z>" >&2
    exit 1
    ;;
esac
