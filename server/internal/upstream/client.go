package upstream

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Client struct {
	http    *http.Client
	baseURL string
	// tempDir 非空时启用落盘路径：multipart 请求体写文件、响应里的 b64 抽成文件。
	// 为空保持老行为（agent/chat/轮询等调用方不传,零影响）。
	tempDir string
}

type ClientOptions struct {
	BaseURL string
	Timeout time.Duration
	TempDir string
}

func NewClient(opts ClientOptions) *Client {
	timeout := opts.Timeout
	if timeout == 0 {
		// 繁忙的 newapi 后端上，某些生图/生视频任务确实会跑好几分钟；
		// 上限设成 30 分钟，这样卡住的渠道也能足够快地失败，
		// 让调用方能及时重试。
		timeout = 30 * time.Minute
	}
	return &Client{
		baseURL: strings.TrimRight(opts.BaseURL, "/"),
		http:    &http.Client{Timeout: timeout},
		tempDir: opts.TempDir,
	}
}

// tempDirOrOS：未注入目录（agent/chat 等老调用方、单测）时回退 os.TempDir。
func (c *Client) tempDirOrOS() string {
	if c.tempDir != "" {
		return c.tempDir
	}
	return os.TempDir()
}

type CallResult struct {
	StatusCode int
	Body       json.RawMessage
	OutputURLs []string
	// Text 是 chat 风格响应里 assistant 的文本正文（choices[0].message.content）。
	// 无条件抽取——是否当作产物由 finalize 决定：仅在没有任何 OutputURLs 时才算
	// 文本产物；生图类 chat 模型（gemini-image 等）的回复文本只是图片的附带说明，
	// 不能反过来把它们误判成文本任务。
	Text   string
	TaskID string
	Usage  *Usage
	// SpillFiles：响应里被抽成临时文件的大字符串（gpt-image 的 b64_json）。
	// ExtractFields 靠它把 "@spill:N" 翻译成 file:// 引用；调用方处理完必须
	// RemoveSpillFiles，callAndComplete 用 defer 兜底。
	SpillFiles []SpillFile
}

// RemoveSpillFiles 删掉抽取出来的临时文件。幂等。
func (r *CallResult) RemoveSpillFiles() {
	if r == nil {
		return
	}
	for _, f := range r.SpillFiles {
		_ = os.Remove(f.Path)
	}
	r.SpillFiles = nil
}

// spillPath 把 "@spill:N" 翻译成临时文件路径。只在 SpillFiles 非空且下标有效时
// 才解释——上游正文里碰巧出现同形字符串时原样保留,不会指向不存在的文件。
func (r *CallResult) spillPath(v string) (string, bool) {
	if r == nil || len(r.SpillFiles) == 0 || !strings.HasPrefix(v, spillPlaceholderPrefix) {
		return "", false
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(v, spillPlaceholderPrefix))
	if err != nil || idx < 0 || idx >= len(r.SpillFiles) {
		return "", false
	}
	return r.SpillFiles[idx].Path, true
}

type Usage struct {
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Cost             float64 `json:"cost"`
}

type MultipartFile struct {
	FieldName   string
	Filename    string
	ContentType string
	// Path 指向已落盘的文件。不再提供 []byte 形态——提供了就会有人用。
	Path string
}

type CallError struct {
	StatusCode int
	Body       string
	Err        error
}

func (e *CallError) Error() string {
	switch {
	case e == nil:
		return ""
	case e.StatusCode > 0:
		// Body 字段保留全文供分类判断；文本形态会进任务 error 和日志，折叠截断（I2）。
		return fmt.Sprintf("upstream %d: %s", e.StatusCode, truncateUpstreamLog([]byte(e.Body)))
	case e.Err != nil:
		return fmt.Sprintf("upstream call: %v", e.Err)
	default:
		return "upstream call failed"
	}
}

func (e *CallError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func IsTemporaryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var callErr *CallError
	if errors.As(err, &callErr) {
		if callErr.StatusCode > 0 {
			return isTemporaryStatus(callErr.StatusCode)
		}
		if callErr.Err != nil {
			return IsTemporaryError(callErr.Err)
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	return errors.Is(err, context.DeadlineExceeded)
}

func isTemporaryStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func (c *Client) Call(ctx context.Context, apiKey, method, path string, payload json.RawMessage) (*CallResult, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("upstream base url is empty")
	}
	url := c.baseURL + path
	var body io.Reader
	if len(payload) > 0 {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	// 请求只记方法、地址、字节数：请求体里有用户提示词、保密的绘图预设正文、参考图
	// base64，一律不进日志（I2）。Authorization 头同样不打。
	log.Printf("[upstream →] %s %s bytes=%d", method, url, len(payload))
	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[upstream ✗] %s %s transport error: %v", method, url, err)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &CallError{Err: err}
	}
	defer resp.Body.Close()
	// OpenAI 标准 /v1/audio/speech 直接返回裸音频字节流（非 JSON）。按响应头
	// Content-Type 识别：转成 data URL 进 OutputURLs——下游 persistOutputs 对
	// data: 早有完整处理（Sora 视频同形态），按声明 mime 落对象存储、推
	// outputKind=audio，前端零改动。只认 `audio/*` 不认 octet-stream：后者在
	// 各家代理里语义太杂，误把别的二进制当音频存起来比漏识别更糟，真遇到再放开。
	// 日志也在此分流：二进制 body 打进日志是满屏乱码，只打 类型+大小。
	// 判定必须在 readBody 之前：裸音频不是 JSON，不能进流式抽取器。
	if respMime := binaryAudioMime(resp.Header.Get("Content-Type")); respMime != "" && resp.StatusCode < 400 {
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, &CallError{Err: err}
		}
		log.Printf("[upstream ←] %s status=%d binary=%s size=%d", url, resp.StatusCode, respMime, len(raw))
		return &CallResult{
			StatusCode: resp.StatusCode,
			OutputURLs: []string{"data:" + respMime + ";base64," + base64.StdEncoding.EncodeToString(raw)},
		}, nil
	}
	raw, spilled, err := c.readBody(resp)
	if err != nil {
		return nil, &CallError{Err: err}
	}
	logUpstreamResponse(url, resp.StatusCode, raw)
	if resp.StatusCode >= 400 {
		(&CallResult{SpillFiles: spilled}).RemoveSpillFiles()
		return nil, &CallError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	result := &CallResult{StatusCode: resp.StatusCode, Body: json.RawMessage(raw), SpillFiles: spilled}
	ExtractFields(raw, result)
	return result, nil
}

// spillKeys：第一期只抽 gpt-image 系列的 b64_json。gemini chat 格式的 data:/inline_data
// 留第二期（spec §9）。
var spillKeys = map[string]bool{"b64_json": true}

// readBody 读响应体。注入了 tempDir 的 client 走流式抽取；否则整份读——
// 老调用方（agent/chat/轮询）零变化。
func (c *Client) readBody(resp *http.Response) ([]byte, []SpillFile, error) {
	if c.tempDir == "" {
		raw, err := io.ReadAll(resp.Body)
		return raw, nil, err
	}
	res, err := spillLargeStrings(resp.Body, spillKeys, c.tempDir)
	if err != nil {
		return nil, nil, err
	}
	return res.JSON, res.Files, nil
}

// binaryAudioMime 判定响应是否为裸音频流：Content-Type 主类型是 audio/ 时返回
// 去掉参数（; charset=… 等）的纯 mime，否则返回空串。
func binaryAudioMime(contentType string) string {
	m := strings.TrimSpace(contentType)
	if semi := strings.Index(m, ";"); semi >= 0 {
		m = strings.TrimSpace(m[:semi])
	}
	m = strings.ToLower(m)
	if strings.HasPrefix(m, "audio/") {
		return m
	}
	return ""
}

func (c *Client) CallMultipart(ctx context.Context, apiKey, method, path string, fields map[string]string, files []MultipartFile) (*CallResult, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("upstream base url is empty")
	}
	url := c.baseURL + path
	// multipart 整体先写临时文件再发：body 是 *os.File,http 层带 Content-Length,
	// 不走 chunked；参考图字节只经过一个 32KB 的 io.Copy 缓冲。
	body, err := os.CreateTemp(c.tempDirOrOS(), "mp-*.bin")
	if err != nil {
		return nil, fmt.Errorf("create multipart temp: %w", err)
	}
	defer func() {
		_ = body.Close()
		_ = os.Remove(body.Name())
	}()
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	for _, file := range files {
		fieldName := file.FieldName
		if fieldName == "" {
			fieldName = "image"
		}
		contentType := file.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(fieldName), escapeQuotes(file.Filename)))
		header.Set("Content-Type", contentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, err
		}
		src, err := os.Open(file.Path)
		if err != nil {
			return nil, fmt.Errorf("open multipart source: %w", err)
		}
		_, err = io.Copy(part, src)
		src.Close()
		if err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	size, err := body.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	// http.NewRequest 收到 *os.File 不会自动算长度,显式给,否则走 chunked。
	req.ContentLength = size
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	log.Printf("[upstream →] %s %s multipart fields=%d files=%d bytes=%d", method, url, len(fields), len(files), size)
	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[upstream ✗] %s %s transport error: %v", method, url, err)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &CallError{Err: err}
	}
	defer resp.Body.Close()
	raw, spilled, err := c.readBody(resp)
	if err != nil {
		return nil, &CallError{Err: err}
	}
	logUpstreamResponse(url, resp.StatusCode, raw)
	if resp.StatusCode >= 400 {
		(&CallResult{SpillFiles: spilled}).RemoveSpillFiles()
		return nil, &CallError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	result := &CallResult{StatusCode: resp.StatusCode, Body: json.RawMessage(raw), SpillFiles: spilled}
	ExtractFields(raw, result)
	return result, nil
}

func escapeQuotes(s string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(s)
}

// upstreamLogCap 失败响应体日志最多留这么多字节（折叠 data URI 之后再截断）。
const upstreamLogCap = 512

// logUpstreamResponse 只记状态码和字节数；非 2xx 额外附折叠截断后的前 512 字节，
// 便于排查。2xx 响应体不打：成功响应常回显 revised_prompt 等，可能带出预设正文。
func logUpstreamResponse(url string, status int, raw []byte) {
	if status >= 200 && status < 300 {
		log.Printf("[upstream ←] %s status=%d bytes=%d", url, status, len(raw))
		return
	}
	log.Printf("[upstream ←] %s status=%d bytes=%d head=%s", url, status, len(raw), truncateUpstreamLog(raw))
}

// dataURIPayloadRegex 匹配 data URI 的 base64 正文部分（保留 "data:<mime>;base64," 前缀）。
var dataURIPayloadRegex = regexp.MustCompile(`(data:[a-zA-Z0-9.+/_-]+;base64,)[A-Za-z0-9+/=_-]+`)

// foldDataURIs 把 data URI 的正文折叠成 "…"，避免截断后的日志仍是一屏 base64。
func foldDataURIs(s string) string {
	return dataURIPayloadRegex.ReplaceAllString(s, "${1}…")
}

// truncateUpstreamLog 先折叠 data URI，再截到 upstreamLogCap 字节并标注剩余字节数。
func truncateUpstreamLog(b []byte) string {
	s := foldDataURIs(string(b))
	if len(s) <= upstreamLogCap {
		return s
	}
	cut := upstreamLogCap
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("…(+%d bytes)", len(s)-cut)
}

var (
	markdownImageRegex = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
	dataURLRegex       = regexp.MustCompile(`data:image/[a-zA-Z0-9+._-]+;base64,[A-Za-z0-9+/=_-]+`)
	imageURLRegex      = regexp.MustCompile(`https?://[^\s)"'<>]+?\.(?:png|jpe?g|webp|gif|bmp)(?:\?[^\s)"'<>]*)?`)
)

func ExtractFields(raw []byte, result *CallResult) {
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return
	}
	if v, ok := decoded["task_id"].(string); ok {
		result.TaskID = v
	} else if v, ok := decoded["id"].(string); ok {
		result.TaskID = v
	} else if v, ok := decoded["result"].(string); ok {
		// 部分代理在提交时返回 { "code":1, "result":"<jobId>" }。
		// 放在最后尝试，这样正常使用 task_id/id 的模型不受影响。
		result.TaskID = v
	}
	if dataArr, ok := decoded["data"].([]any); ok {
		for _, item := range dataArr {
			if m, ok := item.(map[string]any); ok {
				if u, ok := m["url"].(string); ok {
					result.OutputURLs = append(result.OutputURLs, u)
				} else if u, ok := m["image_url"].(string); ok {
					result.OutputURLs = append(result.OutputURLs, u)
				} else if b64, ok := m["b64_json"].(string); ok {
					if p, ok := result.spillPath(b64); ok {
						result.OutputURLs = append(result.OutputURLs, "file://"+p)
					} else {
						result.OutputURLs = append(result.OutputURLs, "data:image/png;base64,"+b64)
					}
				}
			}
		}
	}
	// 音频上游（豆包 /v1/audio/speech 等）同步返回顶层 audio_url。去重后再追加：
	// 轮询路径先按 pollingTemplate.urlField 抠过一次 URL 再进本函数，urlField 恰好
	// 配成 audio_url 时不去重会 append 两遍 → persistOutputs 存两份产物。
	// 不加顶层裸 "url" 的识别——很多上游的提交响应里 url 是回显/占位字段，误提取
	// 的风险比 audio_url 这种语义明确的键大得多，等真出现再说。
	if u, ok := decoded["audio_url"].(string); ok && u != "" && !containsURL(result.OutputURLs, u) {
		result.OutputURLs = append(result.OutputURLs, u)
	}
	if outputs, ok := decoded["output"].([]any); ok {
		for _, item := range outputs {
			if s, ok := item.(string); ok {
				result.OutputURLs = append(result.OutputURLs, s)
			}
		}
	}

	// chat 风格的响应（gemini-image、通过 chat 调用的 gpt-image 等）
	if choices, ok := decoded["choices"].([]any); ok {
		for _, choice := range choices {
			m, ok := choice.(map[string]any)
			if !ok {
				continue
			}
			msg, ok := m["message"].(map[string]any)
			if !ok {
				continue
			}
			extractImagesFromChatContent(msg["content"], result)
			if result.Text == "" {
				result.Text = extractTextFromChatContent(msg["content"])
			}
			// 有些 proxy 会额外返回一个 "images" 字段
			if imgs, ok := msg["images"].([]any); ok {
				for _, im := range imgs {
					if mi, ok := im.(map[string]any); ok {
						if u, ok := mi["url"].(string); ok {
							result.OutputURLs = append(result.OutputURLs, u)
						}
					} else if s, ok := im.(string); ok {
						result.OutputURLs = append(result.OutputURLs, s)
					}
				}
			}
		}
	}

	// Gemini 原生响应：candidates[*].content.parts[*].inline_data / inlineData
	if candidates, ok := decoded["candidates"].([]any); ok {
		for _, c := range candidates {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			content, ok := m["content"].(map[string]any)
			if !ok {
				continue
			}
			parts, ok := content["parts"].([]any)
			if !ok {
				continue
			}
			for _, p := range parts {
				pm, ok := p.(map[string]any)
				if !ok {
					continue
				}
				inline, ok := pm["inline_data"].(map[string]any)
				if !ok {
					inline, ok = pm["inlineData"].(map[string]any)
				}
				if ok {
					mime, _ := inline["mime_type"].(string)
					if mime == "" {
						mime, _ = inline["mimeType"].(string)
					}
					data, _ := inline["data"].(string)
					if data != "" {
						if mime == "" {
							mime = "image/png"
						}
						result.OutputURLs = append(result.OutputURLs, "data:"+mime+";base64,"+data)
					}
				}
			}
		}
	}

	if usage, ok := decoded["usage"].(map[string]any); ok {
		u := &Usage{}
		if v, ok := usage["prompt_tokens"].(float64); ok {
			u.PromptTokens = int64(v)
		}
		if v, ok := usage["completion_tokens"].(float64); ok {
			u.CompletionTokens = int64(v)
		}
		if v, ok := usage["total_tokens"].(float64); ok {
			u.TotalTokens = int64(v)
		}
		if v, ok := usage["cost"].(float64); ok {
			u.Cost = v
		}
		result.Usage = u
	}
}

// extractTextFromChatContent 抽取 chat 响应正文（文本生成任务的产物）。
//   - string：整段即正文；
//   - array：拼接所有 type=="text" 片段（多模态响应把文字拆片段返回）。
func extractTextFromChatContent(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := m["type"].(string); t == "text" {
				if s, ok := m["text"].(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

// extractImagesFromChatContent 处理 `message.content` 的各种形态：
//   - string：可能包含 markdown 图片、裸 data URL 或图片 URL
//   - array：多模态片段，如 [{type:"image_url", image_url:{url:"..."}}]
func extractImagesFromChatContent(content any, result *CallResult) {
	switch v := content.(type) {
	case string:
		seen := map[string]bool{}
		addURL := func(u string) {
			if u == "" || seen[u] {
				return
			}
			seen[u] = true
			result.OutputURLs = append(result.OutputURLs, u)
		}
		// Markdown 图片：![alt](url)
		for _, m := range markdownImageRegex.FindAllStringSubmatch(v, -1) {
			if len(m) > 1 {
				addURL(strings.TrimSpace(m[1]))
			}
		}
		// 裸 data URL
		for _, u := range dataURLRegex.FindAllString(v, -1) {
			addURL(u)
		}
		// 裸 http(s) 图片 URL
		for _, u := range imageURLRegex.FindAllString(v, -1) {
			addURL(u)
		}
	case []any:
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			itype, _ := m["type"].(string)
			switch itype {
			case "image_url":
				if obj, ok := m["image_url"].(map[string]any); ok {
					if u, ok := obj["url"].(string); ok {
						result.OutputURLs = append(result.OutputURLs, u)
					}
				} else if u, ok := m["image_url"].(string); ok {
					result.OutputURLs = append(result.OutputURLs, u)
				}
			case "image":
				if u, ok := m["url"].(string); ok {
					result.OutputURLs = append(result.OutputURLs, u)
				} else if src, ok := m["source"].(map[string]any); ok {
					if data, ok := src["data"].(string); ok {
						mime, _ := src["media_type"].(string)
						if mime == "" {
							mime = "image/png"
						}
						result.OutputURLs = append(result.OutputURLs, "data:"+mime+";base64,"+data)
					}
				}
			}
		}
	}
}

func ExtractCostCents(cost float64) int64 {
	return int64(cost * 100)
}

// InterpolateTemplate 用 inputs 里的值填充一个 JSON template。
//
// 支持的语法：
//   - "{{key}}"                — 整个字符串替换为 inputs[key]（任意类型）
//   - "{{key[0]}}" / "{{a.b}}" — 对 inputs[key] 做索引或点号路径访问
//   - {"$map": "arrKey", "$as": "x", "$template": {...}}   — 对每个元素做 map
//   - {"$concat": [arr1, arr2, ...]}                        — 展开并拼接数组
//   - {"$if": "cond", "$then": ..., "$else": ...}           — 条件分支
func InterpolateTemplate(template json.RawMessage, inputs map[string]any) (json.RawMessage, error) {
	if len(template) == 0 {
		raw, _ := json.Marshal(inputs)
		return raw, nil
	}
	var decoded any
	if err := json.Unmarshal(template, &decoded); err != nil {
		return nil, err
	}
	result := interpolate(decoded, inputs)
	return json.Marshal(result)
}

func interpolate(node any, inputs map[string]any) any {
	switch v := node.(type) {
	case string:
		if strings.HasPrefix(v, "{{") && strings.HasSuffix(v, "}}") {
			key := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(v, "{{"), "}}"))
			return lookupPath(key, inputs)
		}
		return v
	case map[string]any:
		// 特殊操作符
		if mapKey, ok := v["$map"]; ok {
			return evalMap(v, mapKey, inputs)
		}
		if concatParts, ok := v["$concat"]; ok {
			return evalConcat(concatParts, inputs)
		}
		if condKey, ok := v["$if"]; ok {
			return evalIf(v, condKey, inputs)
		}
		out := make(map[string]any, len(v))
		for k, inner := range v {
			out[k] = interpolate(inner, inputs)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, inner := range v {
			out = append(out, interpolate(inner, inputs))
		}
		return out
	default:
		return v
	}
}

// lookupPath 解析 "key"、"key[0]"、"key[0][1]" 或 "key.sub"。
func lookupPath(path string, inputs map[string]any) any {
	if path == "" {
		return nil
	}
	// 第一段："key" 或 "key[...]"
	base := path
	if idx := strings.IndexAny(path, "[."); idx >= 0 {
		base = path[:idx]
	}
	value, ok := inputs[base]
	if !ok {
		return nil
	}
	rest := path[len(base):]
	return walkPath(value, rest)
}

func walkPath(value any, rest string) any {
	for len(rest) > 0 {
		if strings.HasPrefix(rest, "[") {
			end := strings.Index(rest, "]")
			if end < 0 {
				return nil
			}
			idx, err := strconv.Atoi(rest[1:end])
			if err != nil {
				return nil
			}
			// 用 toAnySlice 反射兜底而非 .([]any) 硬断言：生成链路的 images 是业务直接构造的
			// []string（image_inputs.go，渲染前无 JSON 往返），硬断言会失败让 {{images[0]}} 全哑。
			// $map 早有同款兜底，下标访问此前漏了。
			arr := toAnySlice(value)
			if arr == nil {
				return nil
			}
			if idx < 0 || idx >= len(arr) {
				return nil
			}
			value = arr[idx]
			rest = rest[end+1:]
		} else if strings.HasPrefix(rest, ".") {
			// 在嵌套 map 里做点号查找
			next := rest[1:]
			end := strings.IndexAny(next, "[.")
			field := next
			consumed := 1 + len(next)
			if end >= 0 {
				field = next[:end]
				consumed = 1 + end
			}
			m, ok := value.(map[string]any)
			if !ok {
				return nil
			}
			value = m[field]
			rest = rest[consumed:]
		} else {
			return nil
		}
	}
	return value
}

// evalMap 处理 {"$map": "key", "$as": "x", "$template": {...}}。
func evalMap(node map[string]any, mapKey any, inputs map[string]any) any {
	keyStr, ok := mapKey.(string)
	if !ok {
		return []any{}
	}
	source := lookupPath(keyStr, inputs)
	arr := toAnySlice(source)
	if arr == nil {
		return []any{}
	}
	asName, _ := node["$as"].(string)
	if asName == "" {
		asName = "item"
	}
	template, hasTemplate := node["$template"]
	if !hasTemplate {
		return arr
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		sub := make(map[string]any, len(inputs)+1)
		for k, v := range inputs {
			sub[k] = v
		}
		sub[asName] = item
		out = append(out, interpolate(template, sub))
	}
	return out
}

// toAnySlice 把任意 Go 切片规整成 []any；非切片返回 nil。
// $map 的数据源既可能是 JSON 解码出来的 []any，也可能是业务代码直接构造的具体切片
// （如 drama 参考图 submitInputForMaterial 直接塞的 []string）——后者用 .([]any) 断言会失败，
// 导致 $map 当空、参考图全丢。这里用反射兜底，兼容 []string/[]int/…任意 slice。
func toAnySlice(v any) []any {
	if v == nil {
		return nil
	}
	if a, ok := v.([]any); ok {
		return a
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice {
		return nil
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out
}

// evalConcat 处理 {"$concat": [arr1, arr2, ...]} —— 展开一层。
func evalConcat(parts any, inputs map[string]any) any {
	arr, ok := parts.([]any)
	if !ok {
		return []any{}
	}
	out := []any{}
	for _, part := range arr {
		resolved := interpolate(part, inputs)
		if sub, isArr := resolved.([]any); isArr {
			out = append(out, sub...)
			continue
		}
		if resolved != nil {
			out = append(out, resolved)
		}
	}
	return out
}

// evalIf 处理 {"$if": "cond", "$then": ..., "$else": ...}。
func evalIf(node map[string]any, condKey any, inputs map[string]any) any {
	keyStr, ok := condKey.(string)
	if !ok {
		return nil
	}
	value := lookupPath(keyStr, inputs)
	if isTruthy(value) {
		if then, has := node["$then"]; has {
			return interpolate(then, inputs)
		}
		return nil
	}
	if elseVal, has := node["$else"]; has {
		return interpolate(elseVal, inputs)
	}
	return nil
}

func isTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case string:
		return val != ""
	case bool:
		return val
	case []any:
		return len(val) > 0
	case map[string]any:
		return len(val) > 0
	case float64:
		return val != 0
	case int:
		return val != 0
	case int64:
		return val != 0
	}
	// 业务直接构造的 []string 等具体 slice / map / array：非空为真、空为假，
	// 与 []any/map[string]any 分支同义（否则会掉进末尾 return true 把空切片当真）。
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() > 0
	}
	return true
}

func containsURL(urls []string, u string) bool {
	for _, x := range urls {
		if x == u {
			return true
		}
	}
	return false
}
