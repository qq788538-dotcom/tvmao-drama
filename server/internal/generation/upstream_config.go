package generation

import (
	"errors"
	"fmt"
)

// 首次启动 settings 里还没有 base_domain / upstream_api_key（种子数据不含 Key，research §6）。
// 错误文案直接写进任务 error 列、显示在画布节点上，必须是用户能照着做的中文。
var (
	ErrUpstreamKeyNotConfigured = errors.New("未配置上游 API Key，请在设置中填写后重试")
	ErrBaseDomainNotConfigured  = errors.New("未配置上游地址（base_domain），请在设置中填写后重试")
)

// upstreamCredentials 现读本次调用要用的 Key 与上游地址（spec M2/M3：每次请求现读 settings）。
func (s *Service) upstreamCredentials(userID int64) (apiKey, baseDomain string, err error) {
	apiKey, err = s.upstreamKey.Resolve(userID)
	if err != nil {
		return "", "", fmt.Errorf("load upstream api key: %w", err)
	}
	if apiKey == "" {
		return "", "", ErrUpstreamKeyNotConfigured
	}
	baseDomain, err = s.settings.Get("base_domain")
	if err != nil {
		return "", "", fmt.Errorf("load base_domain: %w", err)
	}
	if baseDomain == "" {
		return "", "", ErrBaseDomainNotConfigured
	}
	return apiKey, baseDomain, nil
}
