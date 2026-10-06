package generation_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/billing"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

// SubmitTx 的事务不变量在真实 SQLite 上验证：建行与冻结同事务；冻结失败整体回滚、不留任务行。
// 桌面端计费是空实现，这里用可注入失败的替身钉住「冻结失败 → 回滚」这条结构性保证。

type txTestModels struct {
	price      int64
	genControl string
}

func (m txTestModels) GetByModelID(modelID string) (*models.ModelConfig, error) {
	raw, _ := json.Marshal(map[string]any{"flatFee": m.price})
	mc := &models.ModelConfig{ModelID: modelID, Enabled: true, BillingConfigRaw: raw}
	if m.genControl != "" {
		mc.GenControlRaw = json.RawMessage(m.genControl)
	}
	return mc, nil
}

type txTestBilling struct {
	freezeErr error
	freezes   *[]int64
}

func (b txTestBilling) Freeze(context.Context, int64, int64, int64, string) (generation.BillingResult, error) {
	return generation.BillingResult{}, nil
}
func (b txTestBilling) Settle(context.Context, int64, int64, int64, int64, string) (generation.BillingResult, error) {
	return generation.BillingResult{}, nil
}
func (b txTestBilling) Refund(context.Context, int64, int64, int64, string) (generation.BillingResult, error) {
	return generation.BillingResult{}, nil
}
func (b txTestBilling) FreezeTx(_ context.Context, _ *sql.Tx, _, _, a int64, _ string) (generation.BillingResult, error) {
	*b.freezes = append(*b.freezes, a)
	return generation.BillingResult{}, b.freezeErr
}
func (b txTestBilling) SettleTx(context.Context, *sql.Tx, int64, int64, int64, int64, string) (generation.BillingResult, error) {
	return generation.BillingResult{}, nil
}
func (b txTestBilling) RefundTx(context.Context, *sql.Tx, int64, int64, int64, string) (generation.BillingResult, error) {
	return generation.BillingResult{}, nil
}

const uid = int64(1)

func newSubmitTxHarness(t *testing.T, ml generation.ModelLookup, freezeErr error, st generation.AssetStorage, ar generation.AssetRecorder) (*sql.DB, *generation.Service, *[]int64) {
	t.Helper()
	conn := dbtest.Open(t)
	freezes := &[]int64{}
	svc := generation.NewService(
		generation.NewSQLiteRepository(conn), ml, nil, nil,
		txTestBilling{freezeErr: freezeErr, freezes: freezes},
		nil, st, ar, nil, nil, conn,
	)
	return conn, svc, freezes
}

func TestSubmitTxCommitCreatesRowAndFreeze(t *testing.T) {
	conn, svc, freezes := newSubmitTxHarness(t, txTestModels{price: 30}, nil, nil, nil)
	tx, _ := conn.BeginTx(context.Background(), nil)
	r, err := svc.SubmitTx(context.Background(), tx, generation.SubmitInput{
		UserID: uid, ModelID: "m-fixed",
		Inputs: map[string]any{"prompt": "画只猫"},
		Sink:   json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n1"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var status, inputType, sinkType string
	var frozen int64
	if err := conn.QueryRow(`SELECT status, frozen_cents, typeof(input), typeof(sink) FROM generation_tasks WHERE id=?`, r.TaskID).
		Scan(&status, &frozen, &inputType, &sinkType); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if status != "queued" || frozen != 30 || inputType != "text" || sinkType != "text" {
		t.Fatalf("got status=%s frozen=%d input=%s sink=%s", status, frozen, inputType, sinkType)
	}
	if len(*freezes) != 1 || (*freezes)[0] != 30 {
		t.Fatalf("应在同一事务内冻结一次 30, got %v", *freezes)
	}
}

func TestSubmitTxFreezeErrorRollsBackEverything(t *testing.T) {
	conn, svc, _ := newSubmitTxHarness(t, txTestModels{price: 500}, billing.ErrInsufficientBalance, nil, nil)
	tx, _ := conn.BeginTx(context.Background(), nil)
	_, err := svc.SubmitTx(context.Background(), tx, generation.SubmitInput{
		UserID: uid, ModelID: "m-fixed",
		Inputs: map[string]any{"prompt": "太贵了"},
		Sink:   json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n2"}`),
	})
	if !errors.Is(err, billing.ErrInsufficientBalance) {
		t.Fatalf("冻结错误应原样透传, got %v", err)
	}
	_ = tx.Rollback()
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE user_id=?`, uid).Scan(&n)
	if n != 0 {
		t.Fatalf("回滚后任务行不该存在, got %d 行", n)
	}
}

func TestSubmitTxRejectsSinkless(t *testing.T) {
	conn, svc, _ := newSubmitTxHarness(t, txTestModels{price: 1}, nil, nil, nil)
	tx, _ := conn.BeginTx(context.Background(), nil)
	defer tx.Rollback()
	if _, err := svc.SubmitTx(context.Background(), tx, generation.SubmitInput{
		UserID: uid, ModelID: "m", Inputs: map[string]any{},
	}); err == nil {
		t.Fatal("sinkless 必须被拒——它是同步语义,与队列执行不兼容")
	}
}

type durStorage struct{ generation.AssetStorage }

func (durStorage) ExtractKey(s string) (string, bool) { return "u1/v", s != "" }

type durAssets struct {
	generation.AssetRecorder
	setCalled bool
}

func (*durAssets) GetDurationMsByObjectKey(string) (int64, bool, error) { return 0, false, nil }
func (d *durAssets) SetDurationMsByObjectKey(string, int64) error {
	d.setCalled = true
	return nil
}

// Review Focus 5：带 inputDuration 的模型走 sink 路径时，resolveInputDuration 在写事务里执行。
// 任何经 r.db 的写都会让本次提交卡 10 秒后 SQLITE_BUSY。
func TestSubmitWithInputDurationModelDoesNotSelfDeadlock(t *testing.T) {
	as := &durAssets{}
	conn, svc, _ := newSubmitTxHarness(t,
		txTestModels{price: 1, genControl: `{"inputDuration":{"from":"video"}}`}, nil, durStorage{}, as)
	done := make(chan error, 1)
	go func() {
		_, err := svc.Submit(context.Background(), generation.SubmitInput{
			UserID: uid, ModelID: "m-edit",
			Inputs: map[string]any{"prompt": "x", "videos": []any{"media://u1/v"}},
			Sink:   json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n3"}`),
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("提交超过 5 秒没有返回：写事务内经 r.db 写库导致自死锁")
	}
	if as.setCalled {
		t.Fatal("不得在提交路径回填 duration_ms")
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE status='queued'`).Scan(&n)
	if n != 1 {
		t.Fatalf("应有 1 条 queued 任务, got %d", n)
	}
}
