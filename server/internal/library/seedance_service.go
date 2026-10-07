package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/seedance"
)

// seedanceAssetName 用 object_key 的 SHA-256 前 8 位十六进制做 Seedance 素材 name。
// 之前用 "canvas-"+完整 objectKey(含路径/中文/空格)，太长被上游直接拒。name 只是给人看的
// 标签、上游按 asset-id 引用，所以短标识即可；确定性哈希保证 Submit 与 worker 重注册
// 对同一图片得到同一 name。按要求只取 8 位，不考虑碰撞。
func seedanceAssetName(objectKey string) string {
	sum := sha256.Sum256([]byte(objectKey))
	return hex.EncodeToString(sum[:])[:8]
}

// 桌面端合规校验走 newapi（spec X5）：鉴权用全局 upstream_api_key，网关地址用 base_domain
// （经 models.ResolveUpstream(nil, …) 取，合规校验不绑定模型，所以恒为全局），
// 接口路径与版本号在 seedance_compliance_config 里（见 seedance.ComplianceConfig）。
const (
	settingUpstreamAPIKey = models.SettingUpstreamAPIKey
	settingBaseDomain     = models.SettingBaseDomain
)

// 以下都是窄接口，让 SeedanceService 只依赖它真正用到的那几个方法，测试可注入 fake。

// seedanceGroupStore 读写用户级素材组 id。校验脱离目录后没有别的分组依据，
// 而按用户分组正好对齐 asset 的归属边界。
type seedanceGroupStore interface {
	// 返回素材组 id + 建组时的版本号。版本用于换 key/账号后重建素材组，见 0083。
	GetSeedanceGroup(userID int64) (groupID string, version int, err error)
	SetSeedanceGroup(userID int64, groupID string, version int) error
}

// settingsReader 现读 settings（key 可能被设置页随时改）。
type settingsReader interface {
	Get(key string) (string, error)
}

// urlSigner 把 canonical 的 media://key 转成对外可访问的 HTTPS URL，供 Seedance 拉图。
// 它只认 media:// 形式，喂裸 object key 会原样吐回——那次入库全军覆没就是这么来的。
type urlSigner interface {
	Sign(string) string
}

// seedanceAPI 是 *seedance.Client 的接口形态，便于测试注入 fake。
type seedanceAPI interface {
	CreateAssetGroup(ctx context.Context, sdKey, name, description string) (string, error)
	CreateAsset(ctx context.Context, sdKey, groupID, url, name, assetType string) (string, error)
	GetAsset(ctx context.Context, sdKey, id string) (string, error)
}

// NormalizeAssetType 把前端传来的 kind 收敛成上游 AssetType，非法值返回 false。
// 白名单在这里而不是 handler：类型是决定上游按哪套规格校验的东西，让唯一的
// 写入路径守住它，比在每个入口各写一遍可靠。
func NormalizeAssetType(kind string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "image":
		return seedance.AssetTypeImage, true // 空 = 老前端只会提交图片，保持兼容
	case "video":
		return seedance.AssetTypeVideo, true
	case "audio":
		return seedance.AssetTypeAudio, true
	default:
		return "", false
	}
}

// assetTypeLabel 只用于给人看的错误文案。
func assetTypeLabel(assetType string) string {
	switch assetType {
	case seedance.AssetTypeVideo:
		return "视频"
	case seedance.AssetTypeAudio:
		return "音频"
	default:
		return "图片"
	}
}

type SeedanceService struct {
	repo     SeedanceAssetsRepository
	groups   seedanceGroupStore
	client   seedanceAPI
	settings settingsReader
	signer   urlSigner
	now      func() time.Time // 可注入，测试控制重试兜底窗口的时钟
	// public 是可选的平台公共资产兜底（画布 AI 角色库实现它）。查询顺序：先查
	// 用户自己的校验记录（seedance_assets），查不到才问它——所以对存量行为零
	// 影响；公共资产也永远进不了 seedance_assets，worker 不会碰它们（版本轮换
	// 重提交会把预置的官方 asset_id 覆盖掉）。为 nil = 没接公共库，行为与从前一致。
	public PublicSeedanceAssets
}

// PublicSeedanceAssets 按裸 object_key 批量查「平台预置且已过合规校验」资产的
// Seedance asset_id。查不到的 key 不出现在结果里。
type PublicSeedanceAssets interface {
	PublicAssetIDs(objectKeys []string) (map[string]string, error)
}

// SetPublicAssets 挂接公共资产兜底。放 setter 而不进构造函数：这是可选扩展点，
// 不该让所有 NewSeedanceService 调用方为它多传一个 nil。
func (s *SeedanceService) SetPublicAssets(p PublicSeedanceAssets) { s.public = p }

// publicAssetID 查单个 key 的公共 asset_id；没挂公共库或查不到返回空串。
// 查询出错也返回空串（记日志）——公共库故障只应让公共角色退化成普通图片，
// 不能把整条生成/状态链路拖垮。
func (s *SeedanceService) publicAssetID(objectKey string) string {
	if s.public == nil {
		return ""
	}
	m, err := s.public.PublicAssetIDs([]string{objectKey})
	if err != nil {
		log.Printf("seedance: 公共资产兜底查询失败 key=%s: %v", objectKey, err)
		return ""
	}
	return m[objectKey]
}

// seedanceStatusFailedPermanent 是失败终态：参数类错误（同图重试必然同样失败）、
// 上游判定不合规，或失败重试超过兜底窗口。不再进待办。
// 注意长度受 status VARCHAR(16) 约束，恰好 16 字符。
const seedanceStatusFailedPermanent = "failed_permanent"

// seedanceRetryWindow 是失败重试的兜底上限：不论错误类型，从首次失败起重试超过
// 该窗口即转终态。防止漏分类的错误无限轮询（30s 一拍打上游 + 刷日志）。
const seedanceRetryWindow = time.Hour

// mediaProtocol 与 assets.MediaProtocol 同值。这里不 import assets 包是为了不让
// library 依赖存储实现——它只需要知道「canonical URL 长这样」。
const mediaProtocol = "media://"

func NewSeedanceService(
	repo SeedanceAssetsRepository,
	groups seedanceGroupStore,
	client seedanceAPI,
	settings settingsReader,
	signer urlSigner,
) *SeedanceService {
	return &SeedanceService{repo: repo, groups: groups, client: client, settings: settings, signer: signer, now: time.Now}
}

// isPermanentSeedanceErr 判断错误是否是「同图重试必然同样失败」的参数类错误。
// 依赖 client 解析出的业务 Code；InvalidParameter.* 即认定永久（如宽高比越界）。
func isPermanentSeedanceErr(err error) bool {
	var apiErr *seedance.APIError
	return errors.As(err, &apiErr) && strings.HasPrefix(apiErr.Code, "InvalidParameter")
}

// config 现读 API Key + 版本号。网关地址、Key、三个接口路径任一缺失 → 返回空 key，
// 调用方据此报 ErrSeedanceDisabled（Submit）或直接不动作（worker）。
func (s *SeedanceService) config() (sdKey string, version int, err error) {
	cfg, err := seedance.LoadComplianceConfig(s.settings.Get)
	if err != nil {
		return "", 0, err
	}
	// 合规校验不属于任何模型，ResolveUpstream(nil, …) 即全局 base_domain + upstream_api_key。
	base, key, err := models.ResolveUpstream(nil, s.settings)
	if err != nil {
		return "", 0, err
	}
	if !cfg.Complete() || strings.TrimSpace(base) == "" || strings.TrimSpace(key) == "" {
		return "", cfg.EffectiveVersion(), nil
	}
	return key, cfg.EffectiveVersion(), nil
}

// Configured 供前端在入口处提示「合规接口未配置」（GET /seedance/config）。
func (s *SeedanceService) Configured() bool {
	key, _, err := s.config()
	return err == nil && key != ""
}

// ErrSeedanceDisabled：网关地址、API Key 或合规接口路径没配，校验功能整体关闭。
var ErrSeedanceDisabled = errors.New("合规接口未配置：请在设置中填写网关地址、API Key 和合规接口路径")

// ErrLocalMediaNoPublicURL：素材只能签出本机回环地址，上游拉不到（spec S12 待定）。
var ErrLocalMediaNoPublicURL = errors.New("本机素材暂无公网地址，暂不能提交合规校验")

// signedURL 把 object key 签成对外直链，并断言结果确实是 http(s)。
// 签不出直链时宁可就地失败给人话原因，也不要把一个不是 URL 的字符串发给上游——
// 那样根因会被上游晦涩的 InvalidParameter.URL 掩盖掉。
func (s *SeedanceService) signedURL(objectKey string) (string, error) {
	url := s.signer.Sign(mediaProtocol + objectKey)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", errors.New("无法生成素材直链，请检查存储配置")
	}
	// 桌面端 MediaSigner 签出的是 http://127.0.0.1:<端口>/media/…，发给上游必然拉取失败，
	// 且会被上游晦涩的 URL 错误掩盖根因。S12 定下公网方案前一律就地拒绝。
	if isLoopbackURL(url) {
		return "", ErrLocalMediaNoPublicURL
	}
	return url, nil
}

// isLoopbackURL 判断 URL 的主机是不是本机回环地址。
func isLoopbackURL(raw string) bool {
	u, err := neturl.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Submit 是用户在画布上点「Seedance2.0 合规校验」的入口：把这个素材提交到该用户的
// 素材组，落一行 pending，剩下的交给 worker 轮询。assetType 是上游 AssetType 原值。
// 幂等——已 active 且版本未变的直接返回，不重复占用上游配额。
func (s *SeedanceService) Submit(ctx context.Context, userID int64, objectKey, assetType string) (*SeedanceAsset, error) {
	sdKey, version, err := s.config()
	if err != nil {
		return nil, err
	}
	if sdKey == "" {
		return nil, ErrSeedanceDisabled
	}

	existing, err := s.repo.Get(objectKey)
	if err != nil && !errors.Is(err, ErrSeedanceAssetNotFound) {
		return nil, err
	}
	if existing != nil && existing.UserID == userID && existing.Status == "active" &&
		existing.Version != nil && *existing.Version == version &&
		existing.AssetID != nil && *existing.AssetID != "" {
		return existing, nil // 已通过且版本没变 → 无需重复校验
	}

	url, err := s.signedURL(objectKey)
	if err != nil {
		return nil, err
	}
	gid, err := s.ensureGroup(ctx, sdKey, version, userID)
	if err != nil {
		return nil, err
	}

	aid, err := s.client.CreateAsset(ctx, sdKey, gid, url, seedanceAssetName(objectKey), assetType)
	if err != nil {
		// 落一行失败态，让用户在节点角标上看得到原因，而不是只弹个 toast 就没了。
		status := "failed"
		if isPermanentSeedanceErr(err) {
			status = seedanceStatusFailedPermanent
		}
		_ = s.repo.Upsert(SeedanceAsset{ObjectKey: objectKey, UserID: userID, AssetType: assetType, Status: status})
		_ = s.repo.SetError(objectKey, status, "提交校验失败："+err.Error())
		return nil, err
	}
	rec := SeedanceAsset{
		ObjectKey: objectKey, UserID: userID,
		AssetID: &aid, Version: &version, AssetType: assetType, Status: "pending",
	}
	if err := s.repo.Upsert(rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// Status 供前端给画布节点上角标：没提交过校验返回 nil（不是错误）。
func (s *SeedanceService) Status(userID int64, objectKeys []string) (map[string]SeedanceAsset, error) {
	out, err := s.repo.GetMany(userID, objectKeys)
	if err != nil {
		return nil, err
	}
	if s.public == nil {
		return out, nil
	}
	// 用户自己的记录查不到的 key，兜一层公共资产——平台预置图对所有人都是
	// 「已通过校验」。只补缺不覆盖：用户自己的记录（含 pending/failed）优先。
	missing := make([]string, 0, len(objectKeys))
	for _, k := range objectKeys {
		if _, ok := out[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	pub, err := s.public.PublicAssetIDs(missing)
	if err != nil {
		// 公共库故障不拖垮整个状态查询——角标退化成「未校验」，生成侧同样降级。
		log.Printf("seedance: 公共资产兜底批量查询失败: %v", err)
		return out, nil
	}
	for k, id := range pub {
		aid := id
		out[k] = SeedanceAsset{
			ObjectKey: k,
			AssetID:   &aid,
			AssetType: seedance.AssetTypeImage,
			Status:    "active",
		}
	}
	return out, nil
}

// ensureGroup 惰性创建用户级素材组，并保证它与当前版本一致。
// 素材组已存在且版本相符 → 复用；不存在，或版本 bump(常配合换 key/账号)让旧组
// 作废 → 在当前 key 下重建，回写新 id + 新版本。这样 asset 重刷时不会往一个
// 属于旧账号、新 key 下不存在的组里注册（否则 CreateAsset 会 group 找不到而失败）。
func (s *SeedanceService) ensureGroup(ctx context.Context, sdKey string, version int, userID int64) (string, error) {
	gid, gver, err := s.groups.GetSeedanceGroup(userID)
	if err != nil {
		return "", err
	}
	if gid != "" && gver == version {
		return gid, nil
	}
	gid, err = s.client.CreateAssetGroup(ctx, sdKey, "canvas-user-"+strconv.FormatInt(userID, 10), "")
	if err != nil {
		return "", err
	}
	if err := s.groups.SetSeedanceGroup(userID, gid, version); err != nil {
		return "", err
	}
	return gid, nil
}

// RunWorker 在 main 里作为长生命周期 goroutine 启动：每 30s 收敛一次。
// 合规接口未配置时 ReconcileAll 自身 no-op，所以这里无脑转即可。
func (s *SeedanceService) RunWorker(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.ReconcileAll(ctx); err != nil {
				log.Printf("seedance: reconcile all: %v", err)
			}
		}
	}
}

// ReconcileAll 收敛所有待办行：pending 的查结果，failed 的重试提交。
// 扫描范围直接就是「表里没到终态的行」——不再需要遍历任何目录树。
func (s *SeedanceService) ReconcileAll(ctx context.Context) error {
	sdKey, version, err := s.config()
	if err != nil {
		return err
	}
	if sdKey == "" {
		return nil // 未配置 → Seedance 关闭
	}
	list, err := s.repo.ListPending(version)
	if err != nil {
		return err
	}
	for _, a := range list {
		s.reconcileOne(ctx, sdKey, version, a)
	}
	return nil
}

func (s *SeedanceService) reconcileOne(ctx context.Context, sdKey string, version int, a SeedanceAsset) {
	// 兜底窗口：不论什么错误（含漏分类的），从首次失败起重试超过 seedanceRetryWindow
	// 就强制转终态并停止上游调用——防无限轮询。
	if a.Status == "failed" && a.FirstFailedAt != nil && s.now().Sub(*a.FirstFailedAt) > seedanceRetryWindow {
		prev := ""
		if a.Error != nil {
			prev = *a.Error + "；"
		}
		log.Printf("seedance: %s 失败重试超过 %s，停止重试", a.ObjectKey, seedanceRetryWindow)
		_ = s.repo.SetError(a.ObjectKey, seedanceStatusFailedPermanent,
			prev+"重试 1 小时仍未成功，已停止；请更换"+assetTypeLabel(a.AssetType))
		return
	}

	stale := a.Version == nil || *a.Version != version
	if a.AssetID == nil || *a.AssetID == "" || stale {
		// 当前版本下还没有有效 asset（或版本 bump 让旧 asset 作废）→ 重新提交。
		url, err := s.signedURL(a.ObjectKey)
		if err != nil {
			log.Printf("seedance: %s 签不出直链: %v", a.ObjectKey, err)
			_ = s.repo.SetError(a.ObjectKey, "failed", err.Error())
			return
		}
		gid, err := s.ensureGroup(ctx, sdKey, version, a.UserID)
		if err != nil {
			log.Printf("seedance: ensure group for user %d: %v", a.UserID, err)
			_ = s.repo.SetError(a.ObjectKey, "failed", "创建素材组失败："+err.Error())
			return
		}
		// 类型从表里读回重放——重试这一刻没有前端上下文（见 0084）。
		aid, err := s.client.CreateAsset(ctx, sdKey, gid, url, seedanceAssetName(a.ObjectKey), a.AssetType)
		if err != nil {
			log.Printf("seedance: create asset for %s: %v", a.ObjectKey, err)
			status := "failed"
			if isPermanentSeedanceErr(err) {
				status = seedanceStatusFailedPermanent
			}
			_ = s.repo.SetError(a.ObjectKey, status, "提交校验失败："+err.Error())
			return
		}
		_ = s.repo.Upsert(SeedanceAsset{
			ObjectKey: a.ObjectKey, UserID: a.UserID,
			AssetID: &aid, Version: &version, AssetType: a.AssetType, Status: "pending",
		})
		return
	}

	// 当前版本下已提交、status=pending → 轮询。
	st, err := s.client.GetAsset(ctx, sdKey, *a.AssetID)
	if err != nil {
		log.Printf("seedance: get asset for %s: %v", a.ObjectKey, err)
		_ = s.repo.SetError(a.ObjectKey, "failed", "查询校验状态失败："+err.Error())
		return
	}
	switch st {
	case "Active":
		_ = s.repo.SetStatus(a.ObjectKey, "active")
	case "Failed":
		// 上游明确判定失败（审核未通过/处理失败）：同一素材重查结果不变 → 终态。
		label := assetTypeLabel(a.AssetType)
		_ = s.repo.SetError(a.ObjectKey, seedanceStatusFailedPermanent,
			"素材未通过 Seedance 合规校验（"+label+"不合规），请更换"+label)
	default:
		// 处理中 → 保持 pending，下一拍再查。
	}
}

// ErrRealPersonNotReady：素材提交过校验但尚未通过。generation handler 据此把
// 错误映射为 400 而非 502。名字沿用旧称，语义已从「真人图库图片未就绪」变成
// 「这个素材的合规校验未就绪」。
var ErrRealPersonNotReady = errors.New("素材未通过 Seedance 合规校验")

// Resolve 判定该 object_key 有没有可用的 Seedance asset：
// active → ("asset://<id>", nil)；从没校验过 → ("", nil)，调用方原样透传；
// 校验过但 pending/failed → ("", ErrRealPersonNotReady 包装错误)。
//
// 「从没校验过就原样透传」是这套东西对既有生成路径无侵入的根本原因：没人主动
// 校验过的素材（例如项目管理里的角色配音）永远走不到 asset:// 分支。
//
// 不再关心素材放在哪个目录——只要用户对它做过校验，画布、素材库、任何项目
// 里引用它都直接命中。
func (s *SeedanceService) Resolve(userID int64, objectKey string) (string, error) {
	a, err := s.repo.Get(objectKey)
	if errors.Is(err, ErrSeedanceAssetNotFound) {
		// 没校验过 → 先问公共资产库（画布 AI 角色这类平台预置图），
		// 也不是 → 当普通图片透传。
		if id := s.publicAssetID(objectKey); id != "" {
			return "asset://" + id, nil
		}
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if a.UserID != userID {
		// 别人的校验记录不复用；但公共资产人人可用。
		if id := s.publicAssetID(objectKey); id != "" {
			return "asset://" + id, nil
		}
		return "", nil
	}
	switch {
	case a.Status == "active" && a.AssetID != nil && *a.AssetID != "":
		return "asset://" + *a.AssetID, nil
	case strings.HasPrefix(a.Status, "failed"):
		msg := "合规校验未通过"
		if a.Error != nil && *a.Error != "" {
			msg = *a.Error
		}
		return "", fmt.Errorf("%w：%s", ErrRealPersonNotReady, msg)
	default:
		return "", fmt.Errorf("%w：%s正在进行 Seedance 合规校验，请稍候再试",
			ErrRealPersonNotReady, assetTypeLabel(a.AssetType))
	}
}
