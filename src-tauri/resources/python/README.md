# 内置 Python 占位目录

本目录由 `scripts/fetch-python.sh <target-triple>` 填充（python-build-standalone），其余内容不入库。
目录本身必须存在，否则 tauri-build 因 bundle.resources 找不到源路径而失败。
