package service

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

var errOpenAIPluginTLSProfileUnsupported = errors.New("OpenAI OAuth plugin binding does not support TLS ClientHello profiles")

func rejectOpenAIPluginTLSProfile(manager *PluginManager, account *Account, hasTLSProfile bool) error {
	if hasTLSProfile && manager != nil && manager.ShouldRouteOpenAIOAuth(account) {
		return errOpenAIPluginTLSProfileUnsupported
	}
	return nil
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (result *http.Response, resultErr error) {
	profile := s.resolveTLSProfile(account)
	if err := rejectOpenAIPluginTLSProfile(s.pluginManager, account, profile != nil); err != nil {
		return nil, err
	}
	request, timingTrace := requesttiming.StartAttempt(request, account.ID, openAIUpstreamTimingProxyID(proxyURL, account))
	defer func() { timingTrace.Response(result, resultErr) }()
	if profile != nil {
		return s.httpUpstream.DoWithTLS(request, proxyURL, account.ID, account.Concurrency, profile)
	}
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}

// openAIUpstreamTimingProxyID mirrors the egress attribution used by timing
// details: 0 is direct, a positive ID is the account proxy, -1 is unknown.
func openAIUpstreamTimingProxyID(proxyURL string, account *Account) int64 {
	if proxyURL == "" {
		return 0
	}
	if account != nil && account.Proxy != nil && proxyURL == account.Proxy.URL() {
		return account.Proxy.ID
	}
	return -1
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if useTLSFallback && s.tlsFPProfileService != nil {
		if profile := s.tlsFPProfileService.ResolveTLSProfile(account); profile != nil {
			if err := rejectOpenAIPluginTLSProfile(s.pluginManager, account, true); err != nil {
				return nil, err
			}
			return s.httpUpstream.DoWithTLS(request, proxyURL, account.ID, account.Concurrency, profile)
		}
	}
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	if useTLSFallback {
		return s.httpUpstream.DoWithTLS(request, proxyURL, account.ID, account.Concurrency, nil)
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}
