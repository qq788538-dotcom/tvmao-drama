-- 桌面端个人素材库（目录树 + 条目）与孤儿对象回收表。
-- 约定同 0001：时间 DATETIME 存 UTC 文本，ON UPDATE CURRENT_TIMESTAMP 用触发器。

CREATE TABLE library_folders (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  -- NULL = 根目录；自引用 RESTRICT：有子目录的目录不能直接删。
  parent_id INTEGER NULL REFERENCES library_folders (id) ON DELETE RESTRICT,
  name TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_library_folders_user ON library_folders (user_id);
CREATE INDEX idx_library_folders_parent ON library_folders (parent_id);

CREATE TABLE library_items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  folder_id INTEGER NOT NULL REFERENCES library_folders (id) ON DELETE RESTRICT,
  -- 与 assets 共用对象：孤儿回收按 assets ∪ library_items 的 object_key 判断是否仍被引用。
  object_key TEXT NOT NULL,
  url TEXT NOT NULL,
  -- 引用行（存入素材库 / 从画布保存）为 0，不重复占配额。
  size_bytes INTEGER NOT NULL,
  mime_type TEXT NOT NULL,
  kind TEXT NOT NULL,
  width INTEGER NULL,
  height INTEGER NULL,
  duration_ms INTEGER NULL,
  source TEXT NOT NULL DEFAULT 'upload',
  name TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_library_items_user ON library_items (user_id);
CREATE INDEX idx_library_items_folder ON library_items (folder_id);
CREATE INDEX idx_library_items_object_key ON library_items (object_key);

-- 删除记录后已无引用的对象先在这里打标记，过了宽限期（7 天）才由清扫真正删文件。
CREATE TABLE orphan_objects (
  object_key TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  size_bytes INTEGER NOT NULL,
  marked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_orphan_marked_at ON orphan_objects (marked_at);

CREATE TRIGGER trg_library_folders_updated_at AFTER UPDATE ON library_folders
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE library_folders SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
