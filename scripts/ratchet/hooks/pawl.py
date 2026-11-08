#!/usr/bin/env python3
"""闸 Pawl：棘轮的确定性守门 hook（Claude Code PreToolUse）。

这个文件是红线：它自己也在受保护路径里，agent 不能改它。
只做脚本判断，不调模型、不联网、不花钱。

用法（由 .claude/settings.json 调用，从 stdin 读 hook JSON）：
    python3 pawl.py            # 自动按 tool_name 分派
退出码 0 = 放行；2 = 拦下（原因写到 stderr，Claude Code 会把它交给模型）。
内部出错时按"拦下"处理（fail-closed）。

兼容 macOS 自带的 Python 3.9，只用标准库。
"""
from __future__ import annotations

import datetime
import json
import os
import re
import shlex
import subprocess
import sys

# ---------------------------------------------------------------------------
# 规则表（改这里需要 James 本人改；agent 改不了本文件）
# ---------------------------------------------------------------------------

# 只有 James 能改的路径（相对仓库根）。以 / 结尾的是目录前缀。
PROTECTED_PATHS = (
    ".ratchet/policy/",
    "eval/holdout/",
    "eval/rubrics/",
    ".claude/settings.json",
    ".claude/settings.local.json",
    ".github/workflows/",
    "scripts/ratchet/hooks/",
)

# 受保护的分支：不许 push 到这些分支，也不许删除它们。
PROTECTED_BRANCHES = ("main", "master")

# 允许 rm -r 的仓库外目录（临时目录）。
TEMP_PREFIXES = ("/tmp/", "/private/tmp/", "/var/folders/", "/private/var/folders/")

# 读到 protected 路径时允许的只读命令。其余命令只要提到受保护路径就拦。
READ_ONLY_CMDS = {
    "cat", "less", "more", "head", "tail", "grep", "egrep", "fgrep", "rg",
    "ls", "wc", "diff", "stat", "file", "bat", "jq", "yq", "md5", "md5sum",
    "shasum", "sha256sum", "test", "[", "realpath", "dirname", "basename",
}
READ_ONLY_GIT = {"diff", "log", "show", "status", "blame", "ls-files", "grep", "cat-file"}

# 密钥类文件：只读也不行。
_SECRET_BASENAME = re.compile(
    r"^(\.env(\..+)?|\.envrc|id_rsa.*|id_ed25519.*|id_ecdsa.*|.*\.pem|.*\.p12|.*\.pfx|"
    r".*\.key|.*\.keystore|credentials(\..+)?|\.netrc|\.npmrc|\.pypirc)$"
)
_SECRET_ALLOWED = re.compile(r"^\.env\.(example|sample|template|dist)$")
_SECRET_DIRS = {".ssh", ".aws", ".gnupg", ".docker", ".kube"}

SEGMENT_OPS = {";", "&&", "||", "|", "&", "|&"}
REDIRECT_OPS = {">", ">>", "<", ">|", "&>", "&>>", "<>"}
WRAPPERS = {"sudo", "env", "command", "nohup", "time", "nice", "exec", "xargs", "builtin"}
SHELLS = {"bash", "sh", "zsh", "dash", "fish"}


_CURRENT: dict = {}


class Block(Exception):
    def __init__(self, rule: str, message: str):
        super().__init__(message)
        self.rule = rule
        self.message = message


# ---------------------------------------------------------------------------
# 路径工具
# ---------------------------------------------------------------------------

def _real(p: str) -> str:
    return os.path.realpath(os.path.expanduser(p))


def project_root(data: dict) -> str:
    root = os.environ.get("CLAUDE_PROJECT_DIR") or data.get("cwd") or os.getcwd()
    return _real(root)


def resolve(path: str, cwd: str) -> str:
    path = os.path.expanduser(path)
    if not os.path.isabs(path):
        path = os.path.join(cwd, path)
    return _real(path)


def rel_to(abs_path: str, root: str) -> str | None:
    """abs_path 在 root 内则返回相对路径（posix），否则 None。"""
    if abs_path == root:
        return ""
    if abs_path.startswith(root.rstrip("/") + "/"):
        return abs_path[len(root.rstrip("/")) + 1:]
    return None


def worktree_relative(rel: str) -> str:
    """worker 的 worktree 在 .claude/worktrees/<name>/ 下，规则按 worktree 内的相对路径算。"""
    m = re.match(r"^\.claude/worktrees/[^/]+/(.*)$", rel)
    return m.group(1) if m else rel


def is_protected(rel: str | None) -> str | None:
    if rel is None:
        return None
    rel = worktree_relative(rel)
    for p in PROTECTED_PATHS:
        if p.endswith("/"):
            if rel == p.rstrip("/") or rel.startswith(p):
                return p
        elif rel == p:
            return p
    return None


def contains_protected(rel: str | None) -> str | None:
    """rel 是受保护路径本身、其后代或其祖先目录时返回命中的保护路径（删除/移动整棵子树用）。"""
    if rel is None:
        return None
    hit = is_protected(rel)
    if hit:
        return hit
    r = worktree_relative(rel).rstrip("/")
    for p in PROTECTED_PATHS:
        if r == "" or p.startswith(r + "/"):
            return p
    return None


def is_secret(path: str) -> bool:
    norm = path.replace("\\", "/")
    parts = [x for x in norm.split("/") if x]
    if not parts:
        return False
    base = parts[-1]
    if _SECRET_ALLOWED.match(base):
        return False
    if _SECRET_BASENAME.match(base):
        return True
    return any(part in _SECRET_DIRS for part in parts[:-1]) or base in _SECRET_DIRS


def is_temp(abs_path: str) -> bool:
    tmp = os.environ.get("TMPDIR")
    prefixes = list(TEMP_PREFIXES)
    if tmp:
        prefixes.append(_real(tmp).rstrip("/") + "/")
    return any(abs_path.startswith(p) for p in prefixes)


# ---------------------------------------------------------------------------
# Bash 命令解析
# ---------------------------------------------------------------------------

def tokenize(cmd: str) -> list[str] | None:
    try:
        lex = shlex.shlex(cmd.replace("\n", " ; "), posix=True, punctuation_chars=";&|<>")
        lex.whitespace_split = True
        lex.commenters = ""
        return list(lex)
    except ValueError:
        return None


def strip_prefix(argv: list[str]) -> list[str]:
    """去掉 FOO=bar、sudo/env 之类的前缀，得到真正的命令。"""
    i = 0
    while i < len(argv):
        tok = argv[i]
        if re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", tok):
            i += 1
            continue
        base = os.path.basename(tok)
        if base in WRAPPERS:
            i += 1
            # 跳过包装命令自己的参数，如 sudo -u x、env -i
            while i < len(argv) and argv[i].startswith("-"):
                i += 1
            continue
        break
    return argv[i:]


def words(argv: list[str]) -> list[str]:
    """去掉重定向操作符本身，保留它们的目标（目标也要检查）。"""
    return [t for t in argv if t not in REDIRECT_OPS]


ASSIGNMENT = re.compile(r"^([A-Za-z_][A-Za-z0-9_]*)=(.*)$", re.S)
VARIABLE = re.compile(r"\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))")
# 状态是 (cwd, 本条命令内可静态确定的变量, 上个命令是否成功)。不执行 shell。
ShellState = tuple[str, dict[str, str], bool]


def expand_word(word: str, values: dict[str, str], cwd: str) -> str:
    def replace(m: re.Match) -> str:
        name = m.group(1) or m.group(2)
        return cwd if name == "PWD" else values.get(name, m.group(0))
    return VARIABLE.sub(replace, word)


def expand_argv(argv: list[str], values: dict[str, str], cwd: str) -> list[str]:
    out: list[str] = []
    for word in argv:
        expanded = expand_word(word, values, cwd)
        # -c 的下一项仍是一整段代码，留给递归解析，不能在外层按空格拆散。
        shell_code = bool(out and os.path.basename(out[0]) in SHELLS
                          and re.match(r"^-[A-Za-z]*c[A-Za-z]*$", out[-1]))
        if "$" in word and not shell_code:
            # 保守地包含未加引号展开的字段拆分；引号内的展开可能因此被多拦。
            out.extend(expanded.split() or [""])
        else:
            out.append(expanded)
    return out


def unique_states(states: list[ShellState]) -> list[ShellState]:
    seen = set()
    out = []
    for state in states:
        key = (state[0], tuple(sorted(state[1].items())), state[2])
        if key not in seen:
            seen.add(key)
            out.append(state)
        if len(out) > 64:
            raise Block("shell-state", "闸拦下：命令的条件分支过多，无法可靠判断，请拆成多条命令。")
    return out


def check_simple(seg: list[str], state: ShellState, root: str, depth: int) -> list[ShellState]:
    cwd, original_values, _ = state
    values = dict(original_values)
    i = 0
    while i < len(seg):
        assignment = ASSIGNMENT.match(seg[i])
        if not assignment:
            break
        values[assignment[1]] = expand_word(assignment[2], values, cwd)
        i += 1
    if i == len(seg):
        return [(cwd, values, True)]
    if seg[i] == "export":
        for word in seg[i + 1:]:
            assignment = ASSIGNMENT.match(word)
            if assignment:
                values[assignment[1]] = expand_word(assignment[2], values, cwd)
    if seg[i] == "unset":
        for name in seg[i + 1:]:
            values.pop(name, None)

    # 展开后也要去包装前缀，覆盖 E=env; G=gh; $E $G pr merge 这样的调用。
    argv = strip_prefix(expand_argv(strip_prefix(seg[i:]), values, cwd))
    if not argv:
        return [(cwd, original_values, True)]
    if not argv[0] or "$" in argv[0] or "`" in argv[0]:
        raise Block("shell-variable", "闸拦下：无法静态确定变量展开后的命令，请使用明确的命令名。")
    cmd = os.path.basename(argv[0])
    if cmd in ("git", "gh", "miaoju") and any("$" in a or "`" in a for a in argv[1:]):
        raise Block("shell-variable", "闸拦下：受控命令含无法静态确定的变量参数，请先写明具体值。")

    nested = None
    if cmd in SHELLS:
        for n, arg in enumerate(argv[1:], 1):
            if re.match(r"^-[A-Za-z]*c[A-Za-z]*$", arg) and n + 1 < len(argv):
                check_bash(argv[n + 1], cwd, root, depth + 1, values)
                break
    if cmd == "eval":
        nested = check_bash(" ".join(argv[1:]), cwd, root, depth + 1, values)

    check_secrets(argv, cwd)
    check_miaoju(argv)
    check_gh(argv)
    if cmd == "git":
        check_git(argv, cwd)
    if cmd == "find":
        check_find(argv)
    if cmd == "rm":
        check_rm(argv, cwd, root)
    check_subtree_moves(argv, cwd, root)
    check_protected_in_bash(argv, cwd, root)
    if coordinator_session():
        check_coordinator_bash(argv, cwd, root)
    if nested is not None:
        return nested  # eval 在当前 shell 中修改变量和目录；bash -c 则不传播。
    if cmd in ("export", "unset"):
        return [(cwd, values, True)]
    if cmd == "cd":
        paths = [a for a in argv[1:] if a not in ("--", "-L", "-P")]
        target = paths[0] if paths else os.path.expanduser("~")
        if target == "-":
            target = values.get("OLDPWD", "$")
        if len(paths) > 1 or "$" in target or "`" in target:
            raise Block("shell-cwd", "闸拦下：无法静态确定 cd 后的目录，请使用明确路径。")
        changed = dict(original_values, OLDPWD=cwd)
        # 前面的命令可能创建/删除目录，不能用检查时的文件系统猜 cd 是否会成功。
        return [(resolve(target, cwd), changed, True), (cwd, original_values, False)]
    if cmd in ("true", ":", "false"):
        return [(cwd, original_values, cmd != "false")]
    # 普通命令可能成功也可能失败；前缀赋值/env 不污染后续 shell 的变量。
    return [(cwd, original_values, True), (cwd, original_values, False)]


def check_bash(command: str, cwd: str, root: str, depth: int = 0,
               values: dict[str, str] | None = None) -> list[ShellState]:
    if depth > 3:
        raise Block("nesting", "命令嵌套过深，闸无法判断，按拦下处理。")

    raw_checks(command)

    tokens = tokenize(command)
    if tokens is None:
        raise Block("unparseable", "命令引号不配对，闸无法解析，按拦下处理。请改写成能解析的形式。")

    parts = []
    start = 0
    for n, token in enumerate(tokens):
        if token in SEGMENT_OPS:
            parts.append((tokens[start:n], token))
            start = n + 1
    parts.append((tokens[start:], ";"))
    states: list[ShellState] = [(cwd, dict(values or {}), True)]
    previous = ";"
    for seg, following in parts:
        if not seg:
            continue
        before = states
        after = []
        for state in before:
            if (previous == "&&" and not state[2]) or (previous == "||" and state[2]):
                # 保留旧闸对所有语句的保守检查；未执行的分支不能改变后续状态。
                check_simple(seg, state, root, depth)
                after.append(state)
            else:
                after.extend(check_simple(seg, state, root, depth))
        # 管道与后台命令不可靠地传播 shell 状态；保留原状态以免赋值掩盖危险调用。
        if previous in ("|", "|&", "&") or following in ("|", "|&", "&"):
            after.extend(before)
        states = unique_states(after)
        previous = following
    return states


def raw_checks(command: str) -> None:
    """不依赖解析的兜底匹配，覆盖 $(...)、反引号等。"""
    if re.search(r"\bmiaoju\b[^;&|\n]*\bnode\b(\s+-\S+)*\s+run\b", command):
        raise Block("billing", BILLING_MSG)
    # 真实计费子命令是 `miaoju run <节点ID>`（tvmao-drama cli/internal/app/run.go，#5 调研）；
    # 只放行只读的 `run get` / `run wait`。
    if re.search(r"\bmiaoju\b(\s+-\S+)*\s+run\b(?!\s+(get|wait)\b)", command):
        raise Block("billing", BILLING_MSG)
    # 直接打 sidecar 的付费 HTTP 入口（#5 调研第一节）：带写入意图就拦。
    if re.search(BILLING_HTTP, command) and re.search(HTTP_WRITE, command):
        raise Block("billing", BILLING_MSG)
    if re.search(r"/api/v1/agent/ws\b", command):
        raise Block("billing", BILLING_MSG)
    if re.search(r"\bgh\s+pr\s+merge\b", command):
        raise Block("merge", MERGE_MSG)
    if re.search(r"/pulls/\d+/merge\b|mergePullRequest", command):
        raise Block("merge", MERGE_MSG)


BILLING_HTTP = (
    r"/api/v1/(canvas-tasks(/(desub|upscale|whitemodel))?|canvas-script-tasks|seedance/verify)(?![\w/-])"
    r"|/api/v1/cli/projects/[^/\s]+/nodes/[^/\s]+/runs\b"
)
# 写入意图：curl 参数、method 字段，以及常见脚本 HTTP 客户端（requests/httpx/aiohttp/axios 的
# .post/.put/.patch/.request，urllib 的 urlopen/Request）。
HTTP_WRITE = (
    r"(-X\s*|--request[=\s]+)(POST|PUT|PATCH)\b|\s(-d|--data\S*|--json|-F|--form)\b"
    r"|\bmethod\s*[:=]\s*['\"]?(POST|PUT|PATCH)"
    r"|\.(post|put|patch|request)\s*\(|\burlopen\s*\(|\bRequest\s*\("
)

BILLING_MSG = (
    "闸拦下：`miaoju run` / `miaoju node run` 以及 sidecar 的生成接口一提交就计费且不可取消（关卡 G5）。"
    "验证请用零成本路径（见 tvmao-billing-guard / verify-tvmao 的 feature map）。"
    "确需付费生成，请在回传书里写明理由和数量，等 James 批。"
)
MERGE_MSG = "闸拦下：合并 PR 属于关卡 G4，合并权只在 James。请开 draft PR 并在回传书里说明。"


def check_secrets(argv: list[str], cwd: str) -> None:
    for tok in words(argv[1:]):
        for piece in [tok] + tok.split("=")[1:]:
            if piece and not piece.startswith("-") and is_secret(piece):
                raise Block(
                    "secret",
                    "闸拦下：命令涉及密钥类文件（%s）。agent 不读不写 .env、私钥和凭据。"
                    "需要的配置项请在回传书里列出，由 James 提供。" % piece,
                )


def check_miaoju(argv: list[str]) -> None:
    names = [os.path.basename(t) for t in argv]
    if "miaoju" not in names:
        return
    rest = argv[names.index("miaoju") + 1:]
    positional = [t for t in rest if not t.startswith("-")]
    for i, t in enumerate(positional[:-1]):
        if t == "node" and positional[i + 1] == "run":
            raise Block("billing", BILLING_MSG)
    # `miaoju run <节点ID>` 才是真实计费子命令；全局选项可能带值（--canvas c1），所以不只看第一个位置参数。
    for i, t in enumerate(positional):
        if t == "run" and (i + 1 >= len(positional) or positional[i + 1] not in ("get", "wait")):
            raise Block("billing", BILLING_MSG)


def check_gh(argv: list[str]) -> None:
    if os.path.basename(argv[0]) != "gh":
        return
    pos: list[str] = []
    skip = False
    for t in argv[1:]:
        if skip:
            skip = False
            continue
        if t in ("-R", "--repo"):
            skip = True
            continue
        if t.startswith("-"):
            continue
        pos.append(t)
    if pos[:2] == ["pr", "merge"]:
        raise Block("merge", MERGE_MSG)
    if pos[:1] == ["api"] and any(re.search(r"/pulls/\d+/merge", t) for t in pos):
        raise Block("merge", MERGE_MSG)


def _current_branch(cwd: str) -> str | None:
    try:
        out = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD"],
            capture_output=True, text=True, timeout=5,
        )
        return out.stdout.strip() if out.returncode == 0 else None
    except Exception:
        return None


def _refspec_dst(spec: str) -> str:
    spec = spec.lstrip("+")
    dst = spec.split(":", 1)[1] if ":" in spec else spec
    return re.sub(r"^refs/heads/", "", dst)


def check_git(argv: list[str], cwd: str) -> None:
    i = 1
    git_cwd = cwd
    while i < len(argv) and argv[i].startswith("-"):
        if argv[i] == "-C" and i + 1 < len(argv):
            git_cwd = resolve(argv[i + 1], cwd)
            i += 2
            continue
        if argv[i] in GIT_OPTS_WITH_VALUE:
            i += 2
            continue
        i += 1
    if i >= len(argv):
        return
    subcommand = argv[i]
    args = argv[i + 1:]
    option_args = args[:args.index("--")] if "--" in args else args
    if subcommand == "clean":
        clean_flags = set()
        n = 0
        while n < len(option_args):
            arg = option_args[n]
            if arg == "--exclude":
                n += 2
                continue
            if arg.startswith("--"):
                clean_flags.add(arg)
            elif arg.startswith("-"):
                for k, flag in enumerate(arg[1:], 1):
                    if flag == "e":
                        # -e 的值（包括形似 -n 的排除模式）不是预览选项。
                        if k == len(arg) - 1:
                            n += 1
                        break
                    clean_flags.add(flag)
            n += 1
        preview = bool(clean_flags & {"n", "--dry-run"})
        force_clean = bool(clean_flags & {"f", "--force"})
        if not preview and force_clean and clean_flags & {"d", "x", "X"}:
            raise Block("git-clean", "闸拦下：git clean 强制删除目录或忽略文件。请先用 git clean -n 预览，由 James 决定删除。")
    if subcommand == "reset" and "--hard" in option_args:
        raise Block("git-reset", "闸拦下：git reset --hard 会丢弃工作区修改，请在回传书说明，由 James 处理。")
    if subcommand != "push":
        return
    flags = [a for a in args if a.startswith("-")]
    pos = [a for a in args if not a.startswith("-")]

    force = any(
        f in ("-f", "--force", "--mirror", "--all", "--force-if-includes")
        or f.startswith("--force-with-lease")
        or (re.match(r"^-[a-zA-Z]+$", f) and "f" in f)
        for f in flags
    )
    if force or any(p.startswith("+") for p in pos[1:]):
        raise Block("push", "闸拦下：不允许 force push、--all 或 --mirror。需要改写历史请在回传书里说明，由 James 处理。")

    refspecs = pos[1:]
    if not refspecs:
        branch = _current_branch(git_cwd)
        if branch is None or branch in PROTECTED_BRANCHES or branch == "HEAD":
            raise Block(
                "push",
                "闸拦下：当前分支是 %s，不允许直接 push。请在任务分支上用 `git push -u origin <分支>`。" % branch,
            )
        return
    for spec in refspecs:
        dst = _refspec_dst(spec)
        if dst in PROTECTED_BRANCHES or (dst == "HEAD" and _current_branch(git_cwd) in PROTECTED_BRANCHES):
            raise Block("push", "闸拦下：不允许 push 到 %s（只能走 PR，由 James 合并）。" % dst)


def check_find(argv: list[str]) -> None:
    if "-delete" in argv:
        raise Block("find-delete", "闸拦下：find -delete 会直接删除匹配结果，请先用 find -print 查看。")
    for n, arg in enumerate(argv[:-1]):
        if arg in ("-exec", "-execdir", "-ok", "-okdir"):
            action = strip_prefix(argv[n + 1:])
            if action and ("$" in action[0] or "`" in action[0]):
                raise Block("shell-variable", "闸拦下：无法静态确定 find 要执行的命令，请使用明确的命令名。")
            if action and os.path.basename(action[0]) == "rm":
                raise Block("find-exec-rm", "闸拦下：find 执行 rm 会批量删除匹配结果，请先只输出路径，由 James 决定删除。")


def check_rm(argv: list[str], cwd: str, root: str) -> None:
    flags = [a for a in argv[1:] if a.startswith("-") and a != "--"]
    recursive = any(
        f in ("--recursive",) or (not f.startswith("--") and ("r" in f or "R" in f)) for f in flags
    )
    if not recursive:
        return
    targets = [a for a in argv[1:] if not a.startswith("-")]
    for t in targets:
        if "$" in t or "`" in t:
            raise Block("rm", "闸拦下：`rm -r` 的目标含变量（%s），无法确认在仓库内。请写成明确路径。" % t)
        abs_t = resolve(t, cwd)
        rel = rel_to(abs_t, root)
        if rel is None:
            # 临时目录可以删，但不能是仓库本身的上级目录（macOS 的临时仓库就在 /var/folders 下）
            if is_temp(abs_t) and not root.startswith(abs_t.rstrip("/") + "/"):
                continue
            raise Block("rm", "闸拦下：`rm -r` 的目标 %s 在仓库外。只允许删除仓库内或临时目录里的文件。" % t)
        if rel == "" or rel == ".git" or rel.startswith(".git/"):
            raise Block("rm", "闸拦下：不允许 `rm -r` 仓库根目录或 .git。")
        hit = contains_protected(rel)
        if hit:
            raise Block("protected", "闸拦下：删除 %s 会连带删掉受保护路径 %s（红线），只有 James 能改。" % (t, hit))


GIT_OPTS_WITH_VALUE = {"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--super-prefix", "--config-env"}


def git_positionals(argv: list[str]) -> list[str]:
    """git 的位置参数（子命令在前），正确跳过 -C <路径>、-c <k=v> 这类带值的全局选项。"""
    out: list[str] = []
    i = 1
    in_global = True
    while i < len(argv):
        a = argv[i]
        if in_global and a in GIT_OPTS_WITH_VALUE:
            i += 2
            continue
        if a.startswith("-"):
            i += 1
            continue
        in_global = False
        out.append(a)
        i += 1
    return out


def check_subtree_moves(argv: list[str], cwd: str, root: str) -> None:
    """mv / git rm / git mv 移走受保护路径的上级目录，等于移走受保护路径。"""
    cmd = os.path.basename(argv[0])
    if cmd == "mv":
        srcs = [a for a in argv[1:] if not a.startswith("-")][:-1]
    elif cmd == "git":
        sub = git_positionals(argv)
        if not sub or sub[0] not in ("rm", "mv"):
            return
        srcs = sub[1:-1] if sub[0] == "mv" else sub[1:]
    else:
        return
    for t in srcs:
        hit = contains_protected(rel_to(resolve(t, cwd), root))
        if hit:
            raise Block("protected", "闸拦下：移动或删除 %s 会连带移走受保护路径 %s（红线），只有 James 能改。" % (t, hit))


def coordinator_session() -> bool:
    return os.environ.get("RATCHET_ROLE") == "coordinator" and not _CURRENT.get("agent_id")


# 会写文件的命令：(命令, 写入目标的取法)。"all" = 所有位置参数，"last" = 最后一个位置参数
COORD_WRITE_CMDS = {
    "rm": "all", "touch": "all", "mkdir": "all", "rmdir": "all", "truncate": "all",
    "chmod": "all", "chown": "all", "tee": "all", "sed": "all", "perl": "all",
    "cp": "last", "mv": "all", "ln": "last", "install": "last", "rsync": "last",
}
COORD_DENY_CMDS = {"python", "python3", "node", "ruby", "deno", "bun", "patch", "dd"}
COORD_DENY_GIT = {"apply", "checkout", "restore", "reset", "switch", "merge", "rebase", "cherry-pick", "revert", "stash", "pull", "am", "clean"}


def _coord_target_ok(path: str, cwd: str, root: str) -> bool:
    if path in ("/dev/null", "/dev/stdout", "/dev/stderr"):
        return True
    abs_p = resolve(path, cwd)
    rel = rel_to(abs_p, root)
    if rel is None:
        return is_temp(abs_p)
    r = worktree_relative(rel)
    return r == ".ratchet" or r.startswith(".ratchet/")


def check_coordinator_bash(argv: list[str], cwd: str, root: str) -> None:
    """主轴只能写 .ratchet/：Bash 里的重定向和写文件命令也受同一边界约束。"""
    msg = ("闸拦下：主轴（coordinator）只管项目、不写代码，Bash 也只能写 .ratchet/ 下的文件（%s）。"
           "要改产品代码，请写任务书派给 worker。")
    for i, t in enumerate(argv):
        if t in (">", ">>", ">|", "&>", "&>>") and i + 1 < len(argv) and not _coord_target_ok(argv[i + 1], cwd, root):
            raise Block("coordinator", msg % argv[i + 1])
    cmd = os.path.basename(argv[0])
    plain = [a for a in words(argv[1:])]
    if cmd in COORD_DENY_CMDS:
        # 只读地跑本仓库的棘轮脚本是允许的（预检、检查回传书）
        if cmd.startswith("python") and plain and plain[0].startswith("scripts/ratchet/") and "/hooks/" not in plain[0]:
            return
        raise Block("coordinator", msg % ("不允许用 %s 直接执行代码" % cmd))
    if cmd == "git":
        sub = git_positionals(argv)
        if sub and sub[0] in COORD_DENY_GIT:
            raise Block("coordinator", msg % ("git " + sub[0]))
        return
    mode = COORD_WRITE_CMDS.get(cmd)
    if not mode:
        return
    if cmd in ("sed", "perl") and not any(a.startswith("-i") or a == "--in-place" for a in plain):
        return
    pos = [a for a in plain if not a.startswith("-")]
    if cmd in ("sed", "perl") and pos:
        pos = pos[1:]  # 第一个位置参数是脚本
    targets = pos[-1:] if mode == "last" else pos
    for t in targets:
        if not _coord_target_ok(t, cwd, root):
            raise Block("coordinator", msg % t)


def check_protected_in_bash(argv: list[str], cwd: str, root: str) -> None:
    cmd = os.path.basename(argv[0])
    hit = None
    for tok in words(argv[1:]):
        if tok.startswith("-") and "=" not in tok:
            continue
        cand = tok.split("=", 1)[1] if tok.startswith("-") else tok
        p = is_protected(rel_to(resolve(cand, cwd), root))
        if not p:
            # 兜底：python -c "open('.ratchet/policy/x','w')" 这类把路径藏在字符串里的写法
            p = next((x for x in PROTECTED_PATHS if x.rstrip("/") in tok), None)
        if p:
            hit = p
            break
    if not hit:
        return
    if cmd in READ_ONLY_CMDS and not any(t in (">", ">>", ">|") for t in argv):
        return
    if cmd == "git":
        sub = git_positionals(argv)
        if sub and sub[0] in READ_ONLY_GIT:
            return
    if cmd == "python3" and len(argv) > 1 and argv[1].startswith("scripts/ratchet/") and "hooks" not in argv[1]:
        return
    raise Block(
        "protected",
        "闸拦下：%s 是受保护路径（红线），只有 James 能改。"
        "如果你认为它需要改，在回传书的「未解决风险」里写出建议，不要自己动手。" % hit,
    )


# ---------------------------------------------------------------------------
# 文件类工具
# ---------------------------------------------------------------------------

def check_file_write(path: str, data: dict, root: str) -> None:
    cwd = _real(data.get("cwd") or root)
    abs_p = resolve(path, cwd)
    if is_secret(abs_p):
        raise Block("secret", "闸拦下：不允许写密钥类文件 %s。" % path)
    rel = rel_to(abs_p, root)
    p = is_protected(rel)
    if p:
        raise Block(
            "protected",
            "闸拦下：%s 是受保护路径（红线），只有 James 能改。"
            "如需改动，在回传书里写建议，不要自己动手。" % p,
        )
    if coordinator_session():
        # 主轴只管项目，不写代码：只能写 .ratchet/ 和临时目录
        if rel is not None:
            r = worktree_relative(rel)
            if r == ".ratchet" or r.startswith(".ratchet/"):
                return
            if rel.startswith(".claude/worktrees/"):
                # worktree 里的写入视为 worker 的写入。Claude Code 若在 hook 输入里带 agent_id，
                # 上面已经放行；不带时只能按路径判断（已知缺口，见 ratchet/README.md）。
                return
        elif is_temp(abs_p):
            return
        raise Block(
            "coordinator",
            "闸拦下：主轴（coordinator）只管项目、不写代码，只能写 .ratchet/ 下的文件。"
            "要改 %s，请写任务书派给 worker。" % path,
        )


def check_file_read(path: str, data: dict, root: str) -> None:
    cwd = _real(data.get("cwd") or root)
    if path and is_secret(resolve(path, cwd)):
        raise Block("secret", "闸拦下：不允许读取密钥类文件 %s。需要的配置项请让 James 提供。" % path)


# ---------------------------------------------------------------------------
# 入口
# ---------------------------------------------------------------------------

def log_event(root: str, data: dict, rule: str, message: str) -> None:
    try:
        state = os.path.join(root, ".ratchet", "state")
        os.makedirs(state, exist_ok=True)
        ev = {
            "ts": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"),
            "type": "pawl_block",
            "rule": rule,
            "tool": data.get("tool_name"),
            "agent_id": data.get("agent_id"),
            "session_id": data.get("session_id"),
            "input": data.get("tool_input"),
            "message": message,
        }
        with open(os.path.join(state, "events.jsonl"), "a", encoding="utf-8") as f:
            f.write(json.dumps(ev, ensure_ascii=False) + "\n")
    except Exception:
        pass


def evaluate(data: dict) -> None:
    """检查一次工具调用；违规时抛 Block。"""
    root = project_root(data)
    _CURRENT.clear()
    _CURRENT.update(data)
    tool = data.get("tool_name") or ""
    ti = data.get("tool_input") or {}
    cwd = _real(data.get("cwd") or root)

    if tool == "Bash":
        check_bash(ti.get("command") or "", cwd, root)
    elif tool in ("Edit", "Write", "MultiEdit"):
        check_file_write(ti.get("file_path") or "", data, root)
    elif tool == "NotebookEdit":
        check_file_write(ti.get("notebook_path") or "", data, root)
    elif tool == "Read":
        check_file_read(ti.get("file_path") or "", data, root)
    elif tool == "Grep":
        check_file_read(ti.get("path") or "", data, root)


def main() -> int:
    data: dict = {}
    try:
        data = json.load(sys.stdin)
        evaluate(data)
        return 0
    except Block as b:
        log_event(project_root(data), data, b.rule, b.message)
        sys.stderr.write(b.message + "\n")
        return 2
    except Exception as e:  # fail-closed
        sys.stderr.write("闸内部出错，按拦下处理：%r\n" % (e,))
        return 2


if __name__ == "__main__":
    sys.exit(main())
