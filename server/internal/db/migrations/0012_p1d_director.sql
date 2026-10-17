-- 3D 导演台模型库。
-- 用户级云端模型库：用户上传的 FBX/OBJ/GLB；画布快照里以 media://<object_key> 引用，
-- 删除前扫 workflow_snapshots.payload 做引用检查，删行后进 orphan_objects 走 7 天宽限回收。
CREATE TABLE director_user_models (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  -- 展示名：原始文件名去扩展名
  name TEXT NOT NULL,
  -- 原始上传文件名（含扩展名）
  file_name TEXT NOT NULL,
  object_key TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  mime_type TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_director_user_models_user ON director_user_models (user_id);

-- 全局模型库：平台资产，用户端只读。桌面端内置库为空。
CREATE TABLE director_library_categories (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE director_library_models (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  -- 分类下还有模型时数据库层面拒删分类（业务层另有 CountModels 预检）
  category_id INTEGER NOT NULL REFERENCES director_library_categories (id) ON DELETE RESTRICT,
  name TEXT NOT NULL,
  file_name TEXT NOT NULL,
  object_key TEXT NOT NULL,
  -- NULL = 无缩略图
  thumb_object_key TEXT NULL,
  size_bytes INTEGER NOT NULL,
  mime_type TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_director_library_models_category ON director_library_models (category_id);
