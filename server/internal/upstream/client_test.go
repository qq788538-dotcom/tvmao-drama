package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestExtractCostCents(t *testing.T) {
	got := ExtractCostCents(1.23)
	if got != 123 {
		t.Fatalf("expected 123, got %d", got)
	}
}

// 音频上游（豆包 /v1/audio/speech）同步返回顶层 audio_url——曾经不认识这个键，
// OutputURLs 为空导致「生成成功但节点报“上游未返回结果”」（测试环境 task=805）。
func TestExtractFields_TopLevelAudioURL(t *testing.T) {
	var r CallResult
	ExtractFields([]byte(`{"audio_url":"https://cdn.example/a.mp3","duration":4.58}`), &r)
	if len(r.OutputURLs) != 1 || r.OutputURLs[0] != "https://cdn.example/a.mp3" {
		t.Fatalf("OutputURLs = %v", r.OutputURLs)
	}

	// 轮询路径按 urlField 先抠过一次再进 ExtractFields——urlField 恰好配成
	// audio_url 时必须去重，否则 persistOutputs 会存两份产物。
	r2 := CallResult{OutputURLs: []string{"https://cdn.example/a.mp3"}}
	ExtractFields([]byte(`{"audio_url":"https://cdn.example/a.mp3"}`), &r2)
	if len(r2.OutputURLs) != 1 {
		t.Fatalf("重复 audio_url 未去重: %v", r2.OutputURLs)
	}
}

func mustInterpolate(t *testing.T, template string, inputs map[string]any) any {
	t.Helper()
	raw, err := InterpolateTemplate(json.RawMessage(template), inputs)
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestInterpolate_Map_GoStringSlice(t *testing.T) {
	// 回归 Bug：drama 的 images 是 Go 原生 []string（非 JSON 解码出来的 []any）。
	// $map 必须能展开它——否则 evalMap 类型断言失败、当成空数组，content 渲染成 []，参考图全丢。
	got := mustInterpolate(t,
		`{"content":{"$map":"images","$as":"u","$template":{"url":"{{u}}"}}}`,
		map[string]any{"images": []string{"asset://a", "https://b.png"}})
	want := map[string]any{"content": []any{
		map[string]any{"url": "asset://a"},
		map[string]any{"url": "https://b.png"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got=%#v\nwant=%#v", got, want)
	}
}

func TestInterpolate_SimpleReplace(t *testing.T) {
	got := mustInterpolate(t, `{"prompt": "{{prompt}}"}`, map[string]any{"prompt": "hello"})
	want := map[string]any{"prompt": "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInterpolate_WholeArrayReplace(t *testing.T) {
	got := mustInterpolate(t, `{"image": "{{images}}"}`, map[string]any{
		"images": []any{"a", "b", "c"},
	})
	want := map[string]any{"image": []any{"a", "b", "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInterpolate_IndexAccess(t *testing.T) {
	got := mustInterpolate(t, `{"first": "{{images[0]}}", "second": "{{images[1]}}"}`, map[string]any{
		"images": []any{"x", "y"},
	})
	want := map[string]any{"first": "x", "second": "y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInterpolate_IndexAccess_GoStringSlice(t *testing.T) {
	// 回归 Bug：生成链路的 images 是 Go 原生 []string（image_inputs.go 直接塞，渲染前无 JSON 往返）。
	// 下标访问必须能索引它——否则 walkPath 的 .([]any) 断言失败返回 nil，{{images[0]}} 全哑。
	got := mustInterpolate(t, `{"first": "{{images[0]}}", "second": "{{images[1]}}"}`, map[string]any{
		"images": []string{"x", "y"},
	})
	want := map[string]any{"first": "x", "second": "y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

// TestInterpolate_CapImagesByIndexIf 覆盖「按下标嵌套 $if + $concat 封顶 N 张、不足不塞 null」
// 这个模板配法（image_inputs.go 的 images 是 []string）。
func TestInterpolate_CapImagesByIndexIf(t *testing.T) {
	const tmpl = `{"image":{"$if":"images","$else":null,"$then":{"$concat":[` +
		`{"$if":"images[0]","$then":["{{images[0]}}"],"$else":[]},` +
		`{"$if":"images[1]","$then":["{{images[1]}}"],"$else":[]}]}}}`
	cases := []struct {
		name string
		imgs []string
		want any
	}{
		{"两张原样", []string{"a", "b"}, []any{"a", "b"}},
		{"三张封顶两张", []string{"a", "b", "c"}, []any{"a", "b"}},
		{"一张只出一张不塞null", []string{"a"}, []any{"a"}},
		{"零张出null", []string{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := map[string]any{}
			if len(c.imgs) > 0 {
				in["images"] = c.imgs
			}
			got := mustInterpolate(t, tmpl, in)
			want := map[string]any{"image": c.want}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v want %#v", got, want)
			}
		})
	}
}

func TestInterpolate_Map(t *testing.T) {
	template := `{
		"parts": {
			"$map": "images",
			"$as": "u",
			"$template": { "type": "image_url", "image_url": { "url": "{{u}}" } }
		}
	}`
	got := mustInterpolate(t, template, map[string]any{
		"images": []any{"a.png", "b.png"},
	})
	want := map[string]any{
		"parts": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "a.png"}},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "b.png"}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInterpolate_Concat(t *testing.T) {
	template := `{
		"content": {
			"$concat": [
				[{ "type": "text", "text": "{{prompt}}" }],
				{ "$map": "images", "$as": "u", "$template": { "type": "image_url", "url": "{{u}}" } }
			]
		}
	}`
	got := mustInterpolate(t, template, map[string]any{
		"prompt": "hi",
		"images": []any{"a", "b"},
	})
	want := map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "hi"},
			map[string]any{"type": "image_url", "url": "a"},
			map[string]any{"type": "image_url", "url": "b"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestInterpolate_If(t *testing.T) {
	template := `{
		"image": { "$if": "images", "$then": "{{images}}", "$else": null }
	}`
	// 真值
	got := mustInterpolate(t, template, map[string]any{"images": []any{"a"}})
	if arr, ok := got.(map[string]any)["image"].([]any); !ok || len(arr) != 1 || arr[0] != "a" {
		t.Fatalf("truthy branch failed: %v", got)
	}
	// 假值（空数组）
	got = mustInterpolate(t, template, map[string]any{"images": []any{}})
	if got.(map[string]any)["image"] != nil {
		t.Fatalf("falsy branch failed: %v", got)
	}
}

func TestInterpolate_EmptyTemplate(t *testing.T) {
	got := mustInterpolate(t, `{}`, map[string]any{"x": 1})
	want := map[string]any{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestExtractFields_ChatMarkdownImage(t *testing.T) {
	raw := []byte(`{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "![image](data:image/jpeg;base64,/9j/4AAQABCD==)"
			}
		}]
	}`)
	result := &CallResult{}
	ExtractFields(raw, result)
	if len(result.OutputURLs) != 1 {
		t.Fatalf("expected 1 url, got %d: %v", len(result.OutputURLs), result.OutputURLs)
	}
	if !strings.HasPrefix(result.OutputURLs[0], "data:image/jpeg;base64,") {
		t.Fatalf("unexpected url: %s", result.OutputURLs[0])
	}
}

func TestExtractFields_ChatContentArray(t *testing.T) {
	raw := []byte(`{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": [
					{"type": "text", "text": "here you go"},
					{"type": "image_url", "image_url": {"url": "https://example.com/a.png"}}
				]
			}
		}]
	}`)
	result := &CallResult{}
	ExtractFields(raw, result)
	if len(result.OutputURLs) != 1 || result.OutputURLs[0] != "https://example.com/a.png" {
		t.Fatalf("unexpected output urls: %v", result.OutputURLs)
	}
}

func TestExtractFields_GeminiNative(t *testing.T) {
	raw := []byte(`{
		"candidates": [{
			"content": {
				"parts": [
					{"text": "ok"},
					{"inline_data": {"mime_type": "image/png", "data": "iVBOR=="}}
				]
			}
		}]
	}`)
	result := &CallResult{}
	ExtractFields(raw, result)
	if len(result.OutputURLs) != 1 || !strings.HasPrefix(result.OutputURLs[0], "data:image/png;base64,") {
		t.Fatalf("unexpected: %v", result.OutputURLs)
	}
}

func TestExtractFields_GeminiNativeInlineDataCamelCase(t *testing.T) {
	raw := []byte(`{
		"candidates": [{
			"content": {
				"parts": [
					{"text": ""},
					{"inlineData": {"mimeType": "image/jpeg", "data": "/9j/4AAQ=="}}
				]
			}
		}]
	}`)
	result := &CallResult{}
	ExtractFields(raw, result)
	if len(result.OutputURLs) != 1 || !strings.HasPrefix(result.OutputURLs[0], "data:image/jpeg;base64,") {
		t.Fatalf("unexpected: %v", result.OutputURLs)
	}
}

func TestExtractFields_B64Json(t *testing.T) {
	raw := []byte(`{"data": [{"b64_json": "iVBOR=="}]}`)
	result := &CallResult{}
	ExtractFields(raw, result)
	if len(result.OutputURLs) != 1 || !strings.HasPrefix(result.OutputURLs[0], "data:image/png;base64,") {
		t.Fatalf("unexpected: %v", result.OutputURLs)
	}
}

func TestCallMultipartUploadsMultipleImages(t *testing.T) {
	var seenAuth, seenPrompt string
	var seenFiles []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		seenAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		seenPrompt = r.FormValue("prompt")
		for _, fh := range r.MultipartForm.File["image"] {
			seenFiles = append(seenFiles, fh.Filename)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"https://cdn.example/out.png"}]}`))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	client := NewClient(ClientOptions{BaseURL: srv.URL, TempDir: tmp})
	result, err := client.CallMultipart(
		context.Background(),
		"sk-test",
		http.MethodPost,
		"/v1/images/edits",
		map[string]string{"model": "gpt-image-1", "prompt": "edit this"},
		[]MultipartFile{
			{FieldName: "image", Filename: "a.png", ContentType: "image/png", Path: writeTemp(t, tmp, "aaa")},
			{FieldName: "image", Filename: "b.png", ContentType: "image/png", Path: writeTemp(t, tmp, "bbb")},
		},
	)
	if err != nil {
		t.Fatalf("CallMultipart: %v", err)
	}
	if seenAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", seenAuth)
	}
	if seenPrompt != "edit this" {
		t.Fatalf("prompt = %q", seenPrompt)
	}
	if !reflect.DeepEqual(seenFiles, []string{"a.png", "b.png"}) {
		t.Fatalf("files = %v", seenFiles)
	}
	if len(result.OutputURLs) != 1 || result.OutputURLs[0] != "https://cdn.example/out.png" {
		t.Fatalf("unexpected output urls: %v", result.OutputURLs)
	}
}

func TestCall_HTTP429ReturnsTemporaryCallError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"busy"}`))
	}))
	defer srv.Close()

	_, err := NewClient(ClientOptions{BaseURL: srv.URL}).Call(context.Background(), "sk-test", http.MethodGet, "/tasks/1", nil)
	if err == nil {
		t.Fatal("Call error = nil, want 429 error")
	}
	if !IsTemporaryError(err) {
		t.Fatalf("err = %v, want temporary classification", err)
	}
}

func TestCall_HTTP400ReturnsPermanentCallError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad prompt"}`))
	}))
	defer srv.Close()

	_, err := NewClient(ClientOptions{BaseURL: srv.URL}).Call(context.Background(), "sk-test", http.MethodGet, "/tasks/1", nil)
	if err == nil {
		t.Fatal("Call error = nil, want 400 error")
	}
	if IsTemporaryError(err) {
		t.Fatalf("err = %v, want permanent classification", err)
	}
}

func TestIsTemporaryError_ClassifiesNetworkTimeouts(t *testing.T) {
	err := &CallError{Err: timeoutNetError{}}
	if !IsTemporaryError(err) {
		t.Fatalf("err = %v, want timeout to be temporary", err)
	}
	if IsTemporaryError(errors.New("wrap: " + err.Error())) {
		t.Fatal("plain string wrapper should not become temporary")
	}
	if !IsTemporaryError(&CallError{Err: context.DeadlineExceeded}) {
		t.Fatal("context deadline should be temporary when propagated as transport error")
	}
}

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }
func (timeoutNetError) Unwrap() error   { return net.UnknownNetworkError("tcp") }

// 纯文本 chat 响应（VL 反推提示词等文本生成任务）：正文进 Text，不产生 OutputURLs。
func TestExtractFields_ChatPlainText(t *testing.T) {
	raw := []byte(`{"choices":[{"message":{"role":"assistant","content":"# 提示词\n\n一段描述"}}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`)
	var result CallResult
	ExtractFields(raw, &result)
	if result.Text != "# 提示词\n\n一段描述" {
		t.Fatalf("Text = %q", result.Text)
	}
	if len(result.OutputURLs) != 0 {
		t.Fatalf("plain text must not yield OutputURLs: %v", result.OutputURLs)
	}
	if result.Usage == nil || result.Usage.TotalTokens != 30 {
		t.Fatalf("usage not extracted: %+v", result.Usage)
	}
}

// 多模态分片响应：拼接所有 text 片段。
func TestExtractFields_ChatTextParts(t *testing.T) {
	raw := []byte(`{"choices":[{"message":{"content":[{"type":"text","text":"第一段"},{"type":"text","text":"第二段"}]}}]}`)
	var result CallResult
	ExtractFields(raw, &result)
	if result.Text != "第一段\n第二段" {
		t.Fatalf("Text = %q", result.Text)
	}
}

// 带图响应（gemini-image 这类 chat 生图）：图片照抽，正文也保留——
// 是否采用 Text 由 finalize 按「无媒体产物」判定，抽取层不做取舍。
func TestExtractFields_ChatTextWithImageKeepsBoth(t *testing.T) {
	raw := []byte(`{"choices":[{"message":{"content":"这是你要的图 ![img](https://cdn.example/a.png)"}}]}`)
	var result CallResult
	ExtractFields(raw, &result)
	if len(result.OutputURLs) != 1 || result.OutputURLs[0] != "https://cdn.example/a.png" {
		t.Fatalf("OutputURLs = %v", result.OutputURLs)
	}
	if result.Text == "" {
		t.Fatalf("Text should still carry the caption")
	}
}
