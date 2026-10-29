-- 脚本节点任务失败重排的退避（P30）：失败后回到 pending 时写下次最早可领的时间，
-- worker 只领 next_attempt_at 为空或已到点的任务。上游限流时不至于几秒内就把重试次数用完。
-- NULL = 立即可领（新任务、启动回收、关闭打断的重排都不退避）。
ALTER TABLE canvas_script_tasks ADD COLUMN next_attempt_at DATETIME NULL;
