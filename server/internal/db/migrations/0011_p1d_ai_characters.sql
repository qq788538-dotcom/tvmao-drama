-- 自由画布「AI 角色库」。只建表；1729 条角色数据由内置种子从
-- seed/seedance_characters.jsonl 导入，image_url 保持 canonical 的 media://<object_key> 形态。
CREATE TABLE seedance_characters (
  -- Seedance 素材库分配的角色资产 id（asset-YYYYMMDDHHMMSS-xxxxx），生成时以 asset://<id> 引用。
  asset_id TEXT PRIMARY KEY,
  age INTEGER NOT NULL,
  -- 男 / 女
  gender TEXT NOT NULL,
  occupation TEXT NOT NULL,
  description TEXT NOT NULL,
  -- 青年 / 中年 / 老年
  age_group TEXT NOT NULL,
  -- 形象图 canonical 引用 media://<object_key>；图片本体由 sidecar 按需从 ai_character_image_base_url 下载缓存。
  image_url TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- 桌面端每次生成（asset:// 替换）和每个 /media 请求（缓存判定）都按 image_url 查。
CREATE INDEX idx_seedance_characters_image_url ON seedance_characters (image_url);

CREATE TRIGGER trg_seedance_characters_updated_at AFTER UPDATE ON seedance_characters
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE seedance_characters SET updated_at = CURRENT_TIMESTAMP WHERE asset_id = NEW.asset_id;
END;
