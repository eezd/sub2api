package openaitransportplugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/net/http/httpguts"
)

const (
	ProfileNodeJS24  = "nodejs_24"
	ProfileGoDefault = "go_default"

	ProxyModeInherit  = "inherit"
	ProxyModeDisabled = "disabled"
)

// Config is the complete persisted configuration for the OpenAI transport.
type Config struct {
	ClientHelloProfile           string            `json:"client_hello_profile"`
	ProxyMode                    string            `json:"proxy_mode"`
	RequestTimeoutSeconds        int               `json:"request_timeout_seconds"`
	ResponseHeaderTimeoutSeconds int               `json:"response_header_timeout_seconds"`
	IdleConnectionTimeoutSeconds int               `json:"idle_connection_timeout_seconds"`
	MaxIdleConnections           int               `json:"max_idle_connections"`
	MaxIdleConnectionsPerHost    int               `json:"max_idle_connections_per_host"`
	MaxConnectionsPerHost        int               `json:"max_connections_per_host"`
	ExtraHeaders                 map[string]string `json:"extra_headers"`
}

func DefaultConfig() Config {
	return Config{
		ClientHelloProfile:           ProfileNodeJS24,
		ProxyMode:                    ProxyModeInherit,
		RequestTimeoutSeconds:        0,
		ResponseHeaderTimeoutSeconds: 60,
		IdleConnectionTimeoutSeconds: 90,
		MaxIdleConnections:           100,
		MaxIdleConnectionsPerHost:    20,
		MaxConnectionsPerHost:        0,
		ExtraHeaders:                 map[string]string{},
	}
}

func DecodeConfig(raw []byte) (Config, error) {
	cfg := DefaultConfig()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Config{}, errors.New("配置必须是 JSON 对象")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Config{}, errors.New("配置只能包含一个 JSON 对象")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	cfg.ExtraHeaders = cloneHeaders(cfg.ExtraHeaders)
	return cfg, nil
}

func (c Config) Validate() error {
	switch c.ClientHelloProfile {
	case ProfileNodeJS24, ProfileGoDefault:
	default:
		return errors.New("client_hello_profile 必须是 nodejs_24 或 go_default")
	}
	switch c.ProxyMode {
	case ProxyModeInherit, ProxyModeDisabled:
	default:
		return errors.New("proxy_mode 必须是 inherit 或 disabled")
	}
	if c.RequestTimeoutSeconds < 0 || c.RequestTimeoutSeconds > 3600 {
		return errors.New("request_timeout_seconds 必须在 0 到 3600 之间")
	}
	if c.ResponseHeaderTimeoutSeconds < 1 || c.ResponseHeaderTimeoutSeconds > 600 {
		return errors.New("response_header_timeout_seconds 必须在 1 到 600 之间")
	}
	if c.IdleConnectionTimeoutSeconds < 1 || c.IdleConnectionTimeoutSeconds > 3600 {
		return errors.New("idle_connection_timeout_seconds 必须在 1 到 3600 之间")
	}
	if c.MaxIdleConnections < 0 || c.MaxIdleConnections > 10000 {
		return errors.New("max_idle_connections 必须在 0 到 10000 之间")
	}
	if c.MaxIdleConnectionsPerHost < 0 || c.MaxIdleConnectionsPerHost > 1000 {
		return errors.New("max_idle_connections_per_host 必须在 0 到 1000 之间")
	}
	if c.MaxConnectionsPerHost < 0 || c.MaxConnectionsPerHost > 1000 {
		return errors.New("max_connections_per_host 必须在 0 到 1000 之间")
	}
	if len(c.ExtraHeaders) > 32 {
		return errors.New("extra_headers 最多允许 32 个请求头")
	}
	canonicalNames := make(map[string]struct{}, len(c.ExtraHeaders))
	for name, value := range c.ExtraHeaders {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !httpguts.ValidHeaderFieldName(canonical) || canonical != http.CanonicalHeaderKey(name) {
			return fmt.Errorf("extra_headers 包含无效请求头名称: %q", name)
		}
		if _, exists := canonicalNames[canonical]; exists {
			return fmt.Errorf("extra_headers 包含重复请求头名称: %s", canonical)
		}
		canonicalNames[canonical] = struct{}{}
		if isProtectedHeader(canonical) {
			return fmt.Errorf("extra_headers 不允许覆盖受保护请求头: %s", canonical)
		}
		if len(value) > 4096 || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("extra_headers 请求头值无效: %s", canonical)
		}
	}
	return nil
}

func (c Config) NormalizedJSON() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	copy := c
	copy.ExtraHeaders = cloneHeaders(c.ExtraHeaders)
	if copy.ExtraHeaders == nil {
		copy.ExtraHeaders = map[string]string{}
	}
	return json.Marshal(copy)
}

func cloneHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(input))
	for name, value := range input {
		out[http.CanonicalHeaderKey(strings.TrimSpace(name))] = value
	}
	return out
}

func isProtectedHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Authorization", "Proxy-Authorization", "Proxy-Connection", "Host", "Content-Length", "Connection", "Keep-Alive", "Te", "Transfer-Encoding", "Upgrade", "Trailer", "Cookie", "Set-Cookie":
		return true
	default:
		return false
	}
}
