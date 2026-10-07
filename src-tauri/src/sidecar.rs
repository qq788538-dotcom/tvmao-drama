//! sidecar 进程层：拉起进程、stdin 传 token、等 "READY <port>"、转发并保留 stderr 尾部、
//! 通知进程退出、优雅关闭（先写 SHUTDOWN 并关 stdin，等待，再强杀）。
//! 不依赖 Tauri，单测直接用 /bin/sh 脚本扮演 sidecar。
//!
//! 启动协议见 server/cmd/sidecar/main.go 顶部注释：stdin 第一行是 token；
//! 就绪后 stdout 打印 "READY <port>"；stdin EOF 表示壳要求退出。

use std::collections::VecDeque;
use std::path::PathBuf;
use std::process::Stdio;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use tokio::io::{AsyncBufRead, AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::process::{Child, ChildStderr, ChildStdin, Command};
use tokio::sync::{oneshot, watch};
use tokio::task::JoinHandle;

/// sidecar 发现数据目录正被另一个 sidecar 使用时的退出码（Go `exitDataDirBusy`，I4）。
pub const DATA_DIR_BUSY_EXIT: i32 = 75;

/// 退出码为 DATA_DIR_BUSY_EXIT 时给用户看的原因（失败页上有“重试”按钮）。
pub const DATA_DIR_BUSY_MSG: &str =
    "上一个窗口仍在关闭，数据目录暂时被它占用。请稍后点“重试”。";

/// 失败原因里保留的 stderr 行数。
pub const STDERR_TAIL_LINES: usize = 20;

/// 只保留 stderr 的最后若干行（空行跳过）。
#[derive(Debug)]
pub struct StderrTail {
    lines: VecDeque<String>,
    cap: usize,
}

impl StderrTail {
    pub fn new(cap: usize) -> Self {
        Self { lines: VecDeque::with_capacity(cap), cap }
    }

    pub fn push(&mut self, line: &str) {
        let line = line.trim_end();
        if line.trim().is_empty() {
            return;
        }
        if self.lines.len() == self.cap {
            self.lines.pop_front();
        }
        self.lines.push_back(line.to_string());
    }

    pub fn joined(&self) -> String {
        self.lines.iter().cloned().collect::<Vec<_>>().join("\n")
    }
}

/// 解析 sidecar 的就绪行 "READY <port>"。
pub fn parse_ready(line: &str) -> Option<u16> {
    line.trim().strip_prefix("READY ")?.trim().parse().ok()
}

#[derive(Debug, Clone)]
pub struct LaunchSpec {
    pub program: PathBuf,
    pub args: Vec<String>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct ExitInfo {
    /// 被信号结束（含强杀）时为 None。
    pub code: Option<i32>,
    pub stderr_tail: String,
}

pub struct Launched {
    pub port: u16,
    pub handle: ProcessHandle,
}

/// 运行中的 sidecar。drop 时强杀进程（等价于 kill_on_drop）。
pub struct ProcessHandle {
    stdin: Option<ChildStdin>,
    kill: Option<oneshot::Sender<()>>,
    exited: watch::Receiver<Option<ExitInfo>>,
}

impl ProcessHandle {
    /// 退出通知：值从 None 变成 Some 表示进程已退出。
    pub fn exited(&self) -> watch::Receiver<Option<ExitInfo>> {
        self.exited.clone()
    }

    async fn wait_exit_for(&self, d: Duration) -> Option<ExitInfo> {
        let mut rx = self.exited.clone();
        tokio::time::timeout(d, async move {
            rx.wait_for(|v| v.is_some()).await.ok().and_then(|v| (*v).clone())
        })
        .await
        .ok()
        .flatten()
    }

    async fn kill_now(&mut self) -> ExitInfo {
        self.stdin.take();
        if let Some(k) = self.kill.take() {
            let _ = k.send(());
        }
        self.wait_exit_for(Duration::from_secs(5)).await.unwrap_or(ExitInfo {
            code: None,
            stderr_tail: String::new(),
        })
    }

    /// 优雅关闭：先写一行 SHUTDOWN 再关 stdin（sidecar 读到 SHUTDOWN 或 EOF 都会收尾退出），
    /// 最多等 grace，仍未退出再强杀。
    pub async fn shutdown(mut self, grace: Duration) -> ExitInfo {
        if let Some(mut stdin) = self.stdin.take() {
            // 尽力而为：进程已退出时写入会失败（EPIPE），忽略即可；关 stdin 本身也是退出信号。
            let _ = tokio::time::timeout(Duration::from_millis(500), async {
                let _ = stdin.write_all(b"SHUTDOWN\n").await;
                let _ = stdin.flush().await;
            })
            .await;
        }
        if let Some(info) = self.wait_exit_for(grace).await {
            return info;
        }
        eprintln!("[sidecar] {:.1} 秒内未退出，强制结束", grace.as_secs_f32());
        self.kill_now().await
    }
}

/// 未就绪就退出时的原因：数据目录被占用给固定提示，其他情况带 stderr 尾部。
fn early_exit_reason(msg: String, info: &ExitInfo) -> String {
    if info.code == Some(DATA_DIR_BUSY_EXIT) {
        return DATA_DIR_BUSY_MSG.to_string();
    }
    with_tail(msg, &info.stderr_tail)
}

fn with_tail(msg: String, tail: &str) -> String {
    if tail.is_empty() {
        msg
    } else {
        format!("{msg}：\n{tail}")
    }
}

/// 按字节读一行再做有损 UTF-8 转换：非 UTF-8 输出不能让读取中断（否则管道写满会卡住 sidecar）。
async fn next_line_lossy<R: AsyncBufRead + Unpin>(r: &mut R, buf: &mut Vec<u8>) -> Option<String> {
    buf.clear();
    match r.read_until(b'\n', buf).await {
        Ok(0) | Err(_) => None,
        Ok(_) => Some(String::from_utf8_lossy(buf).into_owned()),
    }
}

async fn pump_stderr(stderr: ChildStderr, tail: Arc<Mutex<StderrTail>>) {
    let mut r = BufReader::new(stderr);
    let mut buf = Vec::new();
    while let Some(line) = next_line_lossy(&mut r, &mut buf).await {
        eprintln!("[sidecar] {}", line.trim_end());
        tail.lock().unwrap().push(&line);
    }
}

async fn wait_exit(
    mut child: Child,
    kill_rx: oneshot::Receiver<()>,
    stderr_task: JoinHandle<()>,
    tail: Arc<Mutex<StderrTail>>,
    tx: watch::Sender<Option<ExitInfo>>,
) {
    let status = tokio::select! {
        s = child.wait() => s,
        // 收到强杀请求，或 ProcessHandle 被 drop（发送端关闭）
        _ = kill_rx => {
            let _ = child.start_kill();
            child.wait().await
        }
    };
    // 进程退出后 stderr 管道关闭；等读完再取尾部（孙进程占着管道时最多等 1 秒）
    let _ = tokio::time::timeout(Duration::from_secs(1), stderr_task).await;
    let code = status.ok().and_then(|s| s.code());
    let stderr_tail = tail.lock().unwrap().joined();
    let _ = tx.send(Some(ExitInfo { code, stderr_tail }));
}

/// 拉起 sidecar 并等到 READY。任何一步失败都返回可读的中文原因（带 stderr 最后几行）。
pub async fn launch(spec: &LaunchSpec, token: &str, cli_token: &str, ready_timeout: Duration) -> Result<Launched, String> {
    let mut cmd = Command::new(&spec.program);
    cmd.args(&spec.args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    #[cfg(windows)]
    cmd.creation_flags(0x0800_0000); // CREATE_NO_WINDOW：不弹控制台窗口
    let mut child = cmd
        .spawn()
        .map_err(|e| format!("无法启动 sidecar（{}）：{e}", spec.program.display()))?;

    let mut stdin = child.stdin.take().expect("stdin 已设为 piped");
    let stdout = child.stdout.take().expect("stdout 已设为 piped");
    let stderr = child.stderr.take().expect("stderr 已设为 piped");

    let tail = Arc::new(Mutex::new(StderrTail::new(STDERR_TAIL_LINES)));
    let stderr_task = tokio::spawn(pump_stderr(stderr, tail.clone()));
    let (exited_tx, exited_rx) = watch::channel(None);
    let (kill_tx, kill_rx) = oneshot::channel();
    tokio::spawn(wait_exit(child, kill_rx, stderr_task, tail, exited_tx));
    let mut handle = ProcessHandle { stdin: None, kill: Some(kill_tx), exited: exited_rx };

    // token 只走 stdin（SEC3）：参数和环境变量同机其他进程读得到。
    let wrote = async {
        stdin.write_all(format!("{token}\n{cli_token}\n").as_bytes()).await?;
        stdin.flush().await?;
        Ok::<(), std::io::Error>(())
    }
    .await;
    if let Err(e) = wrote {
        let info = handle.kill_now().await;
        return Err(early_exit_reason(format!("无法向 sidecar 传递 token：{e}"), &info));
    }
    handle.stdin = Some(stdin);

    let mut out = BufReader::new(stdout);
    let ready = tokio::time::timeout(ready_timeout, async {
        let mut buf = Vec::new();
        while let Some(line) = next_line_lossy(&mut out, &mut buf).await {
            if let Some(port) = parse_ready(&line) {
                return Some(port);
            }
        }
        None
    })
    .await;

    match ready {
        Ok(Some(port)) => {
            // 之后继续读走 stdout，避免管道写满阻塞 sidecar
            tokio::spawn(async move {
                let mut buf = Vec::new();
                while next_line_lossy(&mut out, &mut buf).await.is_some() {}
            });
            Ok(Launched { port, handle })
        }
        Ok(None) => {
            let info = handle.kill_now().await;
            Err(early_exit_reason("sidecar 未就绪即退出".to_string(), &info))
        }
        Err(_) => {
            let info = handle.kill_now().await;
            Err(with_tail(
                format!("sidecar 启动超时（{} 秒内未就绪）", ready_timeout.as_secs()),
                &info.stderr_tail,
            ))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_ready_accepts_port_line() {
        assert_eq!(parse_ready("READY 54321"), Some(54321));
        assert_eq!(parse_ready("READY 54321\r\n"), Some(54321));
    }

    #[test]
    fn parse_ready_ignores_other_lines() {
        assert_eq!(parse_ready("listening..."), None);
        assert_eq!(parse_ready("READY"), None);
        assert_eq!(parse_ready("READY abc"), None);
        assert_eq!(parse_ready("READY 99999"), None);
    }

    #[test]
    fn stderr_tail_is_bounded() {
        let mut t = StderrTail::new(3);
        for i in 0..10 {
            t.push(&format!("line{i}\n"));
        }
        t.push("   ");
        assert_eq!(t.joined(), "line7\nline8\nline9");
    }
}

#[cfg(all(test, unix))]
mod process_tests {
    use super::*;
    use std::time::Instant;

    const TOKEN: &str = "0123456789abcdef0123456789abcdef";

    fn sh(script: &str) -> LaunchSpec {
        LaunchSpec {
            program: "/bin/sh".into(),
            args: vec!["-c".into(), script.into()],
        }
    }

    #[tokio::test]
    async fn launch_writes_cli_token_as_second_line() {
        let spec = sh(r#"read t; read c; [ "$t" = "web-tok" ] || exit 3; [ "$c" = "cli-tok" ] || exit 4; echo "READY 4100"; cat >/dev/null"#);
        let l = launch(&spec, "web-tok", "cli-tok", Duration::from_secs(5)).await.unwrap();
        assert_eq!(l.port, 4100);
    }

    #[tokio::test]
    async fn launch_returns_port_and_token_reaches_stdin() {
        let spec = sh(r#"read t; [ "$t" = "0123456789abcdef0123456789abcdef" ] || exit 3; echo "READY 5555"; cat >/dev/null"#);
        let l = launch(&spec, TOKEN, "cli-tok", Duration::from_secs(5)).await.unwrap();
        assert_eq!(l.port, 5555);
        let start = Instant::now();
        let info = l.handle.shutdown(Duration::from_secs(3)).await;
        assert_eq!(info.code, Some(0));
        assert!(start.elapsed() < Duration::from_secs(2), "关 stdin 后应自行退出");
    }

    #[tokio::test]
    async fn shutdown_sends_shutdown_line_before_closing_stdin() {
        // 只有读到 SHUTDOWN 行才以 0 退出；读到 EOF 而没有 SHUTDOWN 则以 5 退出。
        let spec = sh(r#"read t; echo "READY 7"; while read l; do [ "$l" = "SHUTDOWN" ] && exit 0; done; exit 5"#);
        let l = launch(&spec, TOKEN, "cli-tok", Duration::from_secs(5)).await.unwrap();
        let info = l.handle.shutdown(Duration::from_secs(3)).await;
        assert_eq!(info.code, Some(0), "壳退出时应先写 SHUTDOWN 行");
    }

    #[tokio::test]
    async fn args_with_spaces_and_cjk_pass_through() {
        let spec = LaunchSpec {
            program: "/bin/sh".into(),
            args: vec![
                "-c".into(),
                r#"read t; [ "$1" = "/Users/张 三/数据 目录" ] || exit 4; echo "READY 1"; cat >/dev/null"#.into(),
                "sh".into(),
                "/Users/张 三/数据 目录".into(),
            ],
        };
        let l = launch(&spec, TOKEN, "cli-tok", Duration::from_secs(5)).await.unwrap();
        assert_eq!(l.port, 1);
        l.handle.shutdown(Duration::from_secs(2)).await;
    }

    /// I4：数据目录被上一个 sidecar 占着（Go 退出码 75）→ 明确提示“上一个窗口仍在关闭”，可重试。
    #[tokio::test]
    async fn data_dir_busy_exit_code_reports_retry_hint() {
        let spec = sh(r#"read t; echo "2026/01/01 sidecar 启动失败: busy" >&2; exit 75"#);
        let err = launch(&spec, TOKEN, "cli-tok", Duration::from_secs(5)).await.err().unwrap();
        assert!(err.contains("上一个窗口仍在关闭") && err.contains("重试"), "{err}");
    }

    #[tokio::test]
    async fn exit_before_ready_reports_multiline_stderr() {
        let spec = sh(r#"read t; echo first >&2; echo second >&2; exit 1"#);
        let err = launch(&spec, TOKEN, "cli-tok", Duration::from_secs(5)).await.err().unwrap();
        assert!(err.contains("未就绪即退出"), "{err}");
        assert!(err.contains("first") && err.contains("second"), "{err}");
    }

    #[tokio::test]
    async fn exit_before_ready_without_stderr_has_plain_message() {
        let spec = sh("read t; exit 1");
        let err = launch(&spec, TOKEN, "cli-tok", Duration::from_secs(5)).await.err().unwrap();
        assert_eq!(err, "sidecar 未就绪即退出");
    }

    #[tokio::test]
    async fn silent_sidecar_times_out_and_is_killed() {
        let spec = sh("read t; exec sleep 30");
        let start = Instant::now();
        let err = launch(&spec, TOKEN, "cli-tok", Duration::from_millis(200)).await.err().unwrap();
        assert!(err.contains("启动超时"), "{err}");
        assert!(start.elapsed() < Duration::from_secs(3));
    }

    #[tokio::test]
    async fn shutdown_escalates_to_kill() {
        let spec = sh(r#"read t; echo "READY 1"; trap '' TERM; exec sleep 30"#);
        let l = launch(&spec, TOKEN, "cli-tok", Duration::from_secs(5)).await.unwrap();
        let start = Instant::now();
        let info = l.handle.shutdown(Duration::from_millis(300)).await;
        assert_eq!(info.code, None, "被强杀，没有退出码");
        assert!(start.elapsed() < Duration::from_secs(3));
    }

    #[tokio::test]
    async fn crash_after_ready_is_reported_with_tail() {
        let spec = sh(r#"read t; echo "READY 7"; echo fatal-1 >&2; echo fatal-2 >&2; exit 7"#);
        let l = launch(&spec, TOKEN, "cli-tok", Duration::from_secs(5)).await.unwrap();
        let mut rx = l.handle.exited();
        let info = tokio::time::timeout(Duration::from_secs(3), rx.wait_for(|v| v.is_some()))
            .await
            .unwrap()
            .unwrap()
            .clone()
            .unwrap();
        assert_eq!(info.code, Some(7));
        assert!(info.stderr_tail.contains("fatal-1") && info.stderr_tail.contains("fatal-2"));
    }

    #[tokio::test]
    async fn dropping_handle_kills_process() {
        let spec = sh(r#"read t; echo "READY 1"; exec sleep 30"#);
        let l = launch(&spec, TOKEN, "cli-tok", Duration::from_secs(5)).await.unwrap();
        let mut rx = l.handle.exited();
        drop(l);
        tokio::time::timeout(Duration::from_secs(3), rx.wait_for(|v| v.is_some()))
            .await
            .expect("drop 后进程应被结束")
            .unwrap();
    }

    #[tokio::test]
    async fn non_utf8_stderr_does_not_block() {
        // 非 UTF-8 字节 + 大量日志：读取不能中断，否则 sidecar 写满管道会卡住
        let spec = sh(r#"read t; printf '\377\376\n' >&2; i=0; while [ $i -lt 2000 ]; do echo "log line $i" >&2; i=$((i+1)); done; echo "READY 9"; echo last >&2; exit 2"#);
        let l = launch(&spec, TOKEN, "cli-tok", Duration::from_secs(10)).await.unwrap();
        assert_eq!(l.port, 9);
        let mut rx = l.handle.exited();
        let info = tokio::time::timeout(Duration::from_secs(5), rx.wait_for(|v| v.is_some()))
            .await
            .unwrap()
            .unwrap()
            .clone()
            .unwrap();
        assert_eq!(info.stderr_tail.lines().count(), STDERR_TAIL_LINES);
        assert!(info.stderr_tail.ends_with("last"));
    }
}
