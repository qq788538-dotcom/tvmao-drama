package generation

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/lifecycle"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

// 生成名额只管「上游在途」：上游返回（或轮询返回）后立刻归还，下载落盘只占下载闸（P20）。

func insertDirectRunning(t *testing.T, conn *sql.DB, uid int64, modelID string) int64 {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, owner_instance, heartbeat_at, lease_version)
		VALUES (?, ?, 'running', '{}', 40, 'me', CURRENT_TIMESTAMP, 1)`, uid, modelID)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// blockingPNGServer 收到请求时往 hit 发信号，调用 release 之后才回 PNG（release 可重复调用）。
func blockingPNGServer(t *testing.T) (srv *httptest.Server, hit chan struct{}, release func()) {
	t.Helper()
	hit = make(chan struct{}, 4)
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- struct{}{}
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...))
	}))
	t.Cleanup(func() {
		release()
		srv.Close()
	})
	return srv, hit, release
}

func waitHit(t *testing.T, hit chan struct{}) {
	t.Helper()
	select {
	case <-hit:
	case <-time.After(10 * time.Second):
		t.Fatal("下载没有开始")
	}
}

func TestCallAndCompleteReleasesSlotBeforeDownload(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	svc.concurrency = NewConcurrencyRegistry()
	svc.storage = &captureStorage{}
	svc.httpClient = http.DefaultClient
	svc.tmpDir = t.TempDir()

	dl, hit, release := blockingPNGServer(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"` + dl.URL + `/a.png"}]}`))
	}))
	t.Cleanup(up.Close)

	id := insertDirectRunning(t, conn, uid, "m-rel")
	model := &models.ModelConfig{ModelID: "m-rel", BasePath: "/gen", GenControlRaw: json.RawMessage(`{"concurrency":1}`)}
	done := make(chan error, 1)
	go func() {
		_, err := svc.callAndComplete(context.Background(), id, 1, model, "k", up.URL, []byte(`{}`), uid, nil, 40, nil, nil, nil, "")
		done <- err
	}()

	waitHit(t, hit)
	if got := activeOf(svc.concurrency, "m-rel"); got != 0 {
		t.Fatalf("下载期间不应再占生成名额, active=%d", got)
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("callAndComplete: %v", err)
	}
	if status, _ := taskState(t, conn, id); status != "succeeded" {
		t.Fatalf("status=%s", status)
	}
}

func TestCompleteAsyncReleasesSlotWhenPollReturns(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	svc.concurrency = NewConcurrencyRegistry()
	svc.storage = &lockedCaptureStorage{}
	svc.httpClient = http.DefaultClient
	svc.tmpDir = t.TempDir()
	tracker := &lifecycle.Tracker{}
	svc.taskTracker = tracker

	dl, hit, release := blockingPNGServer(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"task_id":"up-1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"completed","url":"` + dl.URL + `/v.png"}`))
	}))
	t.Cleanup(up.Close)

	id := insertDirectRunning(t, conn, uid, "m-async")
	model := &models.ModelConfig{ModelID: "m-async", BasePath: "/gen", GenControlRaw: json.RawMessage(`{"concurrency":1}`),
		PollingTemplate: json.RawMessage(`{"path":"/t/{{task_id}}","interval":1,"maxWait":60,"urlField":"url"}`)}
	res, err := svc.callAndComplete(context.Background(), id, 1, model, "k", up.URL, []byte(`{}`), uid, nil, 40, nil, nil, nil, "")
	if err != nil || res.Status != "processing" {
		t.Fatalf("应转入轮询: res=%+v err=%v", res, err)
	}
	waitHit(t, hit)
	if got := activeOf(svc.concurrency, "m-async"); got != 0 {
		t.Fatalf("轮询返回后应归还生成名额, active=%d", got)
	}
	release()
	if !tracker.Wait(10 * time.Second) {
		t.Fatal("completeAsync 未结束")
	}
	if status, _ := taskState(t, conn, id); status != "succeeded" {
		t.Fatalf("status=%s", status)
	}
}

// 等名额期间进程开始排空：放弃等待、不动钱、行留在 running 给下次启动接管（P29）。
func TestCallAndCompleteSlotWaitAbortsOnDrain(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	svc.concurrency = NewConcurrencyRegistry()
	tracker := &lifecycle.Tracker{}
	svc.taskTracker = tracker

	id := insertDirectRunning(t, conn, uid, "m-drain")
	model := &models.ModelConfig{ModelID: "m-drain", GenControlRaw: json.RawMessage(`{"concurrency":1}`)}
	hold := svc.concurrency.Acquire(model.ModelID, model.GenConcurrency())
	defer hold()

	done := make(chan error, 1)
	go func() {
		_, err := svc.callAndComplete(context.Background(), id, 1, model, "k", "http://127.0.0.1:1", []byte(`{}`), uid, nil, 40, nil, nil, nil, "")
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	tracker.BeginDrain()
	select {
	case err := <-done:
		if !errors.Is(err, errSlotWaitAborted) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("排空时等名额的任务应及时放弃")
	}
	if status, lease := taskState(t, conn, id); status != "running" || lease != 1 {
		t.Fatalf("放弃等名额不应收口: status=%s lease=%d", status, lease)
	}
	assertOps(t, svc)
	if got := activeOf(svc.concurrency, model.ModelID); got != 1 {
		t.Fatalf("放弃者不应占名额, active=%d", got)
	}
}

// 等名额期间失租：放弃等待、静默退出，不退款（新持有者负责）。
func TestCallAndCompleteSlotWaitAbortsOnLeaseLost(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	svc.concurrency = NewConcurrencyRegistry()
	svc.leaseHeartbeatSec = 1

	id := insertDirectRunning(t, conn, uid, "m-lost")
	model := &models.ModelConfig{ModelID: "m-lost", GenControlRaw: json.RawMessage(`{"concurrency":1}`)}
	hold := svc.concurrency.Acquire(model.ModelID, model.GenConcurrency())
	defer hold()

	done := make(chan error, 1)
	go func() {
		_, err := svc.callAndComplete(context.Background(), id, 1, model, "k", "http://127.0.0.1:1", []byte(`{}`), uid, nil, 40, nil, nil, nil, "")
		done <- err
	}()
	if _, err := conn.Exec(`UPDATE generation_tasks SET lease_version = 2 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errSlotWaitAborted) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("失租后等名额的任务应及时放弃")
	}
	assertOps(t, svc)
}

// 等名额花掉的时间不计入执行 ctx 的总时限：拿到名额时原 ctx 已过期，任务仍应正常完成，
// 而不是调上游立刻失败退款（P29「30 分钟总超时不触发退款失败」）。
func TestCallAndCompleteSlotWaitDoesNotConsumeDeadline(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	svc.concurrency = NewConcurrencyRegistry()
	svc.storage = &captureStorage{}
	svc.tmpDir = t.TempDir()

	png := base64.StdEncoding.EncodeToString(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...))
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + png + `"}]}`))
	}))
	t.Cleanup(up.Close)

	id := insertDirectRunning(t, conn, uid, "m-dl")
	model := &models.ModelConfig{ModelID: "m-dl", BasePath: "/gen", GenControlRaw: json.RawMessage(`{"concurrency":1}`)}
	hold := svc.concurrency.Acquire(model.ModelID, model.GenConcurrency())

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := svc.callAndComplete(ctx, id, 1, model, "k", up.URL, []byte(`{}`), uid, nil, 40, nil, nil, nil, "")
		done <- err
	}()
	time.Sleep(400 * time.Millisecond) // 原 ctx 已过期
	hold()
	if err := <-done; err != nil {
		t.Fatalf("排名额超过总时限不应判失败: %v", err)
	}
	if status, _ := taskState(t, conn, id); status != "succeeded" {
		t.Fatalf("status=%s", status)
	}
}

// 排名额排过原时限后再失租：顺延后的 ctx 仍要跟着失租取消，在途的上游请求立刻中止。
// 父 ctx 已因到期结束，extendDeadline 转发不到之后的失租，必须由 lost 直接取消。
func TestCallAndCompleteLeaseLostAfterOriginalDeadlineCancelsUpstream(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	svc.concurrency = NewConcurrencyRegistry()
	svc.leaseHeartbeatSec = 1

	hit := make(chan struct{}, 1)
	gate := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- struct{}{}
		<-gate // 上游一直不回，直到测试结束
	}))
	t.Cleanup(func() {
		close(gate)
		up.Close()
	})

	id := insertDirectRunning(t, conn, uid, "m-lost-ext")
	model := &models.ModelConfig{ModelID: "m-lost-ext", BasePath: "/gen", GenControlRaw: json.RawMessage(`{"concurrency":1}`)}
	hold := svc.concurrency.Acquire(model.ModelID, model.GenConcurrency())

	// 顺延后拿到名额时还剩 4 秒：失租（心跳 1 秒一拍）应在这之前就把请求取消。
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := svc.callAndComplete(ctx, id, 1, model, "k", up.URL, []byte(`{}`), uid, nil, 40, nil, nil, nil, "")
		done <- err
	}()
	time.Sleep(4300 * time.Millisecond) // 原 ctx 已过期
	hold()
	waitHit(t, hit)
	if _, err := conn.Exec(`UPDATE generation_tasks SET lease_version = 2 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("失租后在途的上游请求应被取消、callAndComplete 应返回")
	}
}

// completeAsync 用提交时的原始 inputs 结算（与续跑路径同形），执行用的 inputs（参考图已内联
// base64）不随轮询常驻（P19）。用一条只看执行 inputs 才命中的附加计费规则区分两者。
func TestCompleteAsyncSettlesWithSubmittedInputs(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	svc.concurrency = NewConcurrencyRegistry()
	tracker := &lifecycle.Tracker{}
	svc.taskTracker = tracker

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"task_id":"up-2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"completed"}`))
	}))
	t.Cleanup(up.Close)

	id := insertDirectRunning(t, conn, uid, "m-p19")
	model := &models.ModelConfig{ModelID: "m-p19", BasePath: "/gen",
		BillingConfigRaw: json.RawMessage(`{"flatFee":40,"extras":[{"when":"req:inputs.execOnly","op":"hasValue","factor":2}]}`),
		PollingTemplate:  json.RawMessage(`{"path":"/t/{{task_id}}","interval":1,"maxWait":60}`)}
	execInputs := map[string]any{"execOnly": "x", "image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, 8192))}
	reqInputs := map[string]any{"prompt": "p"}
	if _, err := svc.callAndComplete(context.Background(), id, 1, model, "k", up.URL, []byte(`{}`), uid, nil, 40, execInputs, reqInputs, nil, ""); err != nil {
		t.Fatal(err)
	}
	if !tracker.Wait(10 * time.Second) {
		t.Fatal("completeAsync 未结束")
	}
	var settled *billingCall
	for _, c := range spyOf(svc).ops() {
		if c.Op == "SettleTx" {
			c := c
			settled = &c
		}
	}
	if settled == nil || settled.Actual != 40 {
		t.Fatalf("应按提交时 inputs 结算 40, got %+v", settled)
	}
}
