package cliapi

import (
	"context"
	"errors"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/library"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
)

// CLI 侧的 Seedance 2.0 合规校验。
//
// 为什么必须有：doubao-seedance* 的生成在 generation/execution_plan.go 里强制
// 要求输入图以 asset://<id> 引用，也就是**必须先过合规校验**。这条约束对网页和
// CLI 一视同仁（两边共用 generation 层）。而提交校验的端点原本只挂在网页
// auth group 下，aud=cli 会被反向拒绝——于是用 CLI 能把整张画布建好，一跑
// seedance 就卡住，还只能回网页对每张图手点一次。素材一多（6 张三视图 + 5 张
// 场景图）这就不是「稍微麻烦」而是走不通。
//
// 入口是**节点 ID** 而不是像网页那样的媒体 URL：CLI 用户手里本来就是节点 ID，
// 让他去复制一串带签名的临时 URL 既别扭又容易过期。服务端从快照里读节点的原始
// content（未签名），解析出 object_key——比网页那条「签名 URL 还原 key」的路更短。

// SeedanceGate 是合规校验需要的 library.SeedanceService 子集。
type SeedanceGate interface {
	Submit(ctx context.Context, userID int64, objectKey, assetType string) (*library.SeedanceAsset, error)
	Status(userID int64, objectKeys []string) (map[string]library.SeedanceAsset, error)
}

// KeyExtractor 从媒体引用还原对象存储 key。由 assets.Storage 实现。
type KeyExtractor interface {
	ExtractKey(url string) (string, bool)
}

// WithSeedance 注入合规校验依赖。不注入时相关接口明确报「未配置」而不是 nil panic。
func (s *Service) WithSeedance(gate SeedanceGate, keys KeyExtractor) *Service {
	s.seedance = gate
	s.mediaKeys = keys
	return s
}

// ComplianceState 是一个节点的合规校验状态。
//
// 不直接透传 library.SeedanceAsset：那里面有 object_key 和上游 assetId，
// 都是内部标识，CLI 契约只认节点 ID。
type ComplianceState struct {
	NodeID string `json:"nodeId"`
	// Kind 是媒体类型：image / video / audio。
	Kind string `json:"kind"`
	// Status 取值 pending / active / failed / failed_permanent；
	// 没有记录时是 unverified（还没提交过校验）。
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

const complianceUnverified = "unverified"

// 节点类型 → 上游媒体类型。
//
// 从节点类型推断而不是让调用方传：节点自己知道它挂的是什么，多一个参数就多一处
// 能传错的地方（传错的后果是上游按错误规格校验，通过了也用不了）。
var seedanceKindByNodeType = map[string]string{
	"input-image": "image",
	"gen-image":   "image",
	"input-audio": "audio",
	"gen-video":   "video",
	"video-input": "video",
}

// VerifyNodeMedia 提交某个节点的媒体去做合规校验。
//
// 幂等由 library.SeedanceService.Submit 保证：已通过的直接回原状态，不重复占用上游配额，
// 所以重复调用是安全的，CLI 侧不需要再包一层幂等键。Submit 同步调用内置模型 seedance-asset，
// 返回时已出结果：通过即 active，失败（含判定不合规）返回上游原文错误。
func (s *Service) VerifyNodeMedia(ctx context.Context, userID, projectID int64, nodeID string) (ComplianceState, error) {
	if s.seedance == nil || s.mediaKeys == nil {
		return ComplianceState{}, New(CodeInternal, "合规校验能力未配置")
	}
	kind, key, err := s.nodeMedia(userID, projectID, nodeID)
	if err != nil {
		return ComplianceState{}, err
	}
	assetType, ok := library.NormalizeAssetType(kind)
	if !ok {
		return ComplianceState{}, Errf(CodeInvalidArgument, "不支持的媒体类型 %q", kind)
	}
	rec, err := s.seedance.Submit(ctx, userID, key, assetType)
	if err != nil {
		if errors.Is(err, library.ErrSeedanceDisabled) {
			// 原样透出：文案已说明是内置模型 seedance-asset 未启用或上游未配置
			return ComplianceState{}, New(CodeUpstreamUnavailable, err.Error()).WithCause(err)
		}
		// 上游拒绝（含判定不合规）是调用方可理解的失败，不是服务器故障。
		return ComplianceState{}, New(CodeInvalidModelParams, err.Error()).WithCause(err)
	}
	return toComplianceState(nodeID, kind, rec), nil
}

// ListCompliance 一次给出项目里所有带媒体节点的校验状态。
//
// 不做成「查单个节点」：真实场景是提交完一批素材后盯着它们什么时候变 active，
// 一个个查要 N 条命令，还看不出整体进度。
func (s *Service) ListCompliance(userID, projectID int64) ([]ComplianceState, error) {
	if s.seedance == nil || s.mediaKeys == nil {
		return nil, New(CodeInternal, "合规校验能力未配置")
	}
	doc, err := s.snapshotDoc(userID, projectID)
	if err != nil {
		return nil, err
	}
	nodes, _ := doc["nodes"].([]any)

	out := []ComplianceState{}
	keyByNode := map[string]string{}
	kindByNode := map[string]string{}
	keys := make([]string, 0, len(nodes))
	for _, item := range nodes {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		nodeID := str(raw["id"])
		kind, ok := seedanceKindByNodeType[str(raw["type"])]
		if !ok {
			continue
		}
		key, ok := s.mediaKeys.ExtractKey(str(raw["content"]))
		if !ok || key == "" {
			continue // 还没生成出内容的节点没什么可校验的
		}
		keyByNode[nodeID] = key
		kindByNode[nodeID] = kind
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return out, nil
	}
	byKey, err := s.seedance.Status(userID, keys)
	if err != nil {
		return nil, New(CodeInternal, "查询合规校验状态失败").WithCause(err)
	}
	// 按快照里的节点顺序输出，保证多次调用结果稳定（map 遍历顺序是随机的）。
	for _, item := range nodes {
		raw, _ := item.(map[string]any)
		nodeID := str(raw["id"])
		key, ok := keyByNode[nodeID]
		if !ok {
			continue
		}
		rec, found := byKey[key]
		if !found {
			out = append(out, ComplianceState{
				NodeID: nodeID, Kind: kindByNode[nodeID], Status: complianceUnverified,
			})
			continue
		}
		out = append(out, toComplianceState(nodeID, kindByNode[nodeID], &rec))
	}
	return out, nil
}

// nodeMedia 定位节点并解析出它的对象存储 key。
func (s *Service) nodeMedia(userID, projectID int64, nodeID string) (string, string, error) {
	doc, err := s.snapshotDoc(userID, projectID)
	if err != nil {
		return "", "", err
	}
	raw, ok := FindNode(doc, nodeID)
	if !ok {
		return "", "", Errf(CodeNodeNotFound, "节点 %s 不存在", nodeID)
	}
	internal := str(raw["type"])
	kind, ok := seedanceKindByNodeType[internal]
	if !ok {
		semantic, _ := SemanticType(internal)
		return "", "", Errf(CodeInvalidNodeType,
			"节点 %s 的类型 %q 没有可校验的媒体", nodeID, semantic)
	}
	content := str(raw["content"])
	if content == "" {
		return "", "", Errf(CodeInvalidNodeType,
			"节点 %s 还没有内容，先生成或上传素材再提交校验", nodeID)
	}
	// 这里读的是**未签名**的原始 content。网页那条路拿到的是带签名的临时 URL，
	// 得先还原 key；CLI 直接从快照读，少一次还原。
	key, ok := s.mediaKeys.ExtractKey(content)
	if !ok || key == "" {
		return "", "", Errf(CodeInvalidNodeType,
			"无法识别节点 %s 的素材，请重新生成或上传", nodeID)
	}
	return kind, key, nil
}

// snapshotDoc 取项目当前画布的原始 doc（含归属校验）。
// 项目存在但没保存过画布时回空画布，不是错误——与 GetCanvas 一致。
func (s *Service) snapshotDoc(userID, projectID int64) (map[string]any, error) {
	if _, err := s.project(userID, projectID); err != nil {
		return nil, err
	}
	snap, err := s.projects.GetLatest(projectID)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			return map[string]any{"nodes": []any{}}, nil
		}
		return nil, New(CodeInternal, "读取画布失败").WithCause(err)
	}
	if snap == nil {
		return map[string]any{"nodes": []any{}}, nil
	}
	return decodeDoc(snap.Payload)
}

func toComplianceState(nodeID, kind string, rec *library.SeedanceAsset) ComplianceState {
	st := ComplianceState{NodeID: nodeID, Kind: kind, Status: complianceUnverified}
	if rec == nil {
		return st
	}
	st.Status = rec.Status
	if rec.Error != nil {
		st.Error = *rec.Error
	}
	return st
}
