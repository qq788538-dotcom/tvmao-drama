#!/usr/bin/env python3
"""棘轮 Ratchet 命令行（里程碑 1）。只用标准库，兼容 macOS 自带 Python 3.9。

子命令：
  new-brief <任务号>         从模板生成 .ratchet/briefs/<任务号>.md
  precheck-brief <文件>       确定性预检：9 项是否齐全、触及 G1–G6 的是否有 James 的批准
  check-report <文件>         回传书检查：必填字段是否齐全、证据路径是否存在
  doctor [--repo 路径]        检查 Mac 工具链与仓库里的棘轮安装状态
  install <目标仓库> [--force] 把棘轮套件装进目标仓库（只在 poteto-CLI 源码里可用）

预检和检查都不调模型、不联网、不花钱。退出码 0 = 通过，1 = 打回。
"""
from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import shutil
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

BRIEF_SECTIONS = [
    "目标", "输入", "信息可见范围", "输出", "禁止事项",
    "验收", "数量与成本上限", "关卡归属", "停止条件",
]
VISIBILITY_LABELS = ["给该 worker", "可见的此前产出", "不可见"]
REPORT_SECTIONS = ["做了什么", "证据路径", "失败尝试", "绕路", "未解决风险"]

# 关卡触发词。命中且前面不是否定词，就视为该任务触及这道关卡。
# 宁可多拦：含糊的一律按"须批"处理（方案"改 7"）。
GATE_KEYWORDS = {
    "G1": r"发布|发帖|发视频|定时发|publish|tweet",
    "G2": r"广告|投放|campaign|出价|加预算|扩受众",
    "G3": r"关注|取关|私信|删帖|改资料|改密码|绑定设备|绑新设备",
    "G4": r"合并|merge|部署|deploy|上线|迁移|migration|生产配置|发版|release",
    "G5": r"付费|计费|node\s+run|quota|配额|购买|订阅|充值|purchase|billing",
    "G6": r"版权|肖像|第三方素材|商标|音乐|copyright|licen[cs]e",
}
NEGATIONS = re.compile(r"(不|禁止|勿|别|不得|不要|不许|不会|无需|严禁|不能|避免|\bno|\bnot|\bnever|\bwithout|\bdon't|\bdo not)\s*\S{0,4}$", re.I)

PLACEHOLDER = re.compile(r"^(TODO|TBD|待填|（填写）|\(填写\)|<.*>|…|\.\.\.)$", re.I)


# ---------------------------------------------------------------------------
# 通用
# ---------------------------------------------------------------------------

def repo_root(start: str | None = None) -> str:
    start = start or os.getcwd()
    try:
        out = subprocess.run(["git", "-C", start, "rev-parse", "--show-toplevel"], capture_output=True, text=True)
        if out.returncode == 0:
            return os.path.realpath(out.stdout.strip())
    except FileNotFoundError:
        pass
    return os.path.realpath(start)


def log_event(root: str, ev: dict) -> None:
    try:
        d = os.path.join(root, ".ratchet", "state")
        os.makedirs(d, exist_ok=True)
        ev = dict(ts=datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"), **ev)
        with open(os.path.join(d, "events.jsonl"), "a", encoding="utf-8") as f:
            f.write(json.dumps(ev, ensure_ascii=False) + "\n")
    except OSError:
        pass


def strip_comments(text: str) -> str:
    return re.sub(r"<!--.*?-->", "", text, flags=re.S)


def parse_front_matter(text: str) -> tuple[dict, str]:
    meta: dict = {}
    m = re.match(r"^---\n(.*?)\n---\n?", text, flags=re.S)
    if not m:
        return meta, text
    for line in m.group(1).splitlines():
        if ":" in line:
            k, v = line.split(":", 1)
            meta[k.strip()] = v.strip()
    return meta, text[m.end():]


def parse_sections(text: str) -> dict:
    """按 `## [序号.] 名称` 切分，返回 {名称: 正文}。"""
    sections: dict = {}
    current = None
    buf: list = []
    for line in text.splitlines():
        m = re.match(r"^##\s+(?:\d+[.、]\s*)?(.+?)\s*$", line)
        if m:
            if current is not None:
                sections[current] = "\n".join(buf).strip()
            current = m.group(1).strip()
            buf = []
        elif current is not None:
            buf.append(line)
    if current is not None:
        sections[current] = "\n".join(buf).strip()
    return sections


def is_empty(body: str | None) -> bool:
    if body is None:
        return True
    lines = [re.sub(r"^[-*]\s*", "", l).strip() for l in body.splitlines()]
    lines = [l for l in lines if l]
    if not lines:
        return True
    return all(PLACEHOLDER.match(l) or re.match(r"^[^：:]+[：:]\s*$", l) for l in lines)


def gates_touched(text: str) -> dict:
    """返回 {关卡: 命中的词}，忽略前面带否定词的命中。"""
    hits: dict = {}
    for gate, pattern in GATE_KEYWORDS.items():
        for m in re.finditer(pattern, text, flags=re.I):
            before = text[max(0, m.start() - 8):m.start()]
            line_before = before.split("\n")[-1]
            if NEGATIONS.search(line_before):
                continue
            hits.setdefault(gate, m.group(0))
            break
    return hits


def approvals_for(root: str, task_id: str) -> set:
    """读 James 写在受保护目录里的批准记录：.ratchet/policy/approvals/<任务号>.md
    其中形如 `- G4: 批准 ...` / `G5 approved ...` 的行算批准。agent 无法写这个目录（闸拦截）。"""
    path = os.path.join(root, ".ratchet", "policy", "approvals", "%s.md" % task_id)
    if not os.path.isfile(path):
        return set()
    ok = set()
    with open(path, encoding="utf-8") as f:
        for line in strip_comments(f.read()).splitlines():
            m = re.match(r"^\s*[-*]?\s*(G[1-6])\s*[:：]\s*(批准|已批|approved)", line, flags=re.I)
            if m:
                ok.add(m.group(1).upper())
    return ok


# ---------------------------------------------------------------------------
# precheck-brief
# ---------------------------------------------------------------------------

def precheck_brief(path: str, root: str) -> list:
    with open(path, encoding="utf-8") as f:
        raw = f.read()
    meta, body = parse_front_matter(strip_comments(raw))
    secs = parse_sections(body)
    problems = []

    task_id = meta.get("id", "")
    if not re.match(r"^T-\d{4,}$", task_id):
        problems.append("front matter 缺少合法的任务号（id: T-0001 这种格式）。")

    for name in BRIEF_SECTIONS:
        if name not in secs:
            problems.append("缺少第「%s」项。" % name)
        elif is_empty(secs[name]):
            problems.append("第「%s」项是空的或只有占位符。" % name)

    vis = secs.get("信息可见范围") or ""
    for label in VISIBILITY_LABELS:
        if label not in vis:
            problems.append("「信息可见范围」缺少「%s」一栏。" % label)

    limits = secs.get("数量与成本上限") or ""
    if not re.search(r"\d", limits):
        problems.append("「数量与成本上限」里没有任何数字（至少写 token 上限或 PR 数上限）。")

    scan = "\n".join(v for k, v in secs.items() if k not in ("禁止事项", "关卡归属", "数量与成本上限"))
    touched = gates_touched(scan)
    # 上限里写"付费生成 0 次"是正常的；写了非零次数才算触及 G5
    m = re.search(r"(%s)[^\n；;，,]*?([1-9]\d*)\s*次" % GATE_KEYWORDS["G5"], limits, flags=re.I)
    if m:
        touched.setdefault("G5", m.group(0))
    approved = approvals_for(root, task_id) if task_id else set()
    owner = secs.get("关卡归属") or ""
    for gate in re.findall(r"G[1-6]", owner):
        touched.setdefault(gate, "关卡归属里列出")
    for gate in sorted(touched):
        if gate not in approved:
            problems.append(
                "触及关卡 %s（命中「%s」），但 .ratchet/policy/approvals/%s.md 里没有 James 对 %s 的批准。"
                "要么删掉这部分指令，要么等 James 批。" % (gate, touched[gate], task_id or "<任务号>", gate)
            )
    return problems


# ---------------------------------------------------------------------------
# check-report
# ---------------------------------------------------------------------------

def check_report(path: str, root: str) -> list:
    with open(path, encoding="utf-8") as f:
        _, body = parse_front_matter(strip_comments(f.read()))
    secs = parse_sections(body)
    problems = []
    for name in REPORT_SECTIONS:
        if name not in secs:
            problems.append("缺少「%s」。" % name)
        elif is_empty(secs[name]):
            problems.append("「%s」是空的。没有就明确写「无」。" % name)
    ev = secs.get("证据路径") or ""
    paths = re.findall(r"`([^`]+)`", ev) or [
        re.sub(r"^[-*]\s*", "", l).strip() for l in ev.splitlines() if l.strip().startswith(("-", "*"))
    ]
    paths = [p for p in paths if p and p != "无"]
    if not paths:
        problems.append("「证据路径」里没有任何路径。没有证据的回传不算完成。")
    for p in paths:
        if re.match(r"^https?://", p):
            continue
        full = p if os.path.isabs(p) else os.path.join(root, p)
        if not os.path.exists(full):
            problems.append("证据路径不存在：%s" % p)
    if re.search(r"^\s*无\s*$", secs.get("证据路径", ""), flags=re.M) and not paths:
        problems.append("「证据路径」不能是「无」。")
    return problems


# ---------------------------------------------------------------------------
# new-brief
# ---------------------------------------------------------------------------

def new_brief(task_id: str, root: str) -> str:
    if not re.match(r"^T-\d{4,}$", task_id):
        raise SystemExit("任务号格式应为 T-0001")
    tpl = os.path.join(root, ".ratchet", "templates", "brief.md")
    if not os.path.isfile(tpl):
        raise SystemExit("找不到模板 %s，先运行 install" % tpl)
    dst_dir = os.path.join(root, ".ratchet", "briefs")
    os.makedirs(dst_dir, exist_ok=True)
    dst = os.path.join(dst_dir, "%s.md" % task_id)
    if os.path.exists(dst):
        raise SystemExit("%s 已存在" % dst)
    with open(tpl, encoding="utf-8") as f:
        text = f.read().replace("T-0000", task_id)
    with open(dst, "w", encoding="utf-8") as f:
        f.write(text)
    return dst


# ---------------------------------------------------------------------------
# doctor
# ---------------------------------------------------------------------------

TOOLS = [
    # (名称, 命令, 最低版本, 是否必须, 用途)
    ("git", ["git", "--version"], None, True, "版本管理"),
    ("python3", ["python3", "--version"], "3.9", True, "闸与预检脚本"),
    ("node", ["node", "--version"], "22", True, "前端与 Claude Code 周边工具"),
    ("pnpm", ["pnpm", "--version"], "10", True, "pnpm dev"),
    ("go", ["go", "version"], "1.26", True, "sidecar 与 miaoju"),
    ("rustc", ["rustc", "--version"], None, True, "Tauri"),
    ("cargo-tauri", ["cargo", "tauri", "--version"], "2.12.1", True, "Tauri CLI"),
    ("ffmpeg", ["ffmpeg", "-version"], None, True, "媒体处理"),
    ("gh", ["gh", "--version"], None, True, "开 PR、查 CI"),
    ("claude", ["claude", "--version"], None, True, "主轴与齿"),
    ("codex", ["codex", "--version"], None, False, "异模型 verifier / judge"),
    ("docker", ["docker", "--version"], None, False, "过夜任务隔离"),
    ("ccusage", ["ccusage", "--version"], None, False, "用量统计（M8）"),
]


def _version_tuple(s: str) -> tuple:
    m = re.search(r"(\d+(?:\.\d+)*)", s)
    return tuple(int(x) for x in m.group(1).split(".")) if m else ()


def _run(cmd: list, cwd: str | None = None) -> tuple:
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=20, cwd=cwd)
        return p.returncode, (p.stdout or p.stderr).strip()
    except (FileNotFoundError, subprocess.TimeoutExpired) as e:
        return 127, str(e)


def doctor(repo: str) -> int:
    failed_required = 0
    print("== 工具链 ==")
    for name, cmd, minimum, required, why in TOOLS:
        code, out = _run(cmd)
        first = out.splitlines()[0] if out else ""
        if code != 0:
            mark = "✗" if required else "–"
            failed_required += required
            print("%s %-12s 未找到%s（%s）" % (mark, name, "" if required else "，可选", why))
            continue
        if minimum and _version_tuple(first) < _version_tuple(minimum):
            failed_required += required
            print("✗ %-12s %s，需要 ≥ %s" % (name, first, minimum))
            continue
        print("✓ %-12s %s" % (name, first))

    code, out = _run(["gh", "auth", "status"])
    failed_required += code != 0
    print("%s gh 登录      %s" % ("✓" if code == 0 else "✗", "已登录" if code == 0 else "未登录：运行 gh auth login"))

    print("\n== 仓库：%s ==" % repo)
    checks = [
        (".claude/settings.json", "权限与 hooks（闸）"),
        ("scripts/ratchet/hooks/pawl.py", "闸脚本"),
        (".ratchet/policy/gates.md", "G1–G6 关卡定义"),
        (".ratchet/templates/brief.md", "任务书模板"),
        (".ratchet/templates/report.md", "回传书模板"),
    ]
    for rel, why in checks:
        ok = os.path.exists(os.path.join(repo, rel))
        failed_required += not ok
        print("%s %-34s %s" % ("✓" if ok else "✗", rel, why))
    settings = os.path.join(repo, ".claude", "settings.json")
    hooked = False
    if os.path.isfile(settings):
        with open(settings, encoding="utf-8") as f:
            hooked = "pawl.py" in f.read()
    failed_required += not hooked
    print("%s settings.json 已接入闸 hook" % ("✓" if hooked else "✗"))

    code, out = _run(["gh", "api", "repos/{owner}/{repo}/branches/main/protection"], cwd=repo)
    if code == 0:
        print("✓ main 已开启分支保护")
    else:
        failed_required += 1
        if re.search(r"HTTP 404|Branch not protected", out):
            print("✗ main 未开启分支保护：GitHub → Settings → Branches 里给 main 加规则：必须 PR、必须 CI 绿")
        else:
            print("✗ main 分支保护未验证（无权查看或查询失败，按未就绪处理）：%s" % (out.splitlines()[-1] if out else ""))
    print("\n提示：`pnpm dev` 能否跑通需要你在仓库里手动跑一次确认（doctor 不会启动应用）。")
    print("结论：%s" % ("未就绪，有 %d 项必需检查没过，不要开始派发。" % failed_required if failed_required else "就绪。"))
    return 1 if failed_required else 0


# ---------------------------------------------------------------------------
# install
# ---------------------------------------------------------------------------

GITIGNORE_LINES = [".ratchet/state/", ".ratchet/evidence/", ".claude/worktrees/", ".claude/settings.local.json"]
# 目标仓库整个忽略 .claude/ 时，闸的 settings.json 和 skill 就进不了库。改成只放行这两项。
CLAUDE_DIR_IGNORES = {".claude/", ".claude", "/.claude/", "/.claude"}
CLAUDE_DIR_REPLACEMENT = [".claude/*", "!.claude/settings.json", "!.claude/skills/"]


def _merge_settings(dst: str, src: str) -> str:
    with open(src, encoding="utf-8") as f:
        ours = json.load(f)
    with open(dst, encoding="utf-8") as f:
        theirs = json.load(f)
    perms = theirs.setdefault("permissions", {})
    deny = perms.setdefault("deny", [])
    for rule in ours["permissions"]["deny"]:
        if rule not in deny:
            deny.append(rule)
    pre = theirs.setdefault("hooks", {}).setdefault("PreToolUse", [])
    if "pawl.py" not in json.dumps(pre):
        pre.extend(ours["hooks"]["PreToolUse"])
    with open(dst, "w", encoding="utf-8") as f:
        json.dump(theirs, f, ensure_ascii=False, indent=2)
        f.write("\n")
    return "合并"


def install(target: str, force: bool) -> int:
    kit = os.path.realpath(os.path.join(HERE, "..", ".."))
    if not os.path.isfile(os.path.join(kit, ".claude", "settings.json")) or os.path.basename(kit) != "kit":
        print("install 只能从 poteto-CLI 源码运行：bin/ratchet install <目标仓库>")
        return 1
    target = os.path.realpath(target)
    if not os.path.isdir(os.path.join(target, ".git")):
        print("%s 不是 git 仓库根目录" % target)
        return 1
    for dirpath, _, files in os.walk(kit):
        for name in files:
            if name.endswith(".pyc"):
                continue
            src = os.path.join(dirpath, name)
            rel = os.path.relpath(src, kit)
            dst = os.path.join(target, rel)
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            if rel == os.path.join(".claude", "settings.json") and os.path.exists(dst) and not force:
                action = _merge_settings(dst, src)
            elif os.path.exists(dst) and not force:
                action = "跳过（已存在）"
            else:
                shutil.copy2(src, dst)
                action = "写入"
            print("%-10s %s" % (action, rel))
    gi = os.path.join(target, ".gitignore")
    existing = open(gi, encoding="utf-8").read() if os.path.exists(gi) else ""
    lines = existing.splitlines()
    if any(l.strip() in CLAUDE_DIR_IGNORES for l in lines):
        new_lines = []
        for l in lines:
            if l.strip() in CLAUDE_DIR_IGNORES:
                new_lines.append("# 棘轮：.claude/ 下只放行闸配置和 skill，其余仍忽略")
                new_lines.extend(CLAUDE_DIR_REPLACEMENT)
            else:
                new_lines.append(l)
        existing = "\n".join(new_lines) + "\n"
        with open(gi, "w", encoding="utf-8") as f:
            f.write(existing)
        print("%-10s .gitignore（.claude/ → 只放行 settings.json 和 skills/）" % "改写")
    add = [l for l in GITIGNORE_LINES if l not in existing.splitlines()]
    if add:
        with open(gi, "a", encoding="utf-8") as f:
            f.write(("\n" if existing and not existing.endswith("\n") else "") + "# 棘轮 Ratchet 运行状态与证据（不入库）\n" + "\n".join(add) + "\n")
        print("%-10s .gitignore（%s）" % ("追加", ", ".join(add)))
    for rel in ("scripts/ratchet/ratchet.py", "scripts/ratchet/hooks/pawl.py"):
        p = os.path.join(target, rel)
        if os.path.exists(p):
            os.chmod(p, 0o755)
    print("\n装好了。下一步：在目标仓库里运行 `python3 scripts/ratchet/ratchet.py doctor`。")
    return 0


# ---------------------------------------------------------------------------

def main(argv: list | None = None) -> int:
    ap = argparse.ArgumentParser(prog="ratchet", description="棘轮 Ratchet 命令行")
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("precheck-brief"); p.add_argument("file")
    p = sub.add_parser("check-report"); p.add_argument("file")
    p = sub.add_parser("new-brief"); p.add_argument("task_id")
    p = sub.add_parser("doctor"); p.add_argument("--repo", default=None)
    p = sub.add_parser("install"); p.add_argument("target"); p.add_argument("--force", action="store_true")
    a = ap.parse_args(argv)

    if a.cmd == "install":
        return install(a.target, a.force)
    if a.cmd == "doctor":
        return doctor(repo_root(a.repo))

    root = repo_root()
    if a.cmd == "new-brief":
        print(new_brief(a.task_id, root))
        return 0
    fn = precheck_brief if a.cmd == "precheck-brief" else check_report
    problems = fn(a.file, root)
    log_event(root, {"type": a.cmd.replace("-", "_"), "file": a.file, "ok": not problems, "problems": problems})
    if problems:
        print("打回（%d 处）：" % len(problems))
        for x in problems:
            print("  - " + x)
        return 1
    print("通过：%s" % a.file)
    return 0


if __name__ == "__main__":
    sys.exit(main())
