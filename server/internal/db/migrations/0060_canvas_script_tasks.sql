-- 自由画布脚本节点“剧本生成分镜”任务表。
-- 桌面端没有积分：frozen_cents 恒写 0，actor_user_id 恒为 NULL，保留列只为代码兼容。
-- id 用 AUTOINCREMENT：前端节点长期持有 taskId，删除后复用 id 会让旧节点查到别的任务。
CREATE TABLE canvas_script_tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  project_id INTEGER NOT NULL,
  node_id TEXT NOT NULL,
  skill_name TEXT NOT NULL,
  duration_sec INTEGER NOT NULL,
  script TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  result TEXT NULL,
  error TEXT NULL,
  frozen_cents INTEGER NOT NULL DEFAULT 0,
  owner_instance TEXT NULL,
  heartbeat_at DATETIME NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  model TEXT NOT NULL DEFAULT '',
  actor_user_id INTEGER NULL
);

CREATE INDEX idx_cst_status ON canvas_script_tasks (status, updated_at);
CREATE INDEX idx_cst_user ON canvas_script_tasks (user_id, id);

CREATE TRIGGER trg_canvas_script_tasks_updated_at AFTER UPDATE ON canvas_script_tasks
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE canvas_script_tasks SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
