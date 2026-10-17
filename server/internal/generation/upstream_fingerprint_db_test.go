package generation

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

// 提交时在任务行记下上游指纹（规范化地址 + Key 的 sha256 前 16 位），重启续轮询时
// 用当前解析出的上游重算比对；对不上立即失败退款，不拿另一个账号的 Key 去盲轮询 24 小时。

func newFingerprintHarness(t *testing.T, m *models.ModelConfig, settings planSettings, key string) (*sql.DB, *Service) {
	t.Helper()
	conn := dbtest.Open(t)
	svc := &Service{
		repo:          NewSQLiteRepository(conn),
		billing:       &billingSpy{},
		db:            conn,
		leaseStaleSec: 60,
		models:        planModels{m: m},
		settings:      settings,
		upstreamKey:   planKeys{key: key},
		concurrency:   NewConcurrencyRegistry(),
	}
	return conn, svc
}

// insertOrphan 插一条心跳早已超时、已拿到上游任务 ID 的 processing 行。
func insertOrphan(t *testing.T, conn *sql.DB, fingerprint string) int64 {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, upstream_task_id, upstream_called_at, upstream_fingerprint, owner_instance, heartbeat_at, lease_version)
		VALUES (1, 'm', 'processing', '{}', 40, 'up-123', '2026-01-01 00:00:00', ?, 'dead-instance', datetime('now','-1 hours'), 3)`, fingerprint)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func taskStatusErr(t *testing.T, conn *sql.DB, id int64) (status, msg string) {
	t.Helper()
	var e sql.NullString
	if err := conn.QueryRow(`SELECT status, error FROM generation_tasks WHERE id=?`, id).Scan(&status, &e); err != nil {
		t.Fatal(err)
	}
	return status, e.String
}

func TestResumeFailsWhenUpstreamFingerprintChanged(t *testing.T) {
	// 提交时走的是独立上游 A；任务进行中用户把模型改成了上游 B。
	m := &models.ModelConfig{ModelID: "m", Enabled: true, PollingTemplate: json.RawMessage(`{"url":"/v1/video/generations/{{taskId}}"}`),
		UpstreamBaseURL: "https://b.example", UpstreamAPIKey: "sk-b"}
	conn, svc := newFingerprintHarness(t, m, okSettings(), "sk-global")
	id := insertOrphan(t, conn, models.UpstreamFingerprint("https://a.example", "sk-a"))

	svc.ResumeOrphanedTasks()

	status, msg := taskStatusErr(t, conn, id)
	want := "该任务提交时使用的上游配置已变更，无法继续查询结果（上游任务 ID：up-123）"
	if status != "failed" || msg != want {
		t.Fatalf("status=%s msg=%q", status, msg)
	}
	assertOps(t, svc, "RefundTx")
}

// 同一地址只换了 Key（换账号）也算变更。
func TestResumeFailsWhenOnlyKeyChanged(t *testing.T) {
	m := &models.ModelConfig{ModelID: "m", Enabled: true, PollingTemplate: json.RawMessage(`{"url":"/x/{{taskId}}"}`)}
	conn, svc := newFingerprintHarness(t, m, okSettings(), "sk-new")
	id := insertOrphan(t, conn, models.UpstreamFingerprint("https://api.example.com", "sk-old"))

	svc.ResumeOrphanedTasks()

	if status, msg := taskStatusErr(t, conn, id); status != "failed" || !strings.Contains(msg, "上游配置已变更") {
		t.Fatalf("status=%s msg=%q", status, msg)
	}
}

// 指纹一致、或旧任务没有指纹：放行，走原有续轮询流程。这里模型没有 pollingTemplate，
// 放行后会以原有的 "orphaned" 文案失败——用它证明没被指纹检查拦下。
func TestResumePassesMatchingOrLegacyFingerprint(t *testing.T) {
	m := &models.ModelConfig{ModelID: "m", Enabled: true, UpstreamBaseURL: "https://a.example/v1", UpstreamAPIKey: "sk-a"}
	for name, fp := range map[string]string{
		"一致":     models.UpstreamFingerprint("https://a.example", "sk-a"),
		"旧任务无指纹": "",
	} {
		t.Run(name, func(t *testing.T) {
			conn, svc := newFingerprintHarness(t, m, okSettings(), "sk-global")
			id := insertOrphan(t, conn, fp)
			svc.ResumeOrphanedTasks()
			if _, msg := taskStatusErr(t, conn, id); !strings.HasPrefix(msg, "orphaned:") {
				t.Fatalf("应通过指纹检查, msg=%q", msg)
			}
		})
	}
}

// 提交路径：发上游请求之前把指纹和"已调过上游"一起落库（不存 Key 本身）。
func TestCallAndCompleteRecordsUpstreamFingerprint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadRequest)
	}))
	defer srv.Close()
	m := &models.ModelConfig{ModelID: "m", Enabled: true, BasePath: "/v1/video/generations"}
	conn, svc := newFingerprintHarness(t, m, okSettings(), "sk-global")
	res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, frozen_cents, owner_instance, heartbeat_at, lease_version)
		VALUES (1, 'm', 'running', '{}', 40, 'me', CURRENT_TIMESTAMP, 2)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()

	_, _ = svc.callAndComplete(context.Background(), id, 2, m, "sk-secret-key", srv.URL+"/", []byte(`{}`), 1, nil, 40, nil, nil, "")

	var fp string
	if err := conn.QueryRow(`SELECT upstream_fingerprint FROM generation_tasks WHERE id=?`, id).Scan(&fp); err != nil {
		t.Fatal(err)
	}
	if fp != models.UpstreamFingerprint(srv.URL, "sk-secret-key") || strings.Contains(fp, "secret") {
		t.Fatalf("指纹不对: %q", fp)
	}
}
