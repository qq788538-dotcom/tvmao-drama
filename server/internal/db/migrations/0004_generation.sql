-- 生成链路：任务表与结果投递外发箱。
-- 约定同 0001：时间 DATETIME 存 UTC
-- 文本，JSON 列 TEXT 且必须是 text 类型，ON UPDATE CURRENT_TIMESTAMP 用触发器实现。
-- 本文件的表与其他迁移的表没有外键依赖。

CREATE TABLE generation_tasks (
  -- AUTOINCREMENT：id 进对象 key gen/task-<id>-<i>，删除后不得复用。
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  project_id INTEGER NULL,
  model_id TEXT NOT NULL,
  -- queued / running / processing / retry_pending / succeeded / failed（settling 为历史值，代码仍兼容）。
  status TEXT NOT NULL DEFAULT 'queued',
  upstream_task_id TEXT NULL,
  input TEXT NOT NULL CHECK (typeof(input) = 'text'),
  output TEXT NULL CHECK (output IS NULL OR typeof(output) = 'text'),
  -- 产物类型大类 image/video/audio；NULL = 未知或纯文本。
  output_kind TEXT NULL,
  -- 桌面端不计费，两列保留只为代码兼容，值无业务含义。
  frozen_cents INTEGER NOT NULL DEFAULT 0,
  cost_cents INTEGER NULL,
  audit_flag TEXT NULL,
  error TEXT NULL,
  -- 执行租约：哪个进程在跑、最后心跳。启动时上个进程的行会被判为过期（generation/startup.go）。
  owner_instance TEXT NULL,
  heartbeat_at DATETIME NULL,
  -- 结果去向，如 {"kind":"canvas_node","projectId":12,"nodeId":"abc","userId":1}；NULL = 调用方自取。
  sink TEXT NULL CHECK (sink IS NULL OR typeof(sink) = 'text'),
  -- 首次向上游发请求的时刻；NULL = 没调过上游，被中断可安全重排。
  upstream_called_at DATETIME NULL,
  attempt INTEGER NOT NULL DEFAULT 0,
  next_retry_at DATETIME NULL,
  -- 执行权代次：每次领取/接管 +1，所有推进状态的写都要带对它。
  lease_version INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_tasks_user ON generation_tasks (user_id);
CREATE INDEX idx_tasks_project ON generation_tasks (project_id);
CREATE INDEX idx_tasks_status ON generation_tasks (status);
CREATE INDEX idx_gen_tasks_lease ON generation_tasks (status, heartbeat_at);
CREATE INDEX idx_gt_queue ON generation_tasks (status, model_id, id);
CREATE INDEX idx_gt_retry ON generation_tasks (status, next_retry_at);

CREATE TRIGGER trg_generation_tasks_updated_at AFTER UPDATE ON generation_tasks
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE generation_tasks SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TABLE generation_sink_outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id INTEGER NOT NULL,
  sink TEXT NOT NULL CHECK (typeof(sink) = 'text'),
  payload TEXT NOT NULL CHECK (typeof(payload) = 'text'),
  -- pending / delivering / done / dead
  state TEXT NOT NULL DEFAULT 'pending',
  attempt INTEGER NOT NULL DEFAULT 0,
  delivery_version INTEGER NOT NULL DEFAULT 0,
  locked_until DATETIME NULL,
  next_retry_at DATETIME NULL,
  last_error TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT uk_outbox_task UNIQUE (task_id)
);
CREATE INDEX idx_outbox_claim ON generation_sink_outbox (state, next_retry_at, locked_until, id);

CREATE TRIGGER trg_generation_sink_outbox_updated_at AFTER UPDATE ON generation_sink_outbox
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE generation_sink_outbox SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
