-- 每模型独立上游（2026-10-07 用户裁定，见 spec 实施期裁定）。
--   upstream_base_url / upstream_api_key：本机用户数据，不进种子；builtin.Apply 升级内置模型时不得覆盖。
--   两列要么同时为空（走全局 settings.base_domain + upstream_api_key），要么同时非空（走模型自己的）。
--   解析入口唯一：models.ResolveUpstream。
ALTER TABLE model_configs ADD COLUMN upstream_base_url TEXT NOT NULL DEFAULT '';
ALTER TABLE model_configs ADD COLUMN upstream_api_key TEXT NOT NULL DEFAULT ''
  CHECK ((upstream_base_url = '') = (upstream_api_key = ''));
