package generation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/seedance"
	"github.com/james/huabu_studio/apps/api/internal/upstream"
)

const maxInputImageBytes = 25 << 20

// 对齐 API 出口的 MediaSigner 时效：上游只需要本次任务期间可拉取，
// 不应该拿到可长期复用的对象存储地址。
const inputMediaURLExpiry = 24 * time.Hour

type preparedImageInput struct {
	Ref         string
	Value       string
	ContentType string
	// Data 只在 dataURI/base64 模式用（值本身就要内嵌进 JSON payload，内存不可避免）。
	Data []byte
	// Path/Size 是 edits 模式（requireBytes）的形态：字节在临时文件里，multipart 直接
	// 从文件读。9/4 事故里 10 个在飞任务的参考图 98MB × 2 份全在内存，这是根因之一。
	Path string
	Size int64
}

// spooledFile 是参考图落盘后的句柄。ContentType 由响应头/data URL 头归一化而来。
type spooledFile struct {
	Path        string
	Size        int64
	ContentType string
}

type spoolFunc func(ctx context.Context, ref string) (spooledFile, error)

func (s *Service) signInputMediaReferences(ctx context.Context, inputs map[string]any, skipImages bool) (map[string]any, error) {
	if s == nil || s.storage == nil || inputs == nil {
		return cloneInputs(inputs), nil
	}
	publish := s.publisher != nil && s.publisher.Configured()
	// 自由画布 HTTP 请求会在 GenerationHandler 签名，但 agent tool、
	// drama 等 service 直调会绕过那层边界。上游只接受可拉取 URL/base64/asset ID，
	// 所以 canonical media:// 必须在模板插值前临时展开；DB/画布快照仍保留
	// 能熬过签名 URL TTL 的 media:// 持久形态。
	sign := func(ref string) (string, error) {
		key, ok := s.storage.ExtractKey(ref)
		if !ok || key == "" {
			return ref, nil
		}
		if publish {
			return s.publisher.Publish(ctx, key)
		}
		url, err := s.storage.SignedURL(ctx, key, inputMediaURLExpiry)
		if err != nil || url == "" {
			if err == nil {
				err = fmt.Errorf("empty signed url")
			}
			return "", fmt.Errorf("sign %q: %w", key, err)
		}
		return url, nil
	}
	out := make(map[string]any, len(inputs))
	for k, v := range inputs {
		transform := sign
		if skipImages && (k == "images" || k == "image") {
			// 图片随后由 prepareImageInputs 直接读盘内嵌，不上传。
			transform = func(ref string) (string, error) { return ref, nil }
		}
		next, err := walkInputStrings(v, transform)
		if err != nil {
			return nil, err
		}
		out[k] = next
	}
	return out, nil
}

func walkInputStrings(v any, transform func(string) (string, error)) (any, error) {
	switch x := v.(type) {
	case string:
		return transform(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			next, err := walkInputStrings(val, transform)
			if err != nil {
				return nil, err
			}
			out[k] = next
		}
		return out, nil
	case []string:
		out := make([]string, len(x))
		for i, val := range x {
			next, err := transform(val)
			if err != nil {
				return nil, err
			}
			out[i] = next
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			next, err := walkInputStrings(val, transform)
			if err != nil {
				return nil, err
			}
			out[i] = next
		}
		return out, nil
	default:
		return v, nil
	}
}

func prepareImageInputs(ctx context.Context, inputs map[string]any, format string, requireBytes bool, fetch func(context.Context, string) ([]byte, string, error), spool spoolFunc) (map[string]any, []preparedImageInput, error) {
	if format == "" {
		format = models.ImageInputFormatURL
	}
	refs := imageRefsFromInputs(inputs)
	if len(refs) == 0 {
		return cloneInputs(inputs), nil, nil
	}
	if requireBytes && spool == nil {
		// 绝不静默退回「读进内存」：那正是要淘汰的路径。
		return nil, nil, fmt.Errorf("image spool is not configured")
	}
	out := cloneInputs(inputs)
	prepared := make([]preparedImageInput, 0, len(refs))
	values := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		img, err := prepareOneImageInput(ctx, ref, format, requireBytes, fetch, spool)
		if err != nil {
			removePreparedFiles(prepared) // 第 N 张失败,前 N-1 张的临时文件不能漏
			return nil, nil, err
		}
		prepared = append(prepared, img)
		values = append(values, img.Value)
	}
	if len(values) == 0 {
		return out, prepared, nil
	}
	out["images"] = values
	out["image"] = values[0]
	return out, prepared, nil
}

// removePreparedFiles 删掉 edits 模式落盘的参考图。幂等：Path 为空或已删都跳过。
func removePreparedFiles(images []preparedImageInput) {
	for i := range images {
		if images[i].Path != "" {
			_ = os.Remove(images[i].Path)
			images[i].Path = ""
		}
	}
}

func prepareOneImageInput(ctx context.Context, ref, format string, requireBytes bool, fetch func(context.Context, string) ([]byte, string, error), spool spoolFunc) (preparedImageInput, error) {
	img := preparedImageInput{Ref: ref, Value: ref}
	switch format {
	case models.ImageInputFormatURL:
		if requireBytes {
			sf, err := spool(ctx, ref)
			if err != nil {
				return img, fmt.Errorf("load image %q: %w", ref, err)
			}
			img.Path = sf.Path
			img.Size = sf.Size
			img.ContentType = normalizeImageMIME(sf.ContentType)
		}
		return img, nil
	case models.ImageInputFormatDataURI, models.ImageInputFormatBase64:
		body, mime, err := loadImageReference(ctx, ref, fetch)
		if err != nil {
			return img, fmt.Errorf("load image %q: %w", ref, err)
		}
		mime = normalizeImageMIME(mime)
		encoded := base64.StdEncoding.EncodeToString(body)
		if format == models.ImageInputFormatDataURI {
			img.Value = "data:" + mime + ";base64," + encoded
		} else {
			img.Value = encoded
		}
		img.Data = body
		img.ContentType = mime
		return img, nil
	default:
		return img, fmt.Errorf("unsupported image input format %q", format)
	}
}

func loadImageReference(ctx context.Context, ref string, fetch func(context.Context, string) ([]byte, string, error)) ([]byte, string, error) {
	if strings.HasPrefix(ref, "data:") {
		body, mime, _, err := decodeDataURL(ref)
		return body, mime, err
	}
	if fetch == nil {
		return nil, "", fmt.Errorf("image fetcher is not configured")
	}
	return fetch(ctx, ref)
}

func imageRefsFromInputs(inputs map[string]any) []string {
	if inputs == nil {
		return nil
	}
	var refs []string
	appendString := func(v any) {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			refs = append(refs, s)
		}
	}
	switch v := inputs["images"].(type) {
	case []string:
		for _, s := range v {
			appendString(s)
		}
	case []any:
		for _, item := range v {
			appendString(item)
		}
	}
	if len(refs) == 0 {
		appendString(inputs["image"])
	}
	return refs
}

func cloneInputs(inputs map[string]any) map[string]any {
	out := make(map[string]any, len(inputs))
	for k, v := range inputs {
		out[k] = v
	}
	return out
}

// seedanceAssetRefFields 是会被 asset:// 替换扫到的字段。images/image 是图片参考
// （imageRefsFromInputs 的同一约定）；videos/audios 是画布连线喂进来的视频/音频参考
// （见 web 的 buildVideoGenerationInputs），它们同样可以先过合规校验再以 asset://
// 引用。其余字段不动。
//
// 扩到视频/音频不会动到既有生成路径：Resolve 对「从没提交过校验」的 object_key
// 返回空串、原样透传，所以没被用户主动校验过的素材（例如项目管理里的角色配音
// 音频）走的还是签名 URL。
var seedanceAssetRefFields = []string{"images", "image", "videos", "video", "audios", "audio"}

// ErrVerifiedAssetUpstreamMismatch：合规素材注册在内置模型 seedance-asset 解析出的上游账号下
// （模型可在模型中心单独配上游，否则回落全局）。生成模型解析出的上游
// 与它不同时，上游里没有这个素材。
var ErrVerifiedAssetUpstreamMismatch = errors.New("已校验素材注册在合规校验模型 seedance-asset 所用的上游账号下，当前模型使用的上游与之不同，无法引用。请让两者使用同一上游，或改用与合规校验相同的上游后重新生成。")

// applyRealPersonRefs 是 Submit / SubmitTx / prepareExecution 共用的 doubao-seedance* 素材处理：
// 先把已校验的图换成 asset:// 引用，再确认目标模型的上游就是素材注册时 seedance-asset 用的上游。
// 两者解析出的上游指纹不同、请求里又有 asset:// 引用时直接拒绝，不调上游——
// 否则用户只会看到上游一条晦涩的 asset not found。
// 待办：素材按上游指纹分账号注册。
func (s *Service) applyRealPersonRefs(userID int64, model *models.ModelConfig, inputs map[string]any) (map[string]any, error) {
	if s.realPerson == nil || model == nil || !strings.HasPrefix(model.ModelID, "doubao-seedance") {
		return inputs, nil
	}
	out, err := s.replaceRealPersonImageRefs(userID, inputs)
	if err != nil {
		return nil, err
	}
	if !hasAssetRefs(out) {
		return out, nil
	}
	// 合规模型查不到时按 ResolveUpstream(nil) 的约定回落全局。
	var assetModel *models.ModelConfig
	if s.models != nil {
		if m, err := s.models.GetByModelID(seedance.ModelID); err == nil {
			assetModel = m
		}
	}
	if !model.HasOwnUpstream() && !assetModel.HasOwnUpstream() {
		return out, nil // 两者都走全局，必然同一账号
	}
	genKey, genBase, gerr := s.upstreamCredentials(userID, model)
	assetKey, assetBase, aerr := s.upstreamCredentials(userID, assetModel)
	if gerr != nil || aerr != nil || models.UpstreamFingerprint(genBase, genKey) != models.UpstreamFingerprint(assetBase, assetKey) {
		return nil, ErrVerifiedAssetUpstreamMismatch
	}
	return out, nil
}

// hasAssetRefs 报告 seedanceAssetRefFields 里是否有 asset:// 引用（刚替换的，或排队任务快照里已有的）。
func hasAssetRefs(inputs map[string]any) bool {
	found := false
	for _, field := range seedanceAssetRefFields {
		v, ok := inputs[field]
		if !ok {
			continue
		}
		_, _ = walkInputStrings(v, func(ref string) (string, error) {
			if strings.HasPrefix(ref, "asset://") {
				found = true
			}
			return ref, nil
		})
	}
	return found
}

// replaceRealPersonImageRefs 把 seedanceAssetRefFields 里已通过校验的素材换成
// asset:// 引用。替换必须发生在 signInputMediaReferences 之前——签名后再替换
// 也可以（ExtractKey 认签名 URL），放前面是让 task 快照里留下 asset:// 便于排查。
func (s *Service) replaceRealPersonImageRefs(userID int64, inputs map[string]any) (map[string]any, error) {
	out := cloneInputs(inputs)
	transform := func(ref string) (string, error) {
		key, ok := s.storage.ExtractKey(ref)
		if !ok || key == "" {
			return ref, nil // 外部 URL / data: / 已是 asset:// → 不动
		}
		assetRef, err := s.realPerson.Resolve(userID, key)
		if err != nil {
			return "", err
		}
		if assetRef == "" {
			return ref, nil // 没做过合规校验 → 原样
		}
		return assetRef, nil
	}
	for _, field := range seedanceAssetRefFields {
		v, ok := out[field]
		if !ok {
			continue
		}
		next, err := walkInputStrings(v, transform)
		if err != nil {
			return nil, err
		}
		out[field] = next
	}
	return out, nil
}

func (s *Service) fetchInputImage(ctx context.Context, ref string) ([]byte, string, error) {
	if strings.HasPrefix(ref, "data:") {
		body, mime, _, err := decodeDataURL(ref)
		return body, mime, err
	}
	if body, mime, ok, err := s.readLocalMedia(ctx, ref); ok {
		return body, mime, err
	}
	if !(strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")) {
		return nil, "", fmt.Errorf("unsupported image reference")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
	if err != nil {
		return nil, "", err
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("fetch %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInputImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxInputImageBytes {
		return nil, "", fmt.Errorf("image exceeds %d bytes", maxInputImageBytes)
	}
	return body, normalizeImageMIME(resp.Header.Get("Content-Type")), nil
}

// spoolInputImage 把一张参考图写进临时文件。http(s) 边下边写、data: 边解码边写；
// 两者都经 LimitReader 卡 maxInputImageBytes，超限删文件报错——上限语义与
// fetchInputImage 完全一致，只是字节不再进内存。
func (s *Service) spoolInputImage(ctx context.Context, ref string) (spooledFile, error) {
	var src io.Reader
	var mime string
	switch {
	case strings.HasPrefix(ref, "data:"):
		hdr, payload, ok := strings.Cut(ref[len("data:"):], ",")
		if !ok {
			return spooledFile{}, fmt.Errorf("malformed data url")
		}
		mime = "application/octet-stream"
		isB64 := false
		for _, seg := range strings.Split(hdr, ";") {
			seg = strings.TrimSpace(seg)
			if seg == "base64" {
				isB64 = true
			} else if seg != "" {
				mime = seg
			}
		}
		if isB64 {
			src = base64.NewDecoder(base64.StdEncoding, strings.NewReader(payload))
		} else {
			src = strings.NewReader(payload)
		}
	case s.storage != nil && func() bool { k, ok := s.storage.ExtractKey(ref); return ok && k != "" }():
		rc, _, err := s.openLocalMedia(ctx, ref)
		if err != nil {
			return spooledFile{}, err
		}
		defer rc.Close()
		src, mime = sniffReader(rc)
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
		if err != nil {
			return spooledFile{}, err
		}
		client := s.httpClient
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return spooledFile{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return spooledFile{}, fmt.Errorf("fetch %d", resp.StatusCode)
		}
		src = resp.Body
		mime = resp.Header.Get("Content-Type")
	default:
		return spooledFile{}, fmt.Errorf("unsupported image reference")
	}

	f, err := os.CreateTemp(s.tempDir(), "in-*.bin")
	if err != nil {
		return spooledFile{}, fmt.Errorf("create temp: %w", err)
	}
	n, err := io.Copy(f, io.LimitReader(src, maxInputImageBytes+1))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil && n > maxInputImageBytes {
		err = fmt.Errorf("image exceeds %d bytes", maxInputImageBytes)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return spooledFile{}, err
	}
	return spooledFile{Path: f.Name(), Size: n, ContentType: normalizeImageMIME(mime)}, nil
}

func normalizeImageMIME(mime string) string {
	if idx := strings.Index(mime, ";"); idx >= 0 {
		mime = strings.TrimSpace(mime[:idx])
	}
	if mime == "" || mime == "application/octet-stream" {
		return "image/png"
	}
	return mime
}

func multipartFilesFromPrepared(images []preparedImageInput) []upstream.MultipartFile {
	files := make([]upstream.MultipartFile, 0, len(images))
	for i, img := range images {
		if img.Path == "" {
			continue
		}
		files = append(files, upstream.MultipartFile{
			FieldName:   "image",
			Filename:    fmt.Sprintf("image-%d.%s", i+1, extFromMime(img.ContentType)),
			ContentType: img.ContentType,
			Path:        img.Path,
		})
	}
	return files
}

func sanitizeTaskPayload(payload []byte) json.RawMessage {
	if len(payload) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return json.RawMessage(payload)
	}
	sanitized, err := json.Marshal(sanitizeTaskValue(decoded))
	if err != nil {
		return json.RawMessage(payload)
	}
	return json.RawMessage(sanitized)
}

func sanitizeTaskSnapshot(inputs map[string]any) map[string]any {
	if inputs == nil {
		return map[string]any{}
	}
	sanitized, ok := sanitizeTaskValue(inputs).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return sanitized
}

func sanitizeTaskValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = sanitizeTaskValue(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = sanitizeTaskValue(v)
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = sanitizeTaskValue(v)
		}
		return out
	case string:
		return sanitizeTaskString(x)
	default:
		return v
	}
}

func sanitizeTaskString(s string) string {
	if strings.HasPrefix(s, "data:image/") && strings.Contains(s, ";base64,") {
		return "[redacted image data_uri]"
	}
	if len(s) > 4096 && looksLikeBase64(s) {
		return "[redacted image base64]"
	}
	return s
}

func looksLikeBase64(s string) bool {
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') ||
			(r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == '+' || r == '/' || r == '=' || r == '-' || r == '_' || r == '\n' || r == '\r' {
			continue
		}
		return false
	}
	return true
}
