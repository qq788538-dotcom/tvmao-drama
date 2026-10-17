-- 内置数据：
--   user_overrides：用户改过的字段路径，JSON 字符串数组，如 ["enabled","ui_schema.ratio.default"]；
--     路径语法见 internal/builtin/path.go。'[]' = 全部字段跟随内置种子。
--   builtin_seed_state：每个内置数据集最近一次导入的种子哈希；哈希没变则启动时跳过合并。
-- 只有 ADD COLUMN 和新表，不依赖 0001 之后的其他迁移。
ALTER TABLE model_configs ADD COLUMN user_overrides TEXT NOT NULL DEFAULT '[]' CHECK (typeof(user_overrides) = 'text');
ALTER TABLE settings ADD COLUMN user_overrides TEXT NOT NULL DEFAULT '[]' CHECK (typeof(user_overrides) = 'text');

CREATE TABLE builtin_seed_state (
  dataset TEXT PRIMARY KEY,
  hash TEXT NOT NULL,
  applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TRIGGER trg_builtin_seed_state_updated_at AFTER UPDATE ON builtin_seed_state
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE builtin_seed_state SET updated_at = CURRENT_TIMESTAMP WHERE dataset = NEW.dataset;
END;
