package upstream

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

var b64Keys = map[string]bool{"b64_json": true}

func png(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func TestSpill_Table(t *testing.T) {
	img1, img2 := png(3000), png(4001)
	e := base64.StdEncoding.EncodeToString
	cases := []struct {
		name     string
		in       string
		wantJSON string
		wantImgs [][]byte
		wantErr  bool
	}{
		{name: "empty", in: ``, wantJSON: ``},
		{name: "no key", in: `{"data":[{"url":"https://x/1.png"}],"usage":{"total_tokens":3}}`,
			wantJSON: `{"data":[{"url":"https://x/1.png"}],"usage":{"total_tokens":3}}`},
		{name: "single", in: `{"created":1,"data":[{"b64_json":"` + e(img1) + `"}],"usage":{"total_tokens":515}}`,
			wantJSON: `{"created":1,"data":[{"b64_json":"@spill:0"}],"usage":{"total_tokens":515}}`, wantImgs: [][]byte{img1}},
		{name: "multiple", in: `{"data":[{"b64_json":"` + e(img1) + `"},{"revised_prompt":"x","b64_json":"` + e(img2) + `"}]}`,
			wantJSON: `{"data":[{"b64_json":"@spill:0"},{"revised_prompt":"x","b64_json":"@spill:1"}]}`, wantImgs: [][]byte{img1, img2}},
		{name: "escaped slash", in: `{"data":[{"b64_json":"` + strings.ReplaceAll(e(img1), "/", `\/`) + `"}]}`,
			wantJSON: `{"data":[{"b64_json":"@spill:0"}]}`, wantImgs: [][]byte{img1}},
		{name: "key name inside other string value", in: `{"revised_prompt":"say \"b64_json\": ok","data":[{"b64_json":"` + e(img1) + `"}]}`,
			wantJSON: `{"revised_prompt":"say \"b64_json\": ok","data":[{"b64_json":"@spill:0"}]}`, wantImgs: [][]byte{img1}},
		{name: "unicode elsewhere", in: `{"revised_prompt":"一只猫","data":[{"b64_json":"` + e(img1) + `"}]}`,
			wantJSON: `{"revised_prompt":"一只猫","data":[{"b64_json":"@spill:0"}]}`, wantImgs: [][]byte{img1}},
		{name: "whitespace around colon", in: `{"data":[{"b64_json" : "` + e(img1) + `"}]}`,
			wantJSON: `{"data":[{"b64_json" : "@spill:0"}]}`, wantImgs: [][]byte{img1}},
		{name: "error json", in: `{"error":{"message":"Model \"gpt-image-2\" is not supported","type":"x"}}`,
			wantJSON: `{"error":{"message":"Model \"gpt-image-2\" is not supported","type":"x"}}`},
		{name: "key with null value", in: `{"b64_json":null,"data":[]}`, wantJSON: `{"b64_json":null,"data":[]}`},
		{name: "empty b64 string", in: `{"data":[{"b64_json":""}]}`, wantJSON: `{"data":[{"b64_json":"@spill:0"}]}`, wantImgs: [][]byte{{}}},
		{name: "truncated inside b64", in: `{"data":[{"b64_json":"` + e(img1)[:100], wantErr: true},
		{name: "invalid base64", in: `{"data":[{"b64_json":"@@@@"}]}`, wantErr: true},
		{name: "raw std no padding tail", in: `{"data":[{"b64_json":"` + base64.RawStdEncoding.EncodeToString(img1) + `"}]}`,
			wantJSON: `{"data":[{"b64_json":"@spill:0"}]}`, wantImgs: [][]byte{img1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			res, err := spillLargeStrings(strings.NewReader(tc.in), b64Keys, dir)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if left, _ := os.ReadDir(dir); len(left) != 0 {
					t.Fatalf("出错时不能残留临时文件: %d", len(left))
				}
				return
			}
			if err != nil {
				t.Fatalf("spill: %v", err)
			}
			if string(res.JSON) != tc.wantJSON {
				t.Fatalf("JSON=\n%s\nwant\n%s", res.JSON, tc.wantJSON)
			}
			if len(res.Files) != len(tc.wantImgs) {
				t.Fatalf("files=%d want %d", len(res.Files), len(tc.wantImgs))
			}
			for i, want := range tc.wantImgs {
				got, _ := os.ReadFile(res.Files[i].Path)
				if !bytes.Equal(got, want) || res.Files[i].Size != int64(len(want)) {
					t.Fatalf("file %d mismatch: len=%d size=%d want %d", i, len(got), res.Files[i].Size, len(want))
				}
			}
			if tc.wantJSON != "" {
				var v any
				if err := json.Unmarshal(res.JSON, &v); err != nil {
					t.Fatalf("剩余 JSON 必须仍可解析: %v", err)
				}
			}
		})
	}
}

// 大图跨多个 64KB 解码块,确认分块解码与整段解码一致。
func TestSpill_LargeCrossesChunks(t *testing.T) {
	img := png(500_000)
	in := `{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(img) + `"}]}`
	res, err := spillLargeStrings(strings.NewReader(in), b64Keys, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(res.Files[0].Path)
	if !bytes.Equal(got, img) {
		t.Fatalf("分块解码结果不一致 len=%d", len(got))
	}
}

func TestExtractFields_SpillPlaceholderBecomesFileURL(t *testing.T) {
	res := &CallResult{SpillFiles: []SpillFile{{Path: "/tmp/out-1.png", Size: 9}}}
	ExtractFields([]byte(`{"data":[{"b64_json":"@spill:0"}],"usage":{"total_tokens":2}}`), res)
	if len(res.OutputURLs) != 1 || res.OutputURLs[0] != "file:///tmp/out-1.png" {
		t.Fatalf("OutputURLs = %#v", res.OutputURLs)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 2 {
		t.Fatalf("usage 应照常抽取: %#v", res.Usage)
	}
}

func TestExtractFields_PlaceholderWithoutSpillFilesStaysLiteral(t *testing.T) {
	res := &CallResult{}
	ExtractFields([]byte(`{"data":[{"b64_json":"@spill:0"}]}`), res)
	if len(res.OutputURLs) != 1 || res.OutputURLs[0] != "data:image/png;base64,@spill:0" {
		t.Fatalf("无 SpillFiles 时不解释占位符: %#v", res.OutputURLs)
	}
}

func TestCallResult_RemoveSpillFiles(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "out-*.png")
	f.Close()
	res := &CallResult{SpillFiles: []SpillFile{{Path: f.Name()}}}
	res.RemoveSpillFiles()
	if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
		t.Fatal("应删除")
	}
	if len(res.SpillFiles) != 0 {
		t.Fatal("删除后应清空列表(幂等)")
	}
	res.RemoveSpillFiles() // 二次调用不 panic
}
