-- CLI 写请求的幂等记录。CLI 网络失败后会带同一个
-- Idempotency-Key 重试，没有这张表就会建出第二个节点、再跑一次生成。
CREATE TABLE IF NOT EXISTS cli_idempotency_keys (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER  NOT NULL,
  scope         TEXT     NOT NULL,
  idem_key      TEXT     NOT NULL,
  request_hash  TEXT     NOT NULL,
  status        TEXT     NOT NULL,
  status_code   INTEGER  NOT NULL DEFAULT 0,
  response_body BLOB     NULL,
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (user_id, scope, idem_key)
);
CREATE INDEX IF NOT EXISTS idx_cli_idem_created_at ON cli_idempotency_keys(created_at);
