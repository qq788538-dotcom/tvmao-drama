-- 桌面端 SQLite 基线（P1a）：本地用户、设置、模型中心、项目、画布快照、资产。
-- 列与参考仓 MySQL 最终结构一致（由迁移 0001…0099 推导，见 docs/superpowers/plans/p1-research.md）。
-- 约定：时间列 DATETIME，存 UTC 文本 'YYYY-MM-DD HH:MM:SS'；JSON 列 TEXT；布尔 INTEGER 0/1；
-- MySQL 的 ON UPDATE CURRENT_TIMESTAMP 用 AFTER UPDATE 触发器实现。

CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  role TEXT NOT NULL DEFAULT 'user',
  storage_quota_bytes INTEGER NOT NULL DEFAULT 10737418240,
  storage_used_bytes INTEGER NOT NULL DEFAULT 0,
  balance_credits INTEGER NOT NULL DEFAULT 0,
  frozen_credits INTEGER NOT NULL DEFAULT 0,
  upstream_api_key TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 桌面端只有一个本地用户：id 固定为 1，管理员（可用后台配置页），配额 1 PiB 视同不限。
INSERT INTO users (id, email, role, storage_quota_bytes)
VALUES (1, 'local@miaoju.desktop', 'admin', 1125899906842624);

CREATE TABLE settings (
  "key" TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE model_configs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  model_id TEXT NOT NULL,
  base_path TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  hidden INTEGER NOT NULL DEFAULT 0,
  sort_order INTEGER NOT NULL DEFAULT 0,
  request_template TEXT NOT NULL CHECK (typeof(request_template) = 'text'),
  polling_template TEXT NULL CHECK (polling_template IS NULL OR typeof(polling_template) = 'text'),
  ui_schema TEXT NULL CHECK (ui_schema IS NULL OR typeof(ui_schema) = 'text'),
  billing_config TEXT NULL CHECK (billing_config IS NULL OR typeof(billing_config) = 'text'),
  gen_control TEXT NULL CHECK (gen_control IS NULL OR typeof(gen_control) = 'text'),
  image_input_format TEXT NOT NULL DEFAULT 'url',
  image_request_mode TEXT NOT NULL DEFAULT 'json',
  admin_note TEXT NOT NULL DEFAULT '',
  price_note TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT uniq_model_id UNIQUE (model_id)
);

CREATE TABLE projects (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users (id),
  name TEXT NOT NULL,
  description TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  deleted_at DATETIME NULL,
  -- 手动指定的封面，canonical 形态 media://key；NULL 或空串 = 未指定，回退到最早一张生成图。
  cover_url TEXT NULL
);
CREATE INDEX idx_projects_user ON projects (user_id);
CREATE INDEX idx_projects_deleted_at ON projects (deleted_at);

CREATE TABLE workflow_snapshots (
  project_id INTEGER PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
  payload TEXT NOT NULL CHECK (typeof(payload) = 'text'),
  -- 乐观锁版本号，每次成功保存自增。
  version INTEGER NOT NULL DEFAULT 0,
  -- 最后一次客户端保存后的版本号；后端权威回写不更新它。
  last_client_version INTEGER NOT NULL DEFAULT 0,
  synced_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE assets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NULL,
  project_id INTEGER NULL,
  source_task_id INTEGER NULL,
  object_key TEXT NOT NULL,
  source_kind TEXT NOT NULL DEFAULT 'upload',
  url TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  mime_type TEXT NOT NULL,
  width INTEGER NULL,
  height INTEGER NULL,
  duration_ms INTEGER NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  library_item_id INTEGER NULL,
  content_hash TEXT NULL,
  -- 生成产物在该任务输出中的序号（0 起）；与 source_task_id 组成唯一键，重放不产生重复资产。
  output_index INTEGER NULL,
  CONSTRAINT uk_assets_task_output UNIQUE (source_task_id, output_index)
);
CREATE INDEX idx_assets_user ON assets (user_id);
CREATE INDEX idx_assets_project ON assets (project_id);
CREATE INDEX idx_assets_object_key ON assets (object_key);
CREATE INDEX idx_assets_library_item_id ON assets (library_item_id);
CREATE INDEX idx_assets_user_hash ON assets (user_id, content_hash);

-- ON UPDATE CURRENT_TIMESTAMP 的等价实现。WHEN 条件让显式写入时间列的 UPDATE 保持原值。
CREATE TRIGGER trg_users_updated_at AFTER UPDATE ON users
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE users SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER trg_settings_updated_at AFTER UPDATE ON settings
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE settings SET updated_at = CURRENT_TIMESTAMP WHERE "key" = NEW."key";
END;

CREATE TRIGGER trg_model_configs_updated_at AFTER UPDATE ON model_configs
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE model_configs SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER trg_projects_updated_at AFTER UPDATE ON projects
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE projects SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER trg_workflow_snapshots_synced_at AFTER UPDATE ON workflow_snapshots
FOR EACH ROW WHEN NEW.synced_at = OLD.synced_at
BEGIN
  UPDATE workflow_snapshots SET synced_at = CURRENT_TIMESTAMP WHERE project_id = NEW.project_id;
END;
