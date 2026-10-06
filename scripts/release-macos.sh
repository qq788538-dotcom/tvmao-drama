#!/usr/bin/env bash
# 构建 macOS 发布包：out/喵剧AI_<版本>_aarch64.dmg（Apple 芯片）和 out/喵剧AI_<版本>_x64.dmg（Intel）。
# 不做开发者签名和公证（spec E5）：Tauri 按 signingIdentity "-" 做 ad-hoc 签名；dmg 内附《首次打开说明.txt》。
# Tauri 的 dmg 配置不能往卷里放额外文件，所以 Tauri 只出 .app，dmg 由 hdiutil 生成。
# 用法：bash scripts/release-macos.sh [aarch64|x64|all]（默认 all）
set -euo pipefail
cd "$(dirname "$0")/.."

WHICH="${1:-all}"
PRODUCT="喵剧AI"
bash scripts/version.sh check
VERSION="$(node -p "require('./package.json').version")"
mkdir -p out

verify_dmg() {
  local dmg="$1" lipo_arch="$2" mnt
  mnt="$(mktemp -d)"
  hdiutil attach -nobrowse -readonly -mountpoint "$mnt" "$dmg" >/dev/null
  local ok=1
  [ -d "$mnt/$PRODUCT.app" ] || { echo "dmg 里缺少 $PRODUCT.app" >&2; ok=0; }
  [ -L "$mnt/Applications" ] || { echo "dmg 里缺少 Applications 快捷方式" >&2; ok=0; }
  [ -f "$mnt/首次打开说明.txt" ] || { echo "dmg 里缺少 首次打开说明.txt" >&2; ok=0; }
  codesign --verify --deep --strict "$mnt/$PRODUCT.app" || ok=0
  for bin in miaoju sidecar; do
    [ "$(lipo -archs "$mnt/$PRODUCT.app/Contents/MacOS/$bin")" = "$lipo_arch" ] \
      || { echo "$bin 架构不是 $lipo_arch" >&2; ok=0; }
  done
  hdiutil detach "$mnt" >/dev/null
  rmdir "$mnt"
  [ "$ok" = 1 ]
}

build_one() {
  local arch="$1" target lipo_arch
  case "$arch" in
    aarch64) target=aarch64-apple-darwin; lipo_arch=arm64 ;;
    x64)     target=x86_64-apple-darwin;  lipo_arch=x86_64 ;;
    *) echo "未知架构：${arch}（可选 aarch64 / x64 / all）" >&2; exit 1 ;;
  esac

  bash scripts/prebuild.sh "$target"
  # build-sidecar.sh 准备 Python 失败只警告；打包必须有对应架构的内置 Python，否则报错退出。
  bash scripts/lib/require-python.sh "$target"
  cargo tauri build --target "$target" --bundles app

  local app="src-tauri/target/$target/release/bundle/macos/$PRODUCT.app"
  [ -d "$app" ] || { echo "缺少构建产物 $app" >&2; exit 1; }
  [ -f "$app/Contents/Resources/python/bin/python3" ] || { echo "$app 里缺少内置 Python" >&2; exit 1; }
  # 签名必须完整且是 ad-hoc；资源要在 Tauri 签名前进包，签名后改动包内容会让 macOS 报“已损坏”
  codesign --verify --deep --strict "$app"
  # 先落变量再 grep：管道里 grep -q 提前退出会让 codesign 吃 SIGPIPE，pipefail 下偶发误判
  local sig_info
  sig_info="$(codesign -dv "$app" 2>&1)"
  grep -q 'Signature=adhoc' <<<"$sig_info" || { echo "$app 不是 ad-hoc 签名" >&2; exit 1; }
  [ "$(/usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$app/Contents/Info.plist")" = "14.0" ] \
    || { echo "LSMinimumSystemVersion 不是 14.0" >&2; exit 1; }
  [ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist")" = "$VERSION" ] \
    || { echo "包内版本号与 package.json 不一致" >&2; exit 1; }

  local stage="out/dmg-stage-$arch" dmg="out/${PRODUCT}_${VERSION}_${arch}.dmg"
  rm -rf "$stage" "$dmg"
  mkdir -p "$stage"
  ditto "$app" "$stage/$PRODUCT.app"
  ln -s /Applications "$stage/Applications"
  cp "packaging/首次打开说明-macOS.txt" "$stage/首次打开说明.txt"
  hdiutil create -volname "$PRODUCT $VERSION" -srcfolder "$stage" -fs HFS+ -format UDZO -ov "$dmg" >/dev/null
  rm -rf "$stage"

  verify_dmg "$dmg" "$lipo_arch"
  echo "已生成 ${dmg}（$(du -h "$dmg" | cut -f1)）"
}

case "$WHICH" in
  all) build_one aarch64; build_one x64 ;;
  aarch64|x64) build_one "$WHICH" ;;
  *) echo "用法: bash scripts/release-macos.sh [aarch64|x64|all]" >&2; exit 1 ;;
esac
