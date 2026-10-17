-- 画布素材的 Seedance 2.0 合规校验状态，
-- 按 object_key 归属；外加用户级素材组 id 与建组版本。
CREATE TABLE seedance_assets (
  -- 对象 key，素材的唯一身份；画布 assets 与素材库 library_items 共用同一个 key。
  object_key TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  -- Seedance 素材库 asset-id；生成时把素材引用替换成 asset://<id>。
  asset_id TEXT NULL,
  -- 校验时的合规配置版本号（seedance_compliance_config.version），版本轮换触发重新校验。
  version INTEGER NULL,
  -- pending / active / failed / failed_permanent
  status TEXT NOT NULL,
  error TEXT NULL,
  -- 首次失败时间，驱动 1 小时重试兜底窗口。
  first_failed_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  -- 上游 CreateAsset 的 AssetType：Image / Video / Audio；worker 重试时据此重放。
  asset_type TEXT NOT NULL DEFAULT 'Image'
);
CREATE INDEX idx_seedance_assets_status ON seedance_assets (status);
CREATE INDEX idx_seedance_assets_user ON seedance_assets (user_id);

CREATE TRIGGER trg_seedance_assets_updated_at AFTER UPDATE ON seedance_assets
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE seedance_assets SET updated_at = CURRENT_TIMESTAMP WHERE object_key = NEW.object_key;
END;

-- 用户在 Seedance 素材库的素材组 id（首次校验时惰性创建）与建组时的版本号（与当前版本不符即重建）。
ALTER TABLE users ADD COLUMN seedance_group_id TEXT NULL;
ALTER TABLE users ADD COLUMN seedance_group_version INTEGER NOT NULL DEFAULT 0;
