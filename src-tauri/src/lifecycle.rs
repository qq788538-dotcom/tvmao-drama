//! sidecar 生命周期状态机：启动中 → 就绪 / 失败。
//! - 启动在后台进行，不阻塞主线程；前端 server_info 会等到“不再是启动中”。
//! - 就绪后 sidecar 崩溃：自动重启一次；距上次自动重启不到 crash_window 又崩 → 失败，由用户点“重试”。
//! - restart()：前端“重试”或 P4 切换数据目录后调用；清零自动重启计数。
//! - shutdown()：应用退出时调用，之后不再拉起。先置位 closing（不拿锁），正在进行的启动随即被取消，
//!   所以退出不会卡在等 READY 上。
//! 不依赖 Tauri：状态变化通过 Emitter 回调发出（lib.rs 里转成 `sidecar-state` 事件）。

use std::sync::Arc;
use std::time::{Duration, Instant};

use serde::Serialize;
use tokio::sync::{watch, Mutex};

use crate::sidecar::{launch, ExitInfo, LaunchSpec, ProcessHandle};

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct ServerInfo {
    pub port: u16,
    pub token: String,
}

/// 发给前端的状态（事件载荷和 sidecar_status 命令返回值）。故意不含 token。
#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(tag = "status", rename_all = "lowercase")]
pub enum SidecarStatus {
    Starting,
    /// 启动前正在迁移数据目录（P4）。不是状态机的阶段：由 lib.rs 在迁移期间直接发出 / 返回。
    /// waiting：迁移子命令正在等上一次迁移的锁（I3）。
    Migrating { waiting: bool },
    Ready,
    Failed { reason: String },
}

#[derive(Clone, Debug, PartialEq)]
enum Phase {
    Starting,
    Ready(ServerInfo),
    Failed(String),
}

impl Phase {
    fn status(&self) -> SidecarStatus {
        match self {
            Phase::Starting => SidecarStatus::Starting,
            Phase::Ready(_) => SidecarStatus::Ready,
            Phase::Failed(r) => SidecarStatus::Failed { reason: r.clone() },
        }
    }
}

pub type SpecFactory = Arc<dyn Fn() -> Result<LaunchSpec, String> + Send + Sync>;
pub type Emitter = Arc<dyn Fn(SidecarStatus) + Send + Sync>;

#[derive(Clone, Copy, Debug)]
pub struct Timeouts {
    /// 等 READY 的最长时间。
    pub ready: Duration,
    /// 关 stdin 后等 sidecar 自行退出的时间，超时强杀。
    pub grace: Duration,
    /// 两次自动重启的最小间隔，间隔内再崩就放弃。
    pub crash_window: Duration,
}

impl Default for Timeouts {
    fn default() -> Self {
        Self {
            ready: Duration::from_secs(20),
            grace: Duration::from_secs(3),
            crash_window: Duration::from_secs(60),
        }
    }
}

#[derive(Debug, PartialEq)]
pub enum CrashAction {
    Restart,
    GiveUp,
}

/// 就绪后崩溃的处理策略：距上次自动重启超过 window（或从未自动重启）就重启，否则放弃。
#[derive(Debug, Default)]
pub struct RestartPolicy {
    last_auto_restart: Option<Instant>,
}

impl RestartPolicy {
    pub fn on_crash(&mut self, now: Instant, window: Duration) -> CrashAction {
        match self.last_auto_restart {
            Some(t) if now.duration_since(t) < window => CrashAction::GiveUp,
            _ => {
                self.last_auto_restart = Some(now);
                CrashAction::Restart
            }
        }
    }

    /// 用户手动重试时清零。
    pub fn reset(&mut self) {
        self.last_auto_restart = None;
    }
}

/// 64 位十六进制随机 token（两个 UUIDv4，来自系统随机源）。
pub fn new_token() -> String {
    format!("{}{}", uuid::Uuid::new_v4().simple(), uuid::Uuid::new_v4().simple())
}

fn crash_reason(info: &ExitInfo) -> String {
    let code = match info.code {
        Some(c) => format!("退出码 {c}"),
        None => "被系统结束".to_string(),
    };
    if info.stderr_tail.is_empty() {
        format!("本地服务意外退出（{code}）")
    } else {
        format!("本地服务意外退出（{code}）：\n{}", info.stderr_tail)
    }
}

struct Inner {
    handle: Option<ProcessHandle>,
    /// 每次拉起 +1；退出通知带着自己的代号，不匹配说明已被新进程取代，忽略。
    generation: u64,
    policy: RestartPolicy,
}

pub struct SidecarManager {
    phase: watch::Sender<Phase>,
    inner: Mutex<Inner>,
    /// 应用正在退出。独立于 inner 锁：shutdown 要能在启动进行中（持锁）时立即置位并取消启动。
    closing: watch::Sender<bool>,
    make_spec: SpecFactory,
    emit: Emitter,
    timeouts: Timeouts,
}

impl SidecarManager {
    pub fn new(make_spec: SpecFactory, emit: Emitter, timeouts: Timeouts) -> Arc<Self> {
        Arc::new(Self {
            phase: watch::Sender::new(Phase::Starting),
            inner: Mutex::new(Inner {
                handle: None,
                generation: 0,
                policy: RestartPolicy::default(),
            }),
            closing: watch::Sender::new(false),
            make_spec,
            emit,
            timeouts,
        })
    }

    pub fn status(&self) -> SidecarStatus {
        self.phase.borrow().status()
    }

    fn set_phase(&self, p: Phase) {
        let s = p.status();
        self.phase.send_replace(p);
        (self.emit)(s);
    }

    /// 等到不再是“启动中”，返回端口和 token，或失败原因。
    pub async fn wait_info(&self) -> Result<ServerInfo, String> {
        let mut rx = self.phase.subscribe();
        let phase = rx
            .wait_for(|p| *p != Phase::Starting)
            .await
            .map_err(|_| "本地服务已关闭".to_string())?
            .clone();
        match phase {
            Phase::Ready(info) => Ok(info),
            Phase::Failed(reason) => Err(reason),
            Phase::Starting => unreachable!("wait_for 已排除"),
        }
    }

    /// 首次启动。lib.rs 在 setup 里用 tauri::async_runtime::spawn 调用，不阻塞主线程。
    pub async fn start(self: &Arc<Self>) -> Result<ServerInfo, String> {
        let mut inner = self.inner.lock().await;
        self.start_locked(&mut inner).await
    }

    /// 前端“重试” / 切换数据目录后调用：清零自动重启计数，关掉旧进程再拉起。
    pub async fn restart(self: &Arc<Self>) -> Result<ServerInfo, String> {
        let mut inner = self.inner.lock().await;
        inner.policy.reset();
        self.start_locked(&mut inner).await
    }

    /// 应用退出：不再自动重启，优雅关闭当前进程。
    pub async fn shutdown(&self) {
        // 先置位（不拿锁）：持锁中的 start_locked 会看到并取消正在进行的启动，随后释放锁。
        self.closing.send_replace(true);
        let mut inner = self.inner.lock().await;
        if let Some(h) = inner.handle.take() {
            h.shutdown(self.timeouts.grace).await;
        }
    }

    fn is_closing(&self) -> bool {
        *self.closing.borrow()
    }

    async fn start_locked(self: &Arc<Self>, inner: &mut Inner) -> Result<ServerInfo, String> {
        const CLOSING: &str = "应用正在退出";
        if self.is_closing() {
            return Err(CLOSING.to_string());
        }
        if let Some(old) = inner.handle.take() {
            old.shutdown(self.timeouts.grace).await;
        }
        inner.generation += 1;
        let generation = inner.generation;
        self.set_phase(Phase::Starting);

        let launching = async {
            let spec = (self.make_spec)()?;
            let token = new_token();
            let l = launch(&spec, &token, self.timeouts.ready).await?;
            Ok::<_, String>((ServerInfo { port: l.port, token }, l.handle))
        };
        // 退出时取消启动：丢弃 launch 的 future 会一并丢弃子进程句柄，进程被强杀（还没就绪，没有可收尾的数据）。
        let mut closing = self.closing.subscribe();
        let launched = tokio::select! {
            r = launching => r,
            _ = closing.wait_for(|c| *c) => {
                // 让还在等 server_info 的调用结束，不要一直挂着
                self.set_phase(Phase::Failed(CLOSING.to_string()));
                return Err(CLOSING.to_string());
            }
        };
        // 就绪与退出同时发生：交还给 shutdown 去优雅关闭。

        match launched {
            Ok((info, handle)) => {
                self.watch_crash(generation, handle.exited());
                inner.handle = Some(handle);
                self.set_phase(Phase::Ready(info.clone()));
                Ok(info)
            }
            Err(reason) => {
                let reason = format!("本地服务启动失败：{reason}");
                self.set_phase(Phase::Failed(reason.clone()));
                Err(reason)
            }
        }
    }

    fn watch_crash(self: &Arc<Self>, generation: u64, mut exited: watch::Receiver<Option<ExitInfo>>) {
        let me = Arc::clone(self);
        tokio::spawn(async move {
            let info = match exited.wait_for(|v| v.is_some()).await {
                Ok(v) => (*v).clone().expect("wait_for 已保证 Some"),
                Err(_) => return,
            };
            me.on_exit(generation, info).await;
        });
    }

    async fn on_exit(self: &Arc<Self>, generation: u64, info: ExitInfo) {
        let mut inner = self.inner.lock().await;
        // 主动关闭（退出 / 重启）或已被新进程取代：不算崩溃
        if self.is_closing() || inner.generation != generation {
            return;
        }
        inner.handle = None;
        let reason = crash_reason(&info);
        eprintln!("[sidecar] {reason}");
        match inner.policy.on_crash(Instant::now(), self.timeouts.crash_window) {
            CrashAction::Restart => {
                let _ = self.start_locked(&mut inner).await;
            }
            CrashAction::GiveUp => self.set_phase(Phase::Failed(reason)),
        }
    }
}


#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn new_token_is_long_and_unique() {
        let a = new_token();
        assert_eq!(a.len(), 64);
        assert!(a.chars().all(|c| c.is_ascii_hexdigit()));
        assert_ne!(a, new_token());
    }

    #[test]
    fn restart_policy_restarts_once_per_window() {
        let w = Duration::from_secs(60);
        let t0 = Instant::now();
        let mut p = RestartPolicy::default();
        assert_eq!(p.on_crash(t0, w), CrashAction::Restart);
        assert_eq!(p.on_crash(t0 + Duration::from_secs(10), w), CrashAction::GiveUp);
        assert_eq!(p.on_crash(t0 + Duration::from_secs(61), w), CrashAction::Restart);
        p.reset();
        assert_eq!(p.on_crash(t0 + Duration::from_secs(62), w), CrashAction::Restart);
    }

    #[test]
    fn status_serializes_without_token() {
        assert_eq!(serde_json::to_string(&SidecarStatus::Starting).unwrap(), r#"{"status":"starting"}"#);
        assert_eq!(serde_json::to_string(&SidecarStatus::Ready).unwrap(), r#"{"status":"ready"}"#);
        // I3：等迁移锁时带 waiting=true，前端据此显示“正在等待上一次迁移结束”
        assert_eq!(
            serde_json::to_string(&SidecarStatus::Migrating { waiting: true }).unwrap(),
            r#"{"status":"migrating","waiting":true}"#
        );
        assert_eq!(
            serde_json::to_string(&SidecarStatus::Migrating { waiting: false }).unwrap(),
            r#"{"status":"migrating","waiting":false}"#
        );
        assert_eq!(
            serde_json::to_string(&SidecarStatus::Failed { reason: "x".into() }).unwrap(),
            r#"{"status":"failed","reason":"x"}"#
        );
    }
}

#[cfg(all(test, unix))]
mod manager_tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    const SERVE_4001: &str = r#"read t; echo "READY 4001"; cat >/dev/null"#;
    const SERVE_4002: &str = r#"read t; echo "READY 4002"; cat >/dev/null"#;
    const CRASH_4003: &str = r#"read t; echo "READY 4003"; sleep 0.3; echo boom >&2; exit 9"#;

    fn sh(script: &str) -> LaunchSpec {
        LaunchSpec { program: "/bin/sh".into(), args: vec!["-c".into(), script.into()] }
    }

    fn fast() -> Timeouts {
        Timeouts {
            ready: Duration::from_secs(5),
            grace: Duration::from_millis(500),
            crash_window: Duration::from_secs(60),
        }
    }

    /// 按调用次数依次返回脚本，超出后一直用最后一个。
    fn scripted(scripts: Vec<&'static str>) -> (SpecFactory, Arc<AtomicUsize>) {
        let n = Arc::new(AtomicUsize::new(0));
        let n2 = n.clone();
        let f: SpecFactory = Arc::new(move || {
            let i = n2.fetch_add(1, Ordering::SeqCst);
            Ok(sh(scripts[i.min(scripts.len() - 1)]))
        });
        (f, n)
    }

    fn recorder() -> (Emitter, Arc<std::sync::Mutex<Vec<SidecarStatus>>>) {
        let log = Arc::new(std::sync::Mutex::new(Vec::new()));
        let log2 = log.clone();
        (Arc::new(move |s| log2.lock().unwrap().push(s)), log)
    }

    async fn wait_until(m: &SidecarManager, pred: impl Fn(&SidecarStatus) -> bool) {
        for _ in 0..250 {
            if pred(&m.status()) {
                return;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        panic!("等待状态超时，当前：{:?}", m.status());
    }

    #[tokio::test]
    async fn start_success_sets_ready_and_emits() {
        let (f, _) = scripted(vec![SERVE_4001]);
        let (emit, log) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        assert_eq!(m.status(), SidecarStatus::Starting);
        let info = m.start().await.unwrap();
        assert_eq!(info.port, 4001);
        assert_eq!(info.token.len(), 64);
        assert_eq!(m.status(), SidecarStatus::Ready);
        assert_eq!(m.wait_info().await.unwrap(), info);
        assert_eq!(*log.lock().unwrap(), vec![SidecarStatus::Starting, SidecarStatus::Ready]);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn start_failure_then_restart_recovers() {
        let (f, _) = scripted(vec![r#"read t; echo bad-config >&2; exit 1"#, SERVE_4001]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        let err = m.start().await.unwrap_err();
        assert!(err.starts_with("本地服务启动失败") && err.contains("bad-config"), "{err}");
        assert!(matches!(m.status(), SidecarStatus::Failed { reason } if reason.contains("bad-config")));
        assert!(m.wait_info().await.is_err());
        assert_eq!(m.restart().await.unwrap().port, 4001);
        assert_eq!(m.status(), SidecarStatus::Ready);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn spec_factory_error_becomes_failed() {
        let f: SpecFactory = Arc::new(|| Err("数据目录不可用：/Volumes/外接盘".to_string()));
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        let err = m.start().await.unwrap_err();
        assert!(err.contains("数据目录不可用"), "{err}");
    }

    #[tokio::test]
    async fn crash_after_ready_restarts_once() {
        let (f, n) = scripted(vec![CRASH_4003, SERVE_4001]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        assert_eq!(m.start().await.unwrap().port, 4003);
        for _ in 0..250 {
            if n.load(Ordering::SeqCst) == 2 && m.status() == SidecarStatus::Ready {
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        assert_eq!(n.load(Ordering::SeqCst), 2);
        assert_eq!(m.wait_info().await.unwrap().port, 4001);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn second_crash_within_window_gives_up() {
        let (f, n) = scripted(vec![CRASH_4003, CRASH_4003]);
        let (emit, log) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        m.start().await.unwrap();
        wait_until(&m, |s| matches!(s, SidecarStatus::Failed { .. })).await;
        assert_eq!(n.load(Ordering::SeqCst), 2);
        let SidecarStatus::Failed { reason } = m.status() else { unreachable!() };
        assert!(reason.contains("意外退出") && reason.contains("9") && reason.contains("boom"), "{reason}");
        let log = log.lock().unwrap();
        assert_eq!(log.first(), Some(&SidecarStatus::Starting));
        assert!(matches!(log.last(), Some(SidecarStatus::Failed { .. })));
    }

    #[tokio::test]
    async fn shutdown_does_not_trigger_restart() {
        let (f, n) = scripted(vec![SERVE_4001]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        m.start().await.unwrap();
        m.shutdown().await;
        tokio::time::sleep(Duration::from_millis(300)).await;
        assert_eq!(n.load(Ordering::SeqCst), 1);
        assert!(m.restart().await.is_err(), "退出中不再拉起");
    }

    #[tokio::test]
    async fn wait_info_blocks_while_starting() {
        let (f, _) = scripted(vec![r#"read t; sleep 0.3; echo "READY 4001"; cat >/dev/null"#]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        let m2 = m.clone();
        tokio::spawn(async move { m2.start().await });
        assert_eq!(m.wait_info().await.unwrap().port, 4001);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn restart_replaces_running_process_without_auto_restart() {
        let (f, n) = scripted(vec![SERVE_4001, SERVE_4002]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        let a = m.start().await.unwrap();
        let b = m.restart().await.unwrap();
        assert_eq!(b.port, 4002);
        assert_ne!(a.token, b.token, "每次拉起都换 token");
        tokio::time::sleep(Duration::from_millis(300)).await;
        assert_eq!(n.load(Ordering::SeqCst), 2, "旧进程被主动关闭不算崩溃");
        assert_eq!(m.status(), SidecarStatus::Ready);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn concurrent_restarts_end_ready() {
        let (f, n) = scripted(vec![SERVE_4001]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        m.start().await.unwrap();
        let (r1, r2) = tokio::join!(m.restart(), m.restart());
        assert!(r1.is_ok() && r2.is_ok());
        tokio::time::sleep(Duration::from_millis(300)).await;
        assert_eq!(n.load(Ordering::SeqCst), 3);
        assert_eq!(m.status(), SidecarStatus::Ready);
        m.shutdown().await;
    }

    // I3：启动卡住（迟迟不 READY）时退出，不能等到 ready 超时，要在 1 秒内返回。
    #[tokio::test]
    async fn shutdown_during_slow_launch_returns_within_1s() {
        let (f, n) = scripted(vec![r#"read t; sleep 30"#]);
        let (emit, _) = recorder();
        let slow = Timeouts { ready: Duration::from_secs(20), ..fast() };
        let m = SidecarManager::new(f, emit, slow);
        let m2 = m.clone();
        let starting = tokio::spawn(async move { m2.start().await });
        tokio::time::sleep(Duration::from_millis(100)).await;
        let t0 = Instant::now();
        tokio::time::timeout(Duration::from_secs(1), m.shutdown()).await.expect("shutdown 应在 1 秒内返回");
        assert!(t0.elapsed() < Duration::from_secs(1));
        let r = tokio::time::timeout(Duration::from_secs(1), starting).await.expect("start 应随之结束").unwrap();
        assert!(r.is_err());
        assert!(m.inner.lock().await.handle.is_none());
        tokio::time::sleep(Duration::from_millis(200)).await;
        assert_eq!(n.load(Ordering::SeqCst), 1, "退出后不再拉起");
    }

    // I3：自动重启进行中退出，同样立即返回。
    #[tokio::test]
    async fn shutdown_during_auto_restart_returns_within_1s() {
        let (f, n) = scripted(vec![CRASH_4003, r#"read t; sleep 30"#]);
        let (emit, _) = recorder();
        let slow = Timeouts { ready: Duration::from_secs(20), ..fast() };
        let m = SidecarManager::new(f, emit, slow);
        m.start().await.unwrap();
        for _ in 0..250 {
            if n.load(Ordering::SeqCst) == 2 {
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        assert_eq!(n.load(Ordering::SeqCst), 2);
        tokio::time::sleep(Duration::from_millis(100)).await;
        tokio::time::timeout(Duration::from_secs(1), m.shutdown()).await.expect("shutdown 应在 1 秒内返回");
    }

    #[tokio::test]
    async fn shutdown_while_starting_stops_process() {
        let (f, _) = scripted(vec![r#"read t; sleep 0.3; echo "READY 4001"; cat >/dev/null"#]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        let m2 = m.clone();
        let starting = tokio::spawn(async move { m2.start().await });
        tokio::time::sleep(Duration::from_millis(50)).await;
        tokio::time::timeout(Duration::from_secs(3), m.shutdown()).await.expect("shutdown 不应卡死");
        let _ = starting.await;
        assert!(m.inner.lock().await.handle.is_none(), "退出后不应留有运行中的进程");
    }
}
