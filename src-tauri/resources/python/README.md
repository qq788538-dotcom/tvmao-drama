# 内置 Python 运行时

本目录由 `scripts/fetch-python.sh <target-triple>` 生成（python-build-standalone，CPython 3.12.15），
供脚本节点执行 Skill 校验脚本。除本文件外全部内容都不入库。
本文件必须保留：tauri.conf.json 的 bundle.resources 引用了本目录，目录不存在时 cargo 编译会失败。
