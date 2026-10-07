package library

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/seedance"
)

// ---- fakes ----

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
func (f *fakeSeedanceAssets) ListPending(currentVersion int) ([]SeedanceAsset, error) {
	var out []SeedanceAsset
	for _, a := range f.rows {
		if a.Status == seedanceStatusFailedPermanent {
			continue
		}
		if a.Status == "active" && a.Version != nil && *a.Version == currentVersion {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

type fakeGroupStore struct {
	groups   map[int64]string
	versions map[int64]int
}

func (f *fakeGroupStore) GetSeedanceGroup(userID int64) (string, int, error) {
	return f.groups[userID], f.versions[userID], nil
}
func (f *fakeGroupStore) SetSeedanceGroup(userID int64, gid string, version int) error {
	if f.groups == nil {
		f.groups = map[int64]string{}
	}
	if f.versions == nil {
		f.versions = map[int64]int{}
	}
	f.groups[userID] = gid
	f.versions[userID] = version
	return nil
}

type fakeLibrarySeedanceClient struct {
	createGroupCalls []string
	createAssetCalls []struct{ groupID, url, name, assetType string }
	getAssetCalls    []string
	getStatuses      map[string]string
	createAssetErr   error
}

func (f *fakeLibrarySeedanceClient) CreateAssetGroup(ctx context.Context, sdKey, name, description string) (string, error) {
	f.createGroupCalls = append(f.createGroupCalls, name)
	return "group-" + name, nil
}
func (f *fakeLibrarySeedanceClient) CreateAsset(ctx context.Context, sdKey, groupID, url, name, assetType string) (string, error) {
	f.createAssetCalls = append(f.createAssetCalls, struct{ groupID, url, name, assetType string }{groupID, url, name, assetType})
	if f.createAssetErr != nil {
		return "", f.createAssetErr
	}
	return "Asset-new", nil
}
func (f *fakeLibrarySeedanceClient) GetAsset(ctx context.Context, sdKey, id string) (string, error) {
	f.getAssetCalls = append(f.getAssetCalls, id)
	return f.getStatuses[id], nil
}

type fakeLibrarySettings map[string]string

func (f fakeLibrarySettings) Get(key string) (string, error) { return f[key], nil }

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

func newSvc(repo SeedanceAssetsRepository, groups *fakeGroupStore, client seedanceAPI) *SeedanceService {
	return NewSeedanceService(repo, groups, client,
		enabledSettings(0), fakeLibrarySigner{})
}

// ---- Submit ----

func TestSubmitCreatesGroupAndAsset(t *testing.T) {
	repo := newFakeAssets()
	client := &fakeLibrarySeedanceClient{}
	svc := newSvc(repo, &fakeGroupStore{}, client)

	rec, err := svc.Submit(context.Background(), 7, "canvas/7/a.png", seedance.AssetTypeImage)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.createGroupCalls) != 1 || client.createGroupCalls[0] != "canvas-user-7" {
		t.Fatalf("应按用户建一个素材组，实际 = %+v", client.createGroupCalls)
	}
	if len(client.createAssetCalls) != 1 {
		t.Fatalf("CreateAsset 调用数 = %d，want 1", len(client.createAssetCalls))
	}
	// 关键断言：发给上游的必须是签出来的 https 直链，不是裸 object key。
	if client.createAssetCalls[0].url != "https://signed/canvas/7/a.png" {
		t.Fatalf("发给上游的 url = %q", client.createAssetCalls[0].url)
	}
	if rec.Status != "pending" || rec.AssetID == nil || *rec.AssetID != "Asset-new" {
		t.Fatalf("落库记录不对：%+v", rec)
	}
}

// 素材组按用户复用：同一用户的第二张图不再重复建组。
func TestSubmitReusesUserGroup(t *testing.T) {
	client := &fakeLibrarySeedanceClient{}
	// 素材组已存在且版本(1)= 当前版本 → 复用。
	svc := newSvc(newFakeAssets(), &fakeGroupStore{
		groups:   map[int64]string{7: "group-existing"},
		versions: map[int64]int{7: 1},
	}, client)

	if _, err := svc.Submit(context.Background(), 7, "canvas/7/a.png", seedance.AssetTypeImage); err != nil {
		t.Fatal(err)
	}
	if len(client.createGroupCalls) != 0 {
		t.Fatalf("已有素材组不该重建，实际 = %+v", client.createGroupCalls)
	}
	if client.createAssetCalls[0].groupID != "group-existing" {
		t.Fatalf("应复用已有组，实际 = %q", client.createAssetCalls[0].groupID)
	}
}

// 版本号 bump(常配合换 key/账号)：旧素材组作废，重建 group 并回写新 id + 新版本，
// asset 注册进新组——否则会往旧账号的组里注册导致 group 找不到。
func TestSubmitRecreatesGroupOnVersionBump(t *testing.T) {
	client := &fakeLibrarySeedanceClient{}
	groups := &fakeGroupStore{
		groups:   map[int64]string{7: "group-old"},
		versions: map[int64]int{7: 1}, // 旧组建于版本 1
	}
	// 当前版本调到 2（模拟 key 轮换后 bump 版本）。
	svc := NewSeedanceService(newFakeAssets(), groups, client,
		enabledSettings(2),
		signerStub(func(k string) string { return "https://cdn/" + k }))

	if _, err := svc.Submit(context.Background(), 7, "canvas/7/a.png", seedance.AssetTypeImage); err != nil {
		t.Fatal(err)
	}
	// CreateAssetGroup(name=canvas-user-7) → 返回 "group-canvas-user-7"。
	const wantGid = "group-canvas-user-7"
	if len(client.createGroupCalls) != 1 {
		t.Fatalf("版本 bump 应重建素材组一次，实际 = %+v", client.createGroupCalls)
	}
	if client.createAssetCalls[0].groupID != wantGid {
		t.Fatalf("asset 应注册进新组 %q，实际 = %q", wantGid, client.createAssetCalls[0].groupID)
	}
	if groups.groups[7] != wantGid || groups.versions[7] != 2 {
		t.Fatalf("应回写新组 id + 新版本(2)，实际 id=%q ver=%d", groups.groups[7], groups.versions[7])
	}
}

// 已 active 且版本没变 → 幂等返回，不重复占用上游配额。
func TestSubmitIdempotentWhenActive(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "active",
		Version: libraryPtrInt(1), AssetID: libraryPtrStr("Asset-old"),
	})
	client := &fakeLibrarySeedanceClient{}
	rec, err := newSvc(repo, &fakeGroupStore{}, client).Submit(context.Background(), 7, "k", seedance.AssetTypeImage)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.createAssetCalls) != 0 {
		t.Fatal("已通过的图不该重复提交")
	}
	if *rec.AssetID != "Asset-old" {
		t.Fatalf("应返回原记录，实际 %+v", rec)
	}
}

// 签不出 http(s) 直链时绝不发给上游——否则根因会被上游的 InvalidParameter.URL 掩盖。
func TestSubmitRefusesUnsignableURL(t *testing.T) {
	client := &fakeLibrarySeedanceClient{}
	svc := NewSeedanceService(newFakeAssets(), &fakeGroupStore{}, client,
		enabledSettings(0),
		signerStub(func(string) string { return "not-a-url" }))

	if _, err := svc.Submit(context.Background(), 7, "k", seedance.AssetTypeImage); err == nil {
		t.Fatal("签不出直链应报错")
	}
	if len(client.createAssetCalls) != 0 {
		t.Fatalf("不该把非 URL 发给上游，实际 = %+v", client.createAssetCalls)
	}
}

func TestSubmitNoKeyDisabled(t *testing.T) {
	svc := NewSeedanceService(newFakeAssets(), &fakeGroupStore{}, &fakeLibrarySeedanceClient{},
		fakeLibrarySettings{}, fakeLibrarySigner{})
	if _, err := svc.Submit(context.Background(), 7, "k", seedance.AssetTypeImage); !errors.Is(err, ErrSeedanceDisabled) {
		t.Fatalf("未配置 sd-key 应报 ErrSeedanceDisabled，实际 %v", err)
	}
}

// ---- Reconcile ----

func TestReconcilePollsPendingToActive(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "pending",
		Version: libraryPtrInt(1), AssetID: libraryPtrStr("Asset-1"),
	})
	client := &fakeLibrarySeedanceClient{getStatuses: map[string]string{"Asset-1": "Active"}}
	if err := newSvc(repo, &fakeGroupStore{}, client).ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.setStatus) != 1 || repo.setStatus[0].status != "active" {
		t.Fatalf("应转 active，实际 %+v", repo.setStatus)
	}
}

// 上游判定 Failed = 图片不合规，同图重查结果不变 → 终态。
func TestReconcileUpstreamFailedIsPermanent(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "pending",
		Version: libraryPtrInt(1), AssetID: libraryPtrStr("Asset-1"),
	})
	client := &fakeLibrarySeedanceClient{getStatuses: map[string]string{"Asset-1": "Failed"}}
	if err := newSvc(repo, &fakeGroupStore{}, client).ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.setError) != 1 || repo.setError[0].status != seedanceStatusFailedPermanent {
		t.Fatalf("应转终态，实际 %+v", repo.setError)
	}
	if !strings.Contains(repo.setError[0].msg, "不合规") {
		t.Fatalf("原因应说人话，实际 %q", repo.setError[0].msg)
	}
}

// 参数类错误同图重试必然同样失败 → 直接终态，不进重试窗口。
func TestReconcileInvalidParameterIsPermanent(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{ObjectKey: "k", UserID: 7, Status: "failed"})
	client := &fakeLibrarySeedanceClient{
		createAssetErr: &seedance.APIError{Code: "InvalidParameter.AspectRatioTooSmall", Detail: "bad"},
	}
	if err := newSvc(repo, &fakeGroupStore{}, client).ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.setError) != 1 || repo.setError[0].status != seedanceStatusFailedPermanent {
		t.Fatalf("参数类错误应直接终态，实际 %+v", repo.setError)
	}
}

// 兜底窗口：漏分类的错误也不能无限重试。
func TestReconcileRetryWindowExpires(t *testing.T) {
	old := time.Now().Add(-2 * seedanceRetryWindow)
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "failed",
		Error: libraryPtrStr("网络超时"), FirstFailedAt: libraryPtrTime(old),
	})
	client := &fakeLibrarySeedanceClient{}
	if err := newSvc(repo, &fakeGroupStore{}, client).ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.createAssetCalls) != 0 {
		t.Fatal("超过兜底窗口后不该再打上游")
	}
	if len(repo.setError) != 1 || repo.setError[0].status != seedanceStatusFailedPermanent {
		t.Fatalf("应强制转终态，实际 %+v", repo.setError)
	}
}

// 版本 bump 让旧 asset 作废 → 重新提交。
func TestReconcileStaleVersionResubmits(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "active",
		Version: libraryPtrInt(1), AssetID: libraryPtrStr("Asset-old"),
	})
	client := &fakeLibrarySeedanceClient{}
	svc := NewSeedanceService(repo, &fakeGroupStore{}, client,
		enabledSettings(2), fakeLibrarySigner{})
	if err := svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.createAssetCalls) != 1 {
		t.Fatalf("版本变了应重新提交，实际 %+v", client.createAssetCalls)
	}
}

func TestReconcileNoopWithoutKey(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{ObjectKey: "k", UserID: 7, Status: "pending"})
	client := &fakeLibrarySeedanceClient{}
	svc := NewSeedanceService(repo, &fakeGroupStore{}, client, fakeLibrarySettings{}, fakeLibrarySigner{})
	if err := svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.getAssetCalls) != 0 || len(client.createAssetCalls) != 0 {
		t.Fatal("未配置 sd-key 应整体 no-op")
	}
}

// ---- Resolve ----

func TestResolveActiveReturnsAssetRef(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "active", AssetID: libraryPtrStr("Asset-9"),
	})
	ref, err := newSvc(repo, &fakeGroupStore{}, &fakeLibrarySeedanceClient{}).Resolve(7, "k")
	if err != nil || ref != "asset://Asset-9" {
		t.Fatalf("ref=%q err=%v", ref, err)
	}
}

// 没校验过的图原样透传——这是绝大多数普通图片的路径。
func TestResolveUnverifiedPassesThrough(t *testing.T) {
	ref, err := newSvc(newFakeAssets(), &fakeGroupStore{}, &fakeLibrarySeedanceClient{}).Resolve(7, "k")
	if err != nil || ref != "" {
		t.Fatalf("ref=%q err=%v", ref, err)
	}
}

func TestResolvePendingReturnsNotReady(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{ObjectKey: "k", UserID: 7, Status: "pending"})
	_, err := newSvc(repo, &fakeGroupStore{}, &fakeLibrarySeedanceClient{}).Resolve(7, "k")
	if !errors.Is(err, ErrRealPersonNotReady) {
		t.Fatalf("pending 应报未就绪，实际 %v", err)
	}
}

func TestResolveFailedCarriesReason(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: seedanceStatusFailedPermanent,
		Error: libraryPtrStr("图片不合规"),
	})
	_, err := newSvc(repo, &fakeGroupStore{}, &fakeLibrarySeedanceClient{}).Resolve(7, "k")
	if !errors.Is(err, ErrRealPersonNotReady) || !strings.Contains(err.Error(), "不合规") {
		t.Fatalf("失败应带原因，实际 %v", err)
	}
}

// 别人的校验记录不复用——asset 归属于某个用户的素材组，跨用户引用会出问题。
func TestResolveOtherUserNotReused(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 99, Status: "active", AssetID: libraryPtrStr("Asset-9"),
	})
	ref, err := newSvc(repo, &fakeGroupStore{}, &fakeLibrarySeedanceClient{}).Resolve(7, "k")
	if err != nil || ref != "" {
		t.Fatalf("跨用户不该复用，ref=%q err=%v", ref, err)
	}
}

func TestSeedanceAssetName(t *testing.T) {
	// 8 位十六进制、确定性(同 key 同 name，保证 Submit 与重注册一致)。
	n1 := seedanceAssetName("tapnow/u37/2026/7/4f1d1bb5_下载 (2).png")
	if len(n1) != 8 {
		t.Fatalf("name 应为 8 位，实际 %d 位: %q", len(n1), n1)
	}
	for _, c := range n1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("name 应为十六进制，含非法字符: %q", n1)
		}
	}
	if seedanceAssetName("tapnow/u37/2026/7/4f1d1bb5_下载 (2).png") != n1 {
		t.Fatal("同一 objectKey 应得到同一 name(确定性)")
	}
}

// ---- 视频/音频（合规校验从图片扩到三类素材）----

// 类型必须原样透传给上游：传错会让上游按另一套规格校验，素材莫名被拒。
// 同时类型必须落库——worker 重试提交时只能从表里读回它。
func TestSubmitPassesAndPersistsAssetType(t *testing.T) {
	for _, at := range []string{seedance.AssetTypeVideo, seedance.AssetTypeAudio} {
		repo := newFakeAssets()
		client := &fakeLibrarySeedanceClient{}
		rec, err := newSvc(repo, &fakeGroupStore{}, client).Submit(context.Background(), 7, "canvas/7/a", at)
		if err != nil {
			t.Fatalf("Submit(%s): %v", at, err)
		}
		if len(client.createAssetCalls) != 1 || client.createAssetCalls[0].assetType != at {
			t.Fatalf("上游 AssetType = %+v, want %s", client.createAssetCalls, at)
		}
		if rec.AssetType != at || repo.rows["canvas/7/a"].AssetType != at {
			t.Fatalf("类型未落库: rec=%q row=%q want %s", rec.AssetType, repo.rows["canvas/7/a"].AssetType, at)
		}
	}
}

// worker 重试提交时把类型重放回上游。第一版忘了这件事，视频重试会被当图片提交。
func TestReconcileReplaysAssetTypeOnRetry(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "failed",
		AssetType: seedance.AssetTypeVideo, Version: libraryPtrInt(1),
	})
	client := &fakeLibrarySeedanceClient{}
	if err := newSvc(repo, &fakeGroupStore{}, client).ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.createAssetCalls) != 1 || client.createAssetCalls[0].assetType != seedance.AssetTypeVideo {
		t.Fatalf("重试未重放类型: %+v", client.createAssetCalls)
	}
}

// 不合规文案跟着类型走——用户看到「请更换图片」而手里是段视频只会困惑。
func TestReconcileFailedMessageUsesTypeLabel(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{
		ObjectKey: "k", UserID: 7, Status: "pending",
		AssetType: seedance.AssetTypeAudio, Version: libraryPtrInt(1),
		AssetID: libraryPtrStr("Asset-1"),
	})
	client := &fakeLibrarySeedanceClient{getStatuses: map[string]string{"Asset-1": "Failed"}}
	if err := newSvc(repo, &fakeGroupStore{}, client).ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.setError) != 1 || !strings.Contains(repo.setError[0].msg, "音频") {
		t.Fatalf("失败文案应提音频: %+v", repo.setError)
	}
}

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
	svc := newSvc(newFakeAssets(), &fakeGroupStore{}, &fakeLibrarySeedanceClient{})
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
	svc := newSvc(repo, &fakeGroupStore{}, &fakeLibrarySeedanceClient{})
	svc.SetPublicAssets(&fakePublicAssets{m: map[string]string{"pub-k": "pub-1"}})
	ref, err := svc.Resolve(7, "pub-k")
	if err != nil || ref != "asset://pub-1" {
		t.Fatalf("跨用户应落到公共库，ref=%q err=%v", ref, err)
	}
}

// 公共库也没有 → 维持「普通图片透传」的存量行为。
func TestResolvePublicMissStaysPassThrough(t *testing.T) {
	svc := newSvc(newFakeAssets(), &fakeGroupStore{}, &fakeLibrarySeedanceClient{})
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
	svc := newSvc(repo, &fakeGroupStore{}, &fakeLibrarySeedanceClient{})
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
	svc := newSvc(newFakeAssets(), &fakeGroupStore{}, &fakeLibrarySeedanceClient{})
	svc.SetPublicAssets(&fakePublicAssets{err: errors.New("db down")})
	ref, err := svc.Resolve(7, "k")
	if err != nil || ref != "" {
		t.Fatalf("公共库故障应降级透传，ref=%q err=%v", ref, err)
	}
}

// Status 只补缺不覆盖：用户自己的记录（哪怕 pending）原样保留，缺的才用公共库填。
func TestStatusFillsPublicForMissing(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{ObjectKey: "mine", UserID: 7, Status: "pending"})
	svc := newSvc(repo, &fakeGroupStore{}, &fakeLibrarySeedanceClient{})
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
	svc := newSvc(repo, &fakeGroupStore{}, &fakeLibrarySeedanceClient{})
	svc.SetPublicAssets(&fakePublicAssets{err: errors.New("db down")})
	out, err := svc.Status(7, []string{"mine", "pub-k"})
	if err != nil || out["mine"].Status != "active" {
		t.Fatalf("公共库故障不该影响自己的记录，out=%+v err=%v", out, err)
	}
}

// 没挂公共库（其他部署形态）→ 行为与从前逐字节一致。
func TestStatusWithoutPublicUnchanged(t *testing.T) {
	repo := newFakeAssets(SeedanceAsset{ObjectKey: "mine", UserID: 7, Status: "active"})
	svc := newSvc(repo, &fakeGroupStore{}, &fakeLibrarySeedanceClient{})
	out, err := svc.Status(7, []string{"mine", "other"})
	if err != nil || len(out) != 1 || out["mine"].Status != "active" {
		t.Fatalf("未挂公共库应与存量一致，out=%+v err=%v", out, err)
	}
}

// enabledSettings 是“合规接口已配置”的 settings：key、网关地址、三个路径齐全，version<=0 表示不写版本。
func enabledSettings(version int) fakeLibrarySettings {
	cfg := `{"createAssetGroupPath":"/g","createAssetPath":"/a","getAssetPath":"/s"`
	if version > 0 {
		cfg += `,"version":` + strconv.Itoa(version)
	}
	cfg += `}`
	return fakeLibrarySettings{
		settingUpstreamAPIKey:            "sk-x",
		settingBaseDomain:                "https://gw.example",
		seedance.SettingComplianceConfig: cfg,
	}
}

// spec S12 未定：本机素材签出来是回环地址，上游拉不到。必须就地拒绝，不发上游、不落行。
func TestSubmitRefusesLoopbackURL(t *testing.T) {
	for _, host := range []string{"127.0.0.1:5173", "localhost:9", "[::1]:80"} {
		repo := newFakeAssets()
		client := &fakeLibrarySeedanceClient{}
		svc := NewSeedanceService(repo, &fakeGroupStore{}, client, enabledSettings(0),
			signerStub(func(k string) string {
				return "http://" + host + "/media/" + strings.TrimPrefix(k, "media://") + "?token=t"
			}))
		_, err := svc.Submit(context.Background(), 7, "u7/a.png", seedance.AssetTypeImage)
		if !errors.Is(err, ErrLocalMediaNoPublicURL) {
			t.Fatalf("%s: err = %v, want ErrLocalMediaNoPublicURL", host, err)
		}
		if len(client.createAssetCalls) != 0 || len(client.createGroupCalls) != 0 || len(repo.upserts) != 0 {
			t.Fatalf("%s: 回环地址不应发往上游或落行", host)
		}
	}
}

func TestSubmitDisabledWhenAnyPieceMissing(t *testing.T) {
	full := enabledSettings(0)
	for _, drop := range []string{settingUpstreamAPIKey, settingBaseDomain, seedance.SettingComplianceConfig} {
		st := fakeLibrarySettings{}
		for k, v := range full {
			if k != drop {
				st[k] = v
			}
		}
		client := &fakeLibrarySeedanceClient{}
		svc := NewSeedanceService(newFakeAssets(), &fakeGroupStore{}, client, st, fakeLibrarySigner{})
		if _, err := svc.Submit(context.Background(), 7, "k", seedance.AssetTypeImage); !errors.Is(err, ErrSeedanceDisabled) {
			t.Fatalf("缺 %s: err = %v", drop, err)
		}
		if svc.Configured() {
			t.Fatalf("缺 %s 时不应视为已配置", drop)
		}
		if err := svc.ReconcileAll(context.Background()); err != nil || len(client.getAssetCalls) != 0 {
			t.Fatalf("缺 %s 时 worker 应 no-op: %v", drop, err)
		}
	}
	st := enabledSettings(0)
	st[seedance.SettingComplianceConfig] = `{"createAssetGroupPath":"/g","createAssetPath":"/a"}`
	svc := NewSeedanceService(newFakeAssets(), &fakeGroupStore{}, &fakeLibrarySeedanceClient{}, st, fakeLibrarySigner{})
	if svc.Configured() {
		t.Fatal("少一个路径也算未配置")
	}
}

func TestConfiguredWhenComplete(t *testing.T) {
	svc := NewSeedanceService(newFakeAssets(), &fakeGroupStore{}, &fakeLibrarySeedanceClient{}, enabledSettings(0), fakeLibrarySigner{})
	if !svc.Configured() {
		t.Fatal("配置齐全应视为已配置")
	}
}

// 前端直接展示这条错误文案（spec X5「未配置时入口提示“合规接口未配置”」）。
func TestErrSeedanceDisabledMentionsUnconfigured(t *testing.T) {
	if !strings.Contains(ErrSeedanceDisabled.Error(), "合规接口未配置") {
		t.Fatalf("文案 = %q", ErrSeedanceDisabled.Error())
	}
}

type fakeSeedancePub struct{ configured bool }

func (f fakeSeedancePub) Configured() bool { return f.configured }
func (f fakeSeedancePub) Publish(_ context.Context, key string) (string, error) {
	return "https://bucket.example.com/" + key, nil
}

func TestSeedanceSignedURLPublishesLoopback(t *testing.T) {
	svc := NewSeedanceService(newFakeAssets(), &fakeGroupStore{}, &fakeLibrarySeedanceClient{}, enabledSettings(0),
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
