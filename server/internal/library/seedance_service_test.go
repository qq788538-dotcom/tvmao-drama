package library

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/seedance"
)

// ---- fakes ----

type fakeRegistrar struct {
	configured bool
	id         string
	err        error
	calls      int
	gotURL     string
	gotType    string
	// onRegister 在 Register 被调用时执行（返回前），用于观察调用期间的落库状态。
	onRegister func()
}

func (f *fakeRegistrar) Configured() bool { return f.configured }
func (f *fakeRegistrar) Register(_ context.Context, url, t string) (string, error) {
	f.calls++
	f.gotURL, f.gotType = url, t
	if f.onRegister != nil {
		f.onRegister()
	}
	return f.id, f.err
}

func newSyncSvc(t *testing.T, reg *fakeRegistrar) (*SeedanceService, *fakeSeedanceAssets, *fakeRegistrar) {
	t.Helper()
	repo := newFakeAssets()
	return NewSeedanceService(repo, reg, fakeLibrarySigner{}), repo, reg
}

func newSvc(repo SeedanceAssetsRepository, client seedanceAPI) *SeedanceService {
	return NewSeedanceService(repo, client, fakeLibrarySigner{})
}

type fakeSeedanceAssets struct {
	rows map[string]SeedanceAsset

	upserts   []SeedanceAsset
	setStatus []struct{ key, status string }
	setError  []struct{ key, status, msg string }
}

func newFakeAssets(rows ...SeedanceAsset) *fakeSeedanceAssets {
	m := map[string]SeedanceAsset{}
	for _, r := range rows {
		m[r.ObjectKey] = r
	}
	return &fakeSeedanceAssets{rows: m}
}

func (f *fakeSeedanceAssets) Get(objectKey string) (*SeedanceAsset, error) {
	if a, ok := f.rows[objectKey]; ok {
		return &a, nil
	}
	return nil, ErrSeedanceAssetNotFound
}
func (f *fakeSeedanceAssets) GetMany(userID int64, keys []string) (map[string]SeedanceAsset, error) {
	out := map[string]SeedanceAsset{}
	for _, k := range keys {
		if a, ok := f.rows[k]; ok && a.UserID == userID {
			out[k] = a
		}
	}
	return out, nil
}
func (f *fakeSeedanceAssets) Upsert(a SeedanceAsset) error {
	a.Error, a.FirstFailedAt = nil, nil // 与真实 Upsert 一致：写入时清空 error / first_failed_at
	f.upserts = append(f.upserts, a)
	f.rows[a.ObjectKey] = a
	return nil
}
func (f *fakeSeedanceAssets) SetStatus(objectKey, status string) error {
	f.setStatus = append(f.setStatus, struct{ key, status string }{objectKey, status})
	return nil
}
func (f *fakeSeedanceAssets) SetError(objectKey, status, msg string) error {
	f.setError = append(f.setError, struct{ key, status, msg string }{objectKey, status, msg})
	return nil
}

type fakeLibrarySigner struct{}

// 必须复刻真实 MediaSigner.Sign 的语义：只认 canonical 的 media:// 形式，认不出
// 就原样返回。以前这个 fake 无脑给任何输入加 https:// 前缀，把「传了裸 object key」
// 那个 bug 整个掩盖了——Seedance 收到的是一个根本不是 URL 的字符串。
func (fakeLibrarySigner) Sign(s string) string {
	const proto = "media://"
	if !strings.HasPrefix(s, proto) {
		return s
	}
	return "https://signed/" + strings.TrimPrefix(s, proto)
}

type signerStub func(string) string

func (f signerStub) Sign(s string) string { return f(s) }

func libraryPtrStr(s string) *string        { return &s }
func libraryPtrInt(i int) *int              { return &i }
func libraryPtrTime(t time.Time) *time.Time { return &t }

// 素材组按用户复用：同一用户的第二张图不再重复建组。
// 版本号 bump(常配合换 key/账号)：旧素材组作废，重建 group 并回写新 id + 新版本，
// asset 注册进新组——否则会往旧账号的组里注册导致 group 找不到。
// 已 active 且版本没变 → 幂等返回，不重复占用上游配额。
func TestSubmitIdempotentWhenActive(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "active",
		Version: libraryPtrInt(1), AssetID: libraryPtrStr("Asset-old"),
	})
	client := &fakeRegistrar{configured: true}
	rec, err := newSvc(repo, client).Submit(context.Background(), 7, "k", seedance.AssetTypeImage)
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 0 {
		t.Fatal("已通过的图不该重复提交")
	}
	if *rec.AssetID != "Asset-old" {
		t.Fatalf("应返回原记录，实际 %+v", rec)
	}
}

// 签不出 http(s) 直链时绝不发给上游——否则根因会被上游的 InvalidParameter.URL 掩盖。
func TestSubmitRefusesUnsignableURL(t *testing.T) {
	client := &fakeRegistrar{configured: true}
	svc := NewSeedanceService(newFakeAssets(), client,
		signerStub(func(string) string { return "not-a-url" }))

	if _, err := svc.Submit(context.Background(), 7, "k", seedance.AssetTypeImage); err == nil {
		t.Fatal("签不出直链应报错")
	}
	if client.calls != 0 {
		t.Fatalf("不该把非 URL 发给上游，调用数 = %d", client.calls)
	}
}

// 上游判定 Failed = 图片不合规，同图重查结果不变 → 终态。
// 参数类错误同图重试必然同样失败 → 直接终态，不进重试窗口。
// 兜底窗口：漏分类的错误也不能无限重试。
// 版本 bump 让旧 asset 作废 → 重新提交。
func TestResolveActiveReturnsAssetRef(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "active", AssetID: libraryPtrStr("Asset-9"),
	})
	ref, err := newSvc(repo, &fakeRegistrar{configured: true}).Resolve(7, "k")
	if err != nil || ref != "asset://Asset-9" {
		t.Fatalf("ref=%q err=%v", ref, err)
	}
}

// 没校验过的图原样透传——这是绝大多数普通图片的路径。
func TestResolveUnverifiedPassesThrough(t *testing.T) {
	ref, err := newSvc(newFakeAssets(), &fakeRegistrar{configured: true}).Resolve(7, "k")
	if err != nil || ref != "" {
		t.Fatalf("ref=%q err=%v", ref, err)
	}
}

func TestResolvePendingReturnsNotReady(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{ObjectKey: "k", UserID: 7, Status: "pending"})
	_, err := newSvc(repo, &fakeRegistrar{configured: true}).Resolve(7, "k")
	if !errors.Is(err, ErrRealPersonNotReady) {
		t.Fatalf("pending 应报未就绪，实际 %v", err)
	}
}

func TestResolveFailedCarriesReason(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: seedanceStatusFailedPermanent,
		Error: libraryPtrStr("图片不合规"),
	})
	_, err := newSvc(repo, &fakeRegistrar{configured: true}).Resolve(7, "k")
	if !errors.Is(err, ErrRealPersonNotReady) || !strings.Contains(err.Error(), "不合规") {
		t.Fatalf("失败应带原因，实际 %v", err)
	}
}

// 别人的校验记录不复用——asset 归属于某个用户的素材组，跨用户引用会出问题。
func TestResolveOtherUserNotReused(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 99, Status: "active", AssetID: libraryPtrStr("Asset-9"),
	})
	ref, err := newSvc(repo, &fakeRegistrar{configured: true}).Resolve(7, "k")
	if err != nil || ref != "" {
		t.Fatalf("跨用户不该复用，ref=%q err=%v", ref, err)
	}
}

// 类型必须原样透传给上游：传错会让上游按另一套规格校验，素材莫名被拒。
// 同时类型必须落库——worker 重试提交时只能从表里读回它。
// worker 重试提交时把类型重放回上游。第一版忘了这件事，视频重试会被当图片提交。
// 不合规文案跟着类型走——用户看到「请更换图片」而手里是段视频只会困惑。
func TestNormalizeAssetType(t *testing.T) {
	cases := map[string]string{
		"image":   seedance.AssetTypeImage,
		"":        seedance.AssetTypeImage, // 老前端不传 kind → 图片
		"Video":   seedance.AssetTypeVideo, // 大小写不敏感
		" audio ": seedance.AssetTypeAudio,
	}
	for in, want := range cases {
		got, ok := NormalizeAssetType(in)
		if !ok || got != want {
			t.Fatalf("NormalizeAssetType(%q) = (%q,%v), want (%q,true)", in, got, ok, want)
		}
	}
	if _, ok := NormalizeAssetType("text"); ok {
		t.Fatal("非法 kind 必须被拒——否则上游会用一个没定义的 AssetType 报晦涩错误")
	}
}

// ---- 公共资产兜底（画布 AI 角色库，SetPublicAssets）----

type fakePublicAssets struct {
	m       map[string]string
	err     error
	queried [][]string
}

func (f *fakePublicAssets) PublicAssetIDs(keys []string) (map[string]string, error) {
	f.queried = append(f.queried, keys)
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]string{}
	for _, k := range keys {
		if id, ok := f.m[k]; ok {
			out[k] = id
		}
	}
	return out, nil
}

// 没校验过但命中公共库 → 以公共 asset 引用提交。
func TestResolvePublicFallbackOnMissing(t *testing.T) {
	svc := newSvc(newFakeAssets(), &fakeRegistrar{configured: true})
	svc.SetPublicAssets(&fakePublicAssets{m: map[string]string{"pub-k": "asset-20260224-x"}})
	ref, err := svc.Resolve(7, "pub-k")
	if err != nil || ref != "asset://asset-20260224-x" {
		t.Fatalf("公共资产应兜底，ref=%q err=%v", ref, err)
	}
}

// 别人的校验记录仍不复用，但公共库对所有用户开放。
func TestResolvePublicFallbackOnOtherUser(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "pub-k", UserID: 99, Status: "active", AssetID: libraryPtrStr("Asset-9"),
	})
	svc := newSvc(repo, &fakeRegistrar{configured: true})
	svc.SetPublicAssets(&fakePublicAssets{m: map[string]string{"pub-k": "pub-1"}})
	ref, err := svc.Resolve(7, "pub-k")
	if err != nil || ref != "asset://pub-1" {
		t.Fatalf("跨用户应落到公共库，ref=%q err=%v", ref, err)
	}
}

// 公共库也没有 → 维持「普通图片透传」的存量行为。
func TestResolvePublicMissStaysPassThrough(t *testing.T) {
	svc := newSvc(newFakeAssets(), &fakeRegistrar{configured: true})
	svc.SetPublicAssets(&fakePublicAssets{})
	ref, err := svc.Resolve(7, "k")
	if err != nil || ref != "" {
		t.Fatalf("公共库 miss 应透传，ref=%q err=%v", ref, err)
	}
}

// 用户自己的记录优先——公共库不该被问到。
func TestResolveOwnRecordBeatsPublic(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "active", AssetID: libraryPtrStr("Asset-9"),
	})
	pub := &fakePublicAssets{m: map[string]string{"k": "pub-1"}}
	svc := newSvc(repo, &fakeRegistrar{configured: true})
	svc.SetPublicAssets(pub)
	ref, err := svc.Resolve(7, "k")
	if err != nil || ref != "asset://Asset-9" {
		t.Fatalf("自己的记录应优先，ref=%q err=%v", ref, err)
	}
	if len(pub.queried) != 0 {
		t.Fatalf("命中自己的记录时不该查公共库，实际查了 %v", pub.queried)
	}
}

// 公共库查询故障只降级（当普通图片），不能把生成链路打断。
func TestResolvePublicErrorDegrades(t *testing.T) {
	svc := newSvc(newFakeAssets(), &fakeRegistrar{configured: true})
	svc.SetPublicAssets(&fakePublicAssets{err: errors.New("db down")})
	ref, err := svc.Resolve(7, "k")
	if err != nil || ref != "" {
		t.Fatalf("公共库故障应降级透传，ref=%q err=%v", ref, err)
	}
}

// Status 只补缺不覆盖：用户自己的记录（哪怕 pending）原样保留，缺的才用公共库填。
func TestStatusFillsPublicForMissing(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{ObjectKey: "mine", UserID: 7, Status: "pending"})
	svc := newSvc(repo, &fakeRegistrar{configured: true})
	svc.SetPublicAssets(&fakePublicAssets{m: map[string]string{"pub-k": "pub-1"}})
	out, err := svc.Status(7, []string{"mine", "pub-k", "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if out["mine"].Status != "pending" {
		t.Fatalf("自己的记录被覆盖了: %+v", out["mine"])
	}
	got, ok := out["pub-k"]
	if !ok || got.Status != "active" || got.AssetID == nil || *got.AssetID != "pub-1" {
		t.Fatalf("公共资产应补成 active: %+v", got)
	}
	if _, ok := out["unknown"]; ok {
		t.Fatal("两边都没有的 key 不该出现在结果里")
	}
}

// Status 的公共库故障同样只降级——角标退化为「未校验」，不报 500。
func TestStatusPublicErrorDegrades(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{ObjectKey: "mine", UserID: 7, Status: "active"})
	svc := newSvc(repo, &fakeRegistrar{configured: true})
	svc.SetPublicAssets(&fakePublicAssets{err: errors.New("db down")})
	out, err := svc.Status(7, []string{"mine", "pub-k"})
	if err != nil || out["mine"].Status != "active" {
		t.Fatalf("公共库故障不该影响自己的记录，out=%+v err=%v", out, err)
	}
}

// 没挂公共库（其他部署形态）→ 行为与从前逐字节一致。
func TestStatusWithoutPublicUnchanged(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{ObjectKey: "mine", UserID: 7, Status: "active"})
	svc := newSvc(repo, &fakeRegistrar{configured: true})
	out, err := svc.Status(7, []string{"mine", "other"})
	if err != nil || len(out) != 1 || out["mine"].Status != "active" {
		t.Fatalf("未挂公共库应与存量一致，out=%+v err=%v", out, err)
	}
}

// spec S12 未定：本机素材签出来是回环地址，上游拉不到。必须就地拒绝，不发上游、不落行。
func TestSubmitRefusesLoopbackURL(t *testing.T) {
	for _, host := range []string{"127.0.0.1:5173", "localhost:9", "[::1]:80"} {
		repo := newFakeAssets()
		client := &fakeRegistrar{configured: true}
		svc := NewSeedanceService(repo, client,
			signerStub(func(k string) string {
				return "http://" + host + "/media/" + strings.TrimPrefix(k, "media://") + "?token=t"
			}))
		_, err := svc.Submit(context.Background(), 7, "u7/a.png", seedance.AssetTypeImage)
		if !errors.Is(err, ErrLocalMediaNoPublicURL) {
			t.Fatalf("%s: err = %v, want ErrLocalMediaNoPublicURL", host, err)
		}
		if client.calls != 0 || len(repo.upserts) != 0 {
			t.Fatalf("%s: 回环地址不应发往上游或落行", host)
		}
	}
}

// 前端直接展示这条错误文案（spec X5「未配置时入口提示“合规接口未配置”」）。
type fakeSeedancePub struct{ configured bool }

func (f fakeSeedancePub) Configured() bool { return f.configured }
func (f fakeSeedancePub) Publish(_ context.Context, key string) (string, error) {
	return "https://bucket.example.com/" + key, nil
}

func TestSeedanceSignedURLPublishesLoopback(t *testing.T) {
	svc := NewSeedanceService(newFakeAssets(), &fakeRegistrar{configured: true},
		signerStub(func(k string) string {
			return "http://127.0.0.1:1234/media/" + strings.TrimPrefix(k, "media://")
		}))
	svc.SetMediaPublisher(fakeSeedancePub{configured: true})
	got, err := svc.signedURL(context.Background(), "u1/a.png")
	if err != nil || got != "https://bucket.example.com/u1/a.png" {
		t.Fatalf("got %q err=%v", got, err)
	}
	svc.SetMediaPublisher(fakeSeedancePub{configured: false})
	if _, err := svc.signedURL(context.Background(), "u1/a.png"); !errors.Is(err, ErrLocalMediaNoPublicURL) {
		t.Fatalf("未配置应报 ErrLocalMediaNoPublicURL, err=%v", err)
	}
}

// ---- 同步注册（seedance-asset 设计 A2）----

func TestSubmitActiveOnSuccess(t *testing.T) {
	svc, repo, reg := newSyncSvc(t, &fakeRegistrar{configured: true, id: "asset-1"})
	rec, err := svc.Submit(context.Background(), 1, "u1/a.mp4", seedance.AssetTypeVideo)
	if err != nil || rec.Status != "active" || *rec.AssetID != "asset-1" || reg.gotType != "Video" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
	// 发给上游的必须是签出来的直链，不是裸 object key。
	if reg.gotURL != "https://signed/u1/a.mp4" {
		t.Fatalf("url = %q", reg.gotURL)
	}
	got, _ := repo.Get("u1/a.mp4")
	if got.Status != "active" || *got.Version != 1 || got.AssetType != "Video" {
		t.Fatalf("got %+v", got)
	}
}

func TestSubmitFailureStoresUpstreamMessage(t *testing.T) {
	reg := &fakeRegistrar{configured: true, err: &seedance.UpstreamError{Status: 400, Message: "素材不合规：检测到真人"}}
	svc, repo, _ := newSyncSvc(t, reg)
	_, err := svc.Submit(context.Background(), 1, "u1/a.png", seedance.AssetTypeImage)
	if err == nil || err.Error() != "素材不合规：检测到真人" {
		t.Fatalf("err=%v", err)
	}
	if len(repo.setError) != 1 || repo.setError[0].status != seedanceStatusFailedPermanent || repo.setError[0].msg != "素材不合规：检测到真人" {
		t.Fatalf("setError=%+v", repo.setError)
	}
}

func TestSubmitRetriesAfterFailure(t *testing.T) {
	reg := &fakeRegistrar{configured: true, err: errors.New("boom")}
	svc, repo, _ := newSyncSvc(t, reg)
	svc.Submit(context.Background(), 1, "u1/a.png", seedance.AssetTypeImage)
	reg.err, reg.id = nil, "asset-2"
	rec, err := svc.Submit(context.Background(), 1, "u1/a.png", seedance.AssetTypeImage)
	if err != nil || rec.Status != "active" || reg.calls != 2 {
		t.Fatalf("rec=%+v err=%v calls=%d", rec, err, reg.calls)
	}
	if repo.rows["u1/a.png"].Status != "active" {
		t.Fatalf("row=%+v", repo.rows["u1/a.png"])
	}
}

func TestSubmitDisabledWhenModelOff(t *testing.T) {
	svc, _, reg := newSyncSvc(t, &fakeRegistrar{configured: false})
	if _, err := svc.Submit(context.Background(), 1, "u1/a.png", seedance.AssetTypeImage); !errors.Is(err, ErrSeedanceDisabled) || reg.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, reg.calls)
	}
	if svc.Configured() {
		t.Fatal("应未配置")
	}
}

// 终审 I3：同步注册可能长达数分钟，调用期间必须已有 pending 行——节点角标/5 秒轮询
// 看得到「正在进行」，生成侧 Resolve 报「请稍候」而不是把签名 URL 原样透传。
func TestSubmitWritesPendingBeforeRegister(t *testing.T) {
	reg := &fakeRegistrar{configured: true, id: "asset-1"}
	svc, repo, _ := newSyncSvc(t, reg)
	// 旧的失败行：pending 必须清掉 error，并带上本次的 asset_type。
	repo.rows["u1/a.mp4"] = SeedanceAsset{ObjectKey: "u1/a.mp4", UserID: 1, Status: seedanceStatusFailedPermanent,
		AssetType: seedance.AssetTypeImage, Error: libraryPtrStr("旧错误")}
	var during SeedanceAsset
	var resolveErr error
	reg.onRegister = func() {
		during = repo.rows["u1/a.mp4"]
		_, resolveErr = svc.Resolve(1, "u1/a.mp4")
	}
	if _, err := svc.Submit(context.Background(), 1, "u1/a.mp4", seedance.AssetTypeVideo); err != nil {
		t.Fatal(err)
	}
	if during.Status != "pending" || during.AssetType != seedance.AssetTypeVideo || during.Error != nil || during.UserID != 1 {
		t.Fatalf("Register 期间应已有 pending 行，实际 %+v", during)
	}
	if !errors.Is(resolveErr, ErrRealPersonNotReady) || !strings.Contains(resolveErr.Error(), "请稍候") {
		t.Fatalf("校验进行中生成应报「请稍候」，实际 %v", resolveErr)
	}
	if got := repo.rows["u1/a.mp4"]; got.Status != "active" || *got.AssetID != "asset-1" {
		t.Fatalf("成功后应为 active，实际 %+v", got)
	}
}

// 失败路径同样先写 pending，最终落 failed_permanent + 上游原文。
func TestSubmitPendingThenFailedPermanent(t *testing.T) {
	reg := &fakeRegistrar{configured: true, err: &seedance.UpstreamError{Status: 400, Message: "素材不合规"}}
	svc, repo, _ := newSyncSvc(t, reg)
	var during string
	reg.onRegister = func() { during = repo.rows["u1/a.png"].Status }
	if _, err := svc.Submit(context.Background(), 1, "u1/a.png", seedance.AssetTypeImage); err == nil {
		t.Fatal("应返回上游错误")
	}
	if during != "pending" {
		t.Fatalf("Register 期间应为 pending，实际 %q", during)
	}
	if got := repo.rows["u1/a.png"]; got.Status != seedanceStatusFailedPermanent {
		t.Fatalf("失败后应为 failed_permanent，实际 %+v", got)
	}
	if len(repo.setError) != 1 || repo.setError[0].msg != "素材不合规" {
		t.Fatalf("setError=%+v", repo.setError)
	}
}

// 崩溃遗留的 pending 行不阻塞重试：Submit 只跳过 active，再点一次就重新注册。
func TestSubmitResubmitsLeftoverPending(t *testing.T) {
	reg := &fakeRegistrar{configured: true, id: "asset-3"}
	svc, repo, _ := newSyncSvc(t, reg)
	repo.rows["u1/a.png"] = SeedanceAsset{ObjectKey: "u1/a.png", UserID: 1, Status: "pending", AssetType: seedance.AssetTypeImage}
	rec, err := svc.Submit(context.Background(), 1, "u1/a.png", seedance.AssetTypeImage)
	if err != nil || reg.calls != 1 || rec.Status != "active" {
		t.Fatalf("rec=%+v err=%v calls=%d", rec, err, reg.calls)
	}
}
