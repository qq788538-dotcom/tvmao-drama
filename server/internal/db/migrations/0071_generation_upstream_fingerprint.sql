-- 生成任务记录提交时使用的上游指纹。
--   upstream_fingerprint = 规范化地址 + "#" + Key 的 sha256 前 16 个十六进制字符（models.UpstreamFingerprint），不存 Key 本身。
--   重启续轮询时用当前解析出的上游重算比对，不一致立即失败退款，避免拿另一个账号的 Key 盲轮询。
--   空串 = 迁移前提交的旧任务，没有指纹，续轮询时直接放行。
ALTER TABLE generation_tasks ADD COLUMN upstream_fingerprint TEXT NOT NULL DEFAULT '';
