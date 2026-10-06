package generation

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// M3：outbox 首投失败进退避后，用户手动「保存到本地」成功（任务 output 已改成 media://），
// 重投必须用任务当前的产物，不能把节点改回上游外链。
func TestOutboxRedeliveryUsesCurrentTaskOutput(t *testing.T) {
	resetSinksForTest()
	t.Cleanup(resetSinksForTest)
	var got SinkResult
	RegisterSink("m3_test", func(_ context.Context, _ json.RawMessage, r SinkResult) error {
		got = r
		return nil
	})
	conn, svc, uid := newRecoverDBHarness(t)
	res, err := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, output, frozen_cents, sink, lease_version)
		VALUES (?, 'm', 'succeeded', '{}', '{"outputUrls":["media://u/gen/task-1-0"],"assetIds":[7],"upstreamUrls":["https://up/x.png"]}', 0, '{"kind":"m3_test"}', 1)`, uid)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload) VALUES (?, '{"kind":"m3_test"}', ?)`,
		id, `{"modelId":"m","outputUrls":["https://up/x.png"],"upstreamUrls":["https://up/x.png"],"assetIds":[0]}`); err != nil {
		t.Fatal(err)
	}
	svc.pumpOutboxOnce()
	if !reflect.DeepEqual(got.OutputURLs, []string{"media://u/gen/task-1-0"}) || !reflect.DeepEqual(got.AssetIDs, []int64{7}) {
		t.Fatalf("重投应使用任务当前 output, got urls=%v ids=%v", got.OutputURLs, got.AssetIDs)
	}
	if !reflect.DeepEqual(got.UpstreamURLs, []string{"https://up/x.png"}) {
		t.Fatalf("upstreamUrls 保持 payload 原值, got %v", got.UpstreamURLs)
	}
}

// 没有 repersist 过：output 与 payload 一致，照常投递原值；失败结果不读 output。
func TestOutboxRedeliveryFailurePayloadUnchanged(t *testing.T) {
	resetSinksForTest()
	t.Cleanup(resetSinksForTest)
	var got SinkResult
	RegisterSink("m3_test", func(_ context.Context, _ json.RawMessage, r SinkResult) error {
		got = r
		return nil
	})
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, lease_version, error)
		VALUES (?, 'm', 'failed', '{}', 0, '{"kind":"m3_test"}', 1, 'boom')`, uid)
	id, _ := res.LastInsertId()
	_, _ = conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload) VALUES (?, '{"kind":"m3_test"}', '{"err":"boom"}')`, id)
	svc.pumpOutboxOnce()
	if got.Err != "boom" || len(got.OutputURLs) != 0 {
		t.Fatalf("失败投递应原样, got %+v", got)
	}
}
