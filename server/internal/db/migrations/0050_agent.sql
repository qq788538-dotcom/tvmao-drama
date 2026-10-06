-- P5 Agent：对话线程/消息、Agent 工具目录、Skill 与分类、Agent 大模型。
-- 列与参考仓 MySQL 最终结构一致（迁移 0016–0021、0100–0102、0108、0117）。
-- 约定同 0001：时间 DATETIME 存 UTC 文本；JSON 列 TEXT 且 CHECK typeof='text'；布尔 INTEGER。
-- 例外：agent_messages.created_at / summarized_at 是 26 字符微秒文本（keyset 分页依赖），由 Go 写入。

CREATE TABLE agent_backbone_models (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  alias TEXT NOT NULL,
  provider TEXT NOT NULL,
  model_name TEXT NOT NULL,
  role TEXT NOT NULL,
  thinking_enabled INTEGER NOT NULL DEFAULT 0,
  thinking_budget INTEGER NOT NULL DEFAULT 0,
  max_tokens INTEGER NOT NULL DEFAULT 4096,
  temperature REAL NOT NULL DEFAULT 0.7,
  is_active INTEGER NOT NULL DEFAULT 1,
  sort_weight INTEGER NOT NULL DEFAULT 0,
  description TEXT NOT NULL DEFAULT '',
  context_window INTEGER NOT NULL DEFAULT 8192,
  -- 桌面端新增（spec A2）：1 = 用户在设置里切换过 model_name，内置数据升级时不覆盖该列。
  model_name_overridden INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_abm_role_active ON agent_backbone_models (role, is_active, sort_weight);

CREATE TABLE agent_threads (
  id TEXT NOT NULL PRIMARY KEY,
  parent_thread_id TEXT NOT NULL DEFAULT '',
  project_id INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  agent_name TEXT NOT NULL DEFAULT 'planner',
  summary TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_agent_thread_project ON agent_threads (project_id);
CREATE INDEX idx_agent_thread_parent ON agent_threads (parent_thread_id);

CREATE TABLE agent_messages (
  id TEXT NOT NULL PRIMARY KEY,
  thread_id TEXT NOT NULL REFERENCES agent_threads (id) ON DELETE CASCADE,
  action_id TEXT NOT NULL,
  role TEXT NOT NULL,
  content_blocks TEXT NOT NULL CHECK (typeof(content_blocks) = 'text'),
  stop_reason TEXT NOT NULL DEFAULT '',
  -- 'YYYY-MM-DD HH:MM:SS.ffffff'。正常由 agent.agentMicroNow() 写入；默认值只是兜底（毫秒补 000，保持定宽）。
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '000'),
  summarized_at DATETIME NULL
);
CREATE INDEX idx_agent_msg_thread ON agent_messages (thread_id, created_at);
CREATE INDEX idx_agent_msg_action ON agent_messages (thread_id, action_id);
CREATE INDEX idx_agent_msg_thread_active ON agent_messages (thread_id, summarized_at, created_at);

CREATE TABLE agent_tools (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tool_name TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL,
  source_model_id INTEGER NOT NULL REFERENCES model_configs (id) ON DELETE CASCADE ON UPDATE CASCADE,
  tool_description TEXT NOT NULL,
  params_schema TEXT NOT NULL CHECK (typeof(params_schema) = 'text'),
  default_params TEXT NOT NULL CHECK (typeof(default_params) = 'text'),
  is_published INTEGER NOT NULL DEFAULT 0,
  sort_weight INTEGER NOT NULL DEFAULT 0,
  use_count_30d INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_at_pub_cat ON agent_tools (is_published, category, sort_weight);

CREATE TABLE skills (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL,
  categories TEXT NULL CHECK (categories IS NULL OR typeof(categories) = 'text'),
  icon_url TEXT NOT NULL DEFAULT '',
  skill_type TEXT NOT NULL,
  system_prompt_addon TEXT NOT NULL,
  force_tools TEXT NOT NULL CHECK (typeof(force_tools) = 'text'),
  param_defaults TEXT NOT NULL CHECK (typeof(param_defaults) = 'text'),
  workflow_steps TEXT NOT NULL CHECK (typeof(workflow_steps) = 'text'),
  trigger_keywords TEXT NULL CHECK (trigger_keywords IS NULL OR typeof(trigger_keywords) = 'text'),
  is_official INTEGER NOT NULL DEFAULT 1,
  creator_id INTEGER NULL REFERENCES users (id) ON DELETE SET NULL,
  is_published INTEGER NOT NULL DEFAULT 1,
  is_featured INTEGER NOT NULL DEFAULT 0,
  use_count INTEGER NOT NULL DEFAULT 0,
  sort_weight INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_skills_cat_pub ON skills (category, is_published, sort_weight);
CREATE INDEX idx_skills_creator ON skills (creator_id);

CREATE TABLE skill_categories (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  color TEXT NOT NULL DEFAULT 'blue',
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TRIGGER trg_agent_backbone_models_updated_at AFTER UPDATE ON agent_backbone_models
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE agent_backbone_models SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
CREATE TRIGGER trg_agent_threads_updated_at AFTER UPDATE ON agent_threads
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE agent_threads SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
CREATE TRIGGER trg_agent_tools_updated_at AFTER UPDATE ON agent_tools
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE agent_tools SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
CREATE TRIGGER trg_skills_updated_at AFTER UPDATE ON skills
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE skills SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
CREATE TRIGGER trg_skill_categories_updated_at AFTER UPDATE ON skill_categories
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE skill_categories SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
