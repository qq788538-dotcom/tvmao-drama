package generation

import (
	"errors"
	"fmt"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

// 首次启动 settings 里还没有 base_domain / upstream_api_key（种子数据不含 Key）。
// 错误文案直接写进任务 error 列、显示在画布节点上，必须是用户能照着做的中文。
var (
	ErrUpstreamKeyNotConfigured = errors.New("未配置上游 API Key，请在设置中填写后重试")
	ErrBaseDomainNotConfigured  = errors.New("未配置上游地址（base_domain），请在设置中填写后重试")
)

// upstreamCredentials 现读本次调用要用的 Key 与上游地址（每次请求现读 settings）。
// 经 models.ResolveUpstream 解析：模型单独配了上游就用模型的，否则回落全局（Key 走 upstreamKey）。
func (s *Service) upstreamCredentials(userID int64, model *models.ModelConfig) (apiKey, baseDomain string, err error) {
	global := models.GlobalWithKey(s.settings, func() (string, error) {
		if s.upstreamKey == nil {
			return "", nil
		}
		return s.upstreamKey.Resolve(userID)
	})
	baseDomain, apiKey, err = models.ResolveUpstream(model, global)
	if err != nil {
		return "", "", fmt.Errorf("load upstream config: %w", err)
	}
	if apiKey == "" {
		return "", "", ErrUpstreamKeyNotConfigured
	}
	if baseDomain == "" {
		return "", "", ErrBaseDomainNotConfigured
	}
	return apiKey, baseDomain, nil
}
