"""把目录打成 zip，zip 内保留顶层目录名。
用 python 的 zipfile：文件名含中文时会自动设置 UTF-8 标记，Windows 资源管理器解压才不会乱码。
用法：python3 scripts/lib/make_zip.py <目录> <输出.zip>
"""
import os
import sys
import zipfile


def main() -> None:
    src, out = sys.argv[1], sys.argv[2]
    base = os.path.dirname(os.path.abspath(src))
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        for root, dirs, files in os.walk(src):
            dirs.sort()
            for name in sorted(files):
                path = os.path.join(root, name)
                z.write(path, os.path.relpath(path, base))


if __name__ == "__main__":
    main()
