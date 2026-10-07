package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// 命令必须打到 /projects/{id}/nodes/{node}/compliance 与 /projects/{id}/compliance。
// 路径写错在本地看不出来——CLI 会照常打印一个 JSON，只是打的是别人的端点。
func TestComplianceVerifyHitsNodeScopedPath(t *testing.T) {
	var gotPath, gotMethod string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/nodes/resolve") {
			_, _ = w.Write([]byte(`{"nodeId":"n-1"}`))
			return
		}
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"nodeId":"n-1","kind":"image","status":"pending"}`))
	})

	if code := h.run("compliance", "verify", "n-1", "--project", "7"); code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, h.stderr.String())
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/cli/projects/7/nodes/n-1/compliance" {
		t.Fatalf("path = %s", gotPath)
	}
}

// 单个节点直接给对象，不要外面包一层数组：
// `miaoju compliance verify n-x | jq .status` 是最常见的用法。
func TestComplianceVerifySingleNodeEmitsObject(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/nodes/resolve") {
			_, _ = w.Write([]byte(`{"nodeId":"n-1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"nodeId":"n-1","kind":"image","status":"pending"}`))
	})
	if code := h.run("compliance", "verify", "n-1", "--project", "7"); code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, h.stderr.String())
	}
	var obj map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &obj); err != nil {
		t.Fatalf("单节点输出应是对象：%s", h.stdout.String())
	}
	if obj["status"] != "pending" {
		t.Fatalf("output = %s", h.stdout.String())
	}
}

// 多个节点逐个提交，输出成数组。
func TestComplianceVerifyMultipleNodesEmitsArray(t *testing.T) {
	posts := 0
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/nodes/resolve") {
			_, _ = w.Write([]byte(`{"nodeId":"` + r.URL.Query().Get("ref") + `"}`))
			return
		}
		posts++
		_, _ = w.Write([]byte(`{"nodeId":"x","kind":"image","status":"pending"}`))
	})
	if code := h.run("compliance", "verify", "n-1", "n-2", "--project", "7"); code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, h.stderr.String())
	}
	if posts != 2 {
		t.Fatalf("提交次数 = %d, want 2", posts)
	}
	var list []map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("多节点输出应是长度 2 的数组：%s", h.stdout.String())
	}
}

func TestComplianceStatusHitsProjectScopedPath(t *testing.T) {
	var gotPath, gotMethod string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`[{"nodeId":"n-1","kind":"image","status":"active"}]`))
	})
	if code := h.run("compliance", "status", "--project", "7"); code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, h.stderr.String())
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v1/cli/projects/7/compliance" {
		t.Fatalf("%s %s", gotMethod, gotPath)
	}
}

// 中途失败要停下并给非零退出码，不能把错误咽掉继续提交后面的。
func TestComplianceVerifyStopsOnError(t *testing.T) {
	posts := 0
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/nodes/resolve") {
			_, _ = w.Write([]byte(`{"nodeId":"n"}`))
			return
		}
		posts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"INVALID_NODE_TYPE","message":"没有可校验的媒体"}}`))
	})
	if code := h.run("compliance", "verify", "n-1", "n-2", "n-3", "--project", "7"); code == 0 {
		t.Fatal("失败时退出码不该是 0")
	}
	if posts != 1 {
		t.Fatalf("应在第一个失败后停下，实际提交 %d 次", posts)
	}
}
