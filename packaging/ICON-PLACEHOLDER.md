# 图标占位说明

`packaging/icon-1024.png` 目前是程序生成的**占位图**（紫色渐变圆角方块 + 白色字母 M），不是正式设计稿。

拿到正式 1024×1024 设计稿后：

1. 用正式稿覆盖 `packaging/icon-1024.png`
2. 运行 `bash scripts/gen-icons.sh` 重新生成 `src-tauri/icons/*`
3. 提交并删除本文件
