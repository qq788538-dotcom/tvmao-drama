package generation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

func TestTaskJSONRoundTripsThroughText(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	pid := int64(3)
	in := `{"modelId":"m","inputs":{"prompt":"猫"}}`
	id, err := repo.Create(Task{UserID: 1, ProjectID: &pid, ModelID: "m", Status: "succeeded",
		Input: json.RawMessage(in), Sink: json.RawMessage(`{"kind":"canvas_node","nodeId":"n"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var inputType, sinkType string
	if err := conn.QueryRow(`SELECT typeof(input), typeof(sink) FROM generation_tasks WHERE id = ?`, id).Scan(&inputType, &sinkType); err != nil {
		t.Fatal(err)
	}
	if inputType != "text" || sinkType != "text" {
		t.Fatalf("JSON 必须以 TEXT 落库, input=%s sink=%s", inputType, sinkType)
	}
	if _, err := conn.Exec(`UPDATE generation_tasks SET output = '{"outputUrls":["media://k"]}' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(1, id)
	if err != nil {
		t.Fatalf("Get 读不回 TEXT 里的 JSON: %v", err)
	}
	if string(got.Input) != in || !strings.Contains(string(got.Output), "media://k") {
		t.Fatalf("input=%s output=%s", got.Input, got.Output)
	}
	list, total, err := repo.List(ListFilter{UserID: 1})
	if err != nil || total != 1 || string(list[0].Input) != in {
		t.Fatalf("List: total=%d err=%v", total, err)
	}
	sink, err := repo.SinkForTask(id)
	if err != nil || !strings.Contains(string(sink), `"nodeId":"n"`) {
		t.Fatalf("SinkForTask: %s %v", sink, err)
	}
}
