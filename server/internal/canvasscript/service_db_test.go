package canvasscript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/skillruntime"
)

type fakeSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func newFakeSettings(cfg Config) *fakeSettings {
	b, _ := json.Marshal(cfg)
	return &fakeSettings{m: map[string]string{SettingKey: string(b), "base_domain": "https://up.example"}}
}

func (f *fakeSettings) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[key], nil
}

func (f *fakeSettings) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[key] = value
	return nil
}

type fakeModels struct {
	missing  map[string]bool
	upstream map[string][2]string // model_id → {独立上游地址, Key}
}

func (f fakeModels) GetByModelID(id string) (*models.ModelConfig, error) {
	if f.missing[id] {
		return nil, models.ErrNotFound
	}
	up := f.upstream[id]
	return &models.ModelConfig{ModelID: id, RequestTemplate: json.RawMessage(`{"max_tokens":8000,"messages":"{{messages}}"}`),
		UpstreamBaseURL: up[0], UpstreamAPIKey: up[1]}, nil
}

type fakeKeys struct{ key string }

func (f fakeKeys) Resolve(int64) (string, error) { return f.key, nil }

type fakeSkills struct {
	mu      sync.Mutex
	snapErr error
	runErr  error
	content string
	runs    []skillruntime.RunRequest
}

func (f *fakeSkills) Snapshot(_ context.Context, name string) (skillruntime.Snapshot, error) {
	if f.snapErr != nil {
		return skillruntime.Snapshot{}, f.snapErr
	}
	return skillruntime.Snapshot{Name: name}, nil
}

func (f *fakeSkills) Run(_ context.Context, req skillruntime.RunRequest) (skillruntime.RunResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, req)
	if f.runErr != nil {
		return skillruntime.RunResult{}, f.runErr
	}
	return skillruntime.RunResult{Content: f.content}, nil
}

const (
	testModel      = "canvas-gpt-5.5"
	testStyle      = "真人短剧风格 v3"
	validCandidate = `{"storyboard":[{"name":"镜1"}]}`
)

func testConfig() Config {
	return Config{
		ModelCandidates: []ModelCandidate{{ModelID: testModel, DisplayName: "gpt-5.5"}},
		Styles: []StyleEntry{
			{Name: "真人短剧风格", Version: "v3", SkillName: "real-person-short-drama-v3", Enabled: true},
			{Name: "停用风格", SkillName: "x", Enabled: false},
		},
		Concurrency: 1,
		MaxRetries:  1,
	}
}

func newTestService(t *testing.T, cfg Config, sk *fakeSkills, m fakeModels) (*Service, *sql.DB) {
	t.Helper()
	conn := dbtest.Open(t)
	return NewService(conn, newFakeSettings(cfg), m, fakeKeys{key: "sk-test"}, sk), conn
}

func submitOne(t *testing.T, svc *Service) int64 {
	t.Helper()
	id, err := svc.Submit(context.Background(), 1, 7, "node-1", testStyle, "第一场：安岁推门。", 10, testModel, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func taskRow(t *testing.T, conn *sql.DB, id int64) (status, errMsg string, attempts int) {
	t.Helper()
	var e sql.NullString
	if err := conn.QueryRow(`SELECT status, error, attempts FROM canvas_script_tasks WHERE id=?`, id).Scan(&status, &e, &attempts); err != nil {
		t.Fatal(err)
	}
	return status, e.String, attempts
}

func TestSubmitPersistsPendingTask(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	id := submitOne(t, svc)
	var status, model, skill string
	var frozen, attempts int64
	var actor sql.NullInt64
	err := conn.QueryRow(`SELECT status, model, skill_name, frozen_cents, attempts, actor_user_id FROM canvas_script_tasks WHERE id=?`, id).
		Scan(&status, &model, &skill, &frozen, &attempts, &actor)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusPending || model != testModel || skill != "real-person-short-drama-v3" || frozen != 0 || attempts != 0 || actor.Valid {
		t.Fatalf("status=%s model=%s skill=%s frozen=%d attempts=%d actor=%v", status, model, skill, frozen, attempts, actor)
	}
}

func TestSubmitValidation(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	cases := []struct {
		name, style, script string
		dur                 int
		model, want         string
	}{
		{"空剧本", testStyle, "  ", 10, testModel, "剧本内容不能为空"},
		{"超长", testStyle, strings.Repeat("字", MaxScriptRunes+1), 10, testModel, "剧本最长"},
		{"时长过短", testStyle, "x", 3, testModel, "时长需在"},
		{"时长过长", testStyle, "x", 16, testModel, "时长需在"},
		{"未选模型", testStyle, "x", 10, "", "请选择模型"},
		{"模型不在候选", testStyle, "x", 10, "other", "所选模型不可用"},
		{"风格不存在", "不存在 v9", "x", 10, testModel, "所选风格不存在或已停用"},
		{"风格停用", "停用风格", "x", 10, testModel, "所选风格不存在或已停用"},
	}
	for _, c := range cases {
		_, err := svc.Submit(context.Background(), 1, 7, "n", c.style, c.script, c.dur, c.model, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%v want %q", c.name, err, c.want)
		}
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM canvas_script_tasks`).Scan(&n)
	if n != 0 {
		t.Fatalf("校验失败不应落库，got %d 行", n)
	}
}

func TestSubmitRejectsMissingModelAndSkill(t *testing.T) {
	svc, _ := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{missing: map[string]bool{testModel: true}})
	if _, err := svc.Submit(context.Background(), 1, 7, "n", testStyle, "x", 10, testModel, nil); err == nil || !strings.Contains(err.Error(), "所选模型不可用") {
		t.Fatalf("模型中心查不到应拒绝: %v", err)
	}
	svc2, _ := newTestService(t, testConfig(), &fakeSkills{snapErr: errors.New("no such skill")}, fakeModels{})
	if _, err := svc2.Submit(context.Background(), 1, 7, "n", testStyle, "x", 10, testModel, nil); err == nil || !strings.Contains(err.Error(), "风格对应的 skill 不可用") {
		t.Fatalf("skill 不存在应拒绝: %v", err)
	}
}

// 桌面端没有积分（X3）：零价模型必须保留，只剔除模型中心查不到的候选。
func TestEnabledStylesKeepsZeroPriceCandidates(t *testing.T) {
	cfg := testConfig()
	cfg.ModelCandidates = append(cfg.ModelCandidates, ModelCandidate{ModelID: "deleted-model", DisplayName: "已删除"})
	svc, _ := newTestService(t, cfg, &fakeSkills{}, fakeModels{missing: map[string]bool{"deleted-model": true}})
	styles, cands, err := svc.EnabledStyles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(styles) != 1 || styles[0].Key() != testStyle {
		t.Fatalf("只应返回启用的风格: %#v", styles)
	}
	if len(cands) != 1 || cands[0].ModelID != testModel || cands[0].Credits != 0 {
		t.Fatalf("零价候选应保留、已删除的剔除: %#v", cands)
	}
}

func TestClaimOneConcurrentSingleWinner(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	id := submitOne(t, svc)
	var wins int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := svc.claimOne(context.Background()); ok {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("应恰好一个抢到，got %d", wins)
	}
	status, _, attempts := taskRow(t, conn, id)
	var owner string
	_ = conn.QueryRow(`SELECT owner_instance FROM canvas_script_tasks WHERE id=?`, id).Scan(&owner)
	if status != StatusRunning || attempts != 1 || owner != svc.instanceID {
		t.Fatalf("status=%s attempts=%d owner=%s", status, attempts, owner)
	}
}

func TestExecuteSuccessStoresResult(t *testing.T) {
	sk := &fakeSkills{content: validCandidate}
	svc, _ := newTestService(t, testConfig(), sk, fakeModels{})
	id := submitOne(t, svc)
	task, ok := svc.claimOne(context.Background())
	if !ok {
		t.Fatal("应抢到任务")
	}
	svc.execute(context.Background(), task, testConfig())
	got, err := svc.Get(context.Background(), 1, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusSucceeded || got.Result != validCandidate {
		t.Fatalf("status=%s result=%s", got.Status, got.Result)
	}
	req := sk.runs[0]
	if !strings.Contains(req.Input, "DURATION_SEC=10") || req.SourceText != "第一场：安岁推门。" {
		t.Fatalf("Input/SourceText 不对: %#v", req)
	}
	if req.Model.BaseURL != "https://up.example" || req.Model.APIKey != "sk-test" || req.Model.Model != testModel {
		t.Fatalf("模型配置不对: %#v", req.Model)
	}
	if _, ok := req.Model.ExtraFields["messages"]; ok || req.Model.ExtraFields["max_tokens"] != float64(8000) {
		t.Fatalf("ExtraFields 透传不对: %#v", req.Model.ExtraFields)
	}
}

func TestExecuteFailRequeuesThenFails(t *testing.T) {
	sk := &fakeSkills{runErr: errors.New("上游 500")}
	svc, conn := newTestService(t, testConfig(), sk, fakeModels{})
	id := submitOne(t, svc)
	task, _ := svc.claimOne(context.Background())
	svc.execute(context.Background(), task, testConfig())
	if status, msg, _ := taskRow(t, conn, id); status != StatusPending || !strings.Contains(msg, "生成失败") {
		t.Fatalf("第 1 次失败应重排: status=%s err=%s", status, msg)
	}
	task, _ = svc.claimOne(context.Background())
	svc.execute(context.Background(), task, testConfig())
	if status, msg, attempts := taskRow(t, conn, id); status != StatusFailed || attempts != 2 || !strings.Contains(msg, "生成失败") || strings.Contains(msg, "上游 500") {
		t.Fatalf("用尽重试应判失败: status=%s attempts=%d err=%s", status, attempts, msg)
	}
}

func TestExecuteBadCandidateFails(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{content: `{"storyboard":[]}`}, fakeModels{})
	id := submitOne(t, svc)
	task, _ := svc.claimOne(context.Background())
	svc.execute(context.Background(), task, testConfig())
	if _, msg, _ := taskRow(t, conn, id); !strings.Contains(msg, "不符合要求") || strings.Contains(msg, "storyboard") {
		t.Fatalf("err=%s", msg)
	}
}

func TestExecuteEmptyModelFailsImmediately(t *testing.T) {
	sk := &fakeSkills{content: validCandidate}
	svc, conn := newTestService(t, testConfig(), sk, fakeModels{})
	res, err := conn.Exec(`INSERT INTO canvas_script_tasks (user_id, project_id, node_id, skill_name, duration_sec, script, model) VALUES (1, 7, 'n', 'real-person-short-drama-v3', 10, '剧本', '')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	task, _ := svc.claimOne(context.Background())
	svc.execute(context.Background(), task, testConfig())
	if status, msg, _ := taskRow(t, conn, id); status != StatusFailed || msg != "任务未记录模型，请重新提交" || len(sk.runs) != 0 {
		t.Fatalf("status=%s err=%s runs=%d", status, msg, len(sk.runs))
	}
}

func TestRecoverOnStartup(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	insert := func(attempts int) int64 {
		res, err := conn.Exec(`INSERT INTO canvas_script_tasks (user_id, project_id, node_id, skill_name, duration_sec, script, model, status, attempts, owner_instance, heartbeat_at)
			VALUES (1, 7, 'n', 'real-person-short-drama-v3', 10, '剧本', ?, 'running', ?, 'old-process', CURRENT_TIMESTAMP)`, testModel, attempts)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	retryable, exhausted := insert(1), insert(2)
	if err := svc.RecoverOnStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	var owner sql.NullString
	_ = conn.QueryRow(`SELECT owner_instance FROM canvas_script_tasks WHERE id=?`, retryable).Scan(&owner)
	if status, _, _ := taskRow(t, conn, retryable); status != StatusPending || owner.Valid {
		t.Fatalf("未用尽重试应回到 pending 并清租约: status=%s owner=%v", status, owner)
	}
	if status, msg, _ := taskRow(t, conn, exhausted); status != StatusFailed || !strings.Contains(msg, "上次运行中断") {
		t.Fatalf("用尽重试应判失败: status=%s err=%s", status, msg)
	}
}

func TestGetOnlyReturnsOwnTask(t *testing.T) {
	svc, _ := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	id := submitOne(t, svc)
	if _, err := svc.Get(context.Background(), 2, id); err == nil || err.Error() != "任务不存在" {
		t.Fatalf("err=%v", err)
	}
}

func TestTaskIDsNeverReused(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	first := submitOne(t, svc)
	if _, err := conn.Exec(`DELETE FROM canvas_script_tasks WHERE id=?`, first); err != nil {
		t.Fatal(err)
	}
	if second := submitOne(t, svc); second <= first {
		t.Fatalf("删除后 id 不应复用: first=%d second=%d", first, second)
	}
}

func TestUpdatedAtTrigger(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	id := submitOne(t, svc)
	if _, err := conn.Exec(`UPDATE canvas_script_tasks SET updated_at='2000-01-01 00:00:00' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE canvas_script_tasks SET status='running' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	var updated string
	_ = conn.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM canvas_script_tasks WHERE id=?`, id).Scan(&updated)
	if strings.HasPrefix(updated, "2000-") {
		t.Fatalf("状态变更应刷新 updated_at: %s", updated)
	}
}

func TestRunWorkerProcessesPendingTask(t *testing.T) {
	svc, _ := newTestService(t, testConfig(), &fakeSkills{content: validCandidate}, fakeModels{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.RunWorker(ctx)
	id := submitOne(t, svc)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.Get(context.Background(), 1, id)
		if err == nil && got.Status == StatusSucceeded {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("worker 应在一轮抢占内跑完任务")
}
