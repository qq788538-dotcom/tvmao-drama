package library

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	neturl "net/url"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/seedance"
)

// urlSigner 把 canonical 的 media://key 转成对外可访问的 HTTPS URL，供 Seedance 拉图。
// 它只认 media:// 形式，喂裸 object key 会原样吐回——那次入库全军覆没就是这么来的。
type urlSigner interface {
	Sign(string) string
}

// seedanceAPI 是 *seedance.Registrar 的接口形态，便于测试注入 fake。
type seedanceAPI interface {
	Configured() bool
	Register(ctx context.Context, url, assetType string) (string, error)
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
	repo   SeedanceAssetsRepository
	client seedanceAPI
	signer urlSigner
	// publisher 把本机素材发布到用户的对象存储；nil = 回环地址一律报错。
	publisher MediaPublisher
	// public 是可选的平台公共资产兜底（画布 AI 角色库实现它）。查询顺序：先查
	// 用户自己的校验记录（seedance_assets），查不到才问它——所以对存量行为零
	// 影响；公共资产也永远进不了 seedance_assets，也不会被校验流程覆盖。为 nil = 没接公共库，行为与从前一致。
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

// seedanceStatusFailedPermanent：校验失败（上游原文见 error）；用户可再次提交。
// 注意长度受 status VARCHAR(16) 约束，恰好 16 字符。
const seedanceStatusFailedPermanent = "failed_permanent"

// mediaProtocol 与 assets.MediaProtocol 同值。这里不 import assets 包是为了不让
// library 依赖存储实现——它只需要知道「canonical URL 长这样」。
const mediaProtocol = "media://"

func NewSeedanceService(repo SeedanceAssetsRepository, client seedanceAPI, signer urlSigner) *SeedanceService {
	return &SeedanceService{repo: repo, client: client, signer: signer}
}

// Configured 供前端在入口处提示「合规校验未启用」（GET /seedance/config）。
func (s *SeedanceService) Configured() bool { return s.client.Configured() }

// ErrSeedanceDisabled：内置模型 seedance-asset 未启用或上游未配置，校验功能整体关闭。
var ErrSeedanceDisabled = errors.New("合规校验未启用：内置模型 seedance-asset 未启用或上游地址、API Key 未配置")

// MediaPublisher 把本机媒体发布到用户配置的对象存储（*objectstore.Publisher 满足）。
type MediaPublisher interface {
	Configured() bool
	Publish(ctx context.Context, mediaKey string) (string, error)
}

// SetMediaPublisher 注入对象存储发布器；nil = 回环地址一律报错。
func (s *SeedanceService) SetMediaPublisher(p MediaPublisher) { s.publisher = p }

// ErrLocalMediaNoPublicURL：素材只能签出本机回环地址，且未配置对象存储。
var ErrLocalMediaNoPublicURL = errors.New("未配置对象存储：合规校验需要把素材上传到你自己的对象存储，请在 设置 → 配置 → 对象存储 中填写")

// signedURL 把 object key 签成对外直链，并断言结果确实是 http(s)。
// 签不出直链时宁可就地失败给人话原因，也不要把一个不是 URL 的字符串发给上游——
// 那样根因会被上游晦涩的 InvalidParameter.URL 掩盖掉。
func (s *SeedanceService) signedURL(ctx context.Context, objectKey string) (string, error) {
	url := s.signer.Sign(mediaProtocol + objectKey)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", errors.New("无法生成素材直链，请检查存储配置")
	}
	// 桌面端 MediaSigner 签出的是 http://127.0.0.1:<端口>/media/…，发给上游必然拉取失败，
	// 且会被上游晦涩的 URL 错误掩盖根因。已配置对象存储则发布后取公网地址，否则就地拒绝。
	if isLoopbackURL(url) {
		if s.publisher == nil || !s.publisher.Configured() {
			return "", ErrLocalMediaNoPublicURL
		}
		return s.publisher.Publish(ctx, objectKey)
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

// Submit 是用户在画布上点「Seedance2.0 合规校验」的入口：同步调用内置模型
// seedance-asset 注册素材，拿到 asset_id 即通过（seedance-asset 设计 A2）。
// 已 active 的直接返回；其余状态（含 pending、failed_permanent）一律重新注册。
// assetType 是上游 AssetType 原值。
//
// 注册前先写一条临时 pending 行（终审 I3）：同步调用可能长达数分钟，期间节点角标
// 靠它显示「正在进行」并触发前端 5 秒轮询，生成侧 Resolve 也据此报「请稍候」，
// 而不是把未校验的签名 URL 原样透传。注册调用免费，不做在途去重；应用崩溃遗留的
// pending 行再点一次校验即可覆盖。
func (s *SeedanceService) Submit(ctx context.Context, userID int64, objectKey, assetType string) (*SeedanceAsset, error) {
	if !s.client.Configured() {
		return nil, ErrSeedanceDisabled
	}
	existing, err := s.repo.Get(objectKey)
	if err != nil && !errors.Is(err, ErrSeedanceAssetNotFound) {
		return nil, err
	}
	if existing != nil && existing.UserID == userID && existing.Status == "active" &&
		existing.AssetID != nil && *existing.AssetID != "" {
		return existing, nil // 已通过 → 不重复占用上游配额
	}
	url, err := s.signedURL(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	// 临时 pending（Upsert 会清空旧 error）；写失败就不调上游，免得结果无处落库。
	if err := s.repo.Upsert(SeedanceAsset{ObjectKey: objectKey, UserID: userID, AssetType: assetType, Status: "pending"}); err != nil {
		return nil, err
	}
	// 同步注册：拿到 asset_id 即通过；失败原样记录上游原文，让节点角标看得到原因。
	aid, err := s.client.Register(ctx, url, assetType)
	if err != nil {
		_ = s.repo.Upsert(SeedanceAsset{ObjectKey: objectKey, UserID: userID, AssetType: assetType, Status: seedanceStatusFailedPermanent})
		_ = s.repo.SetError(objectKey, seedanceStatusFailedPermanent, err.Error())
		return nil, err
	}
	version := 1
	rec := SeedanceAsset{ObjectKey: objectKey, UserID: userID, AssetID: &aid, Version: &version, AssetType: assetType, Status: "active"}
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
