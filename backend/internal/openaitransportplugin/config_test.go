package openaitransportplugin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeConfigNormalizesCompleteDefaults(t *testing.T) {
	config, err := DecodeConfig([]byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, ProfileNodeJS24, config.ClientHelloProfile)
	require.Equal(t, ProxyModeInherit, config.ProxyMode)
	require.Equal(t, 60, config.ResponseHeaderTimeoutSeconds)
	require.Equal(t, 90, config.IdleConnectionTimeoutSeconds)
	require.NotNil(t, config.ExtraHeaders)

	normalized, err := config.NormalizedJSON()
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(normalized, &fields))
	for _, name := range []string{
		"client_hello_profile", "proxy_mode", "request_timeout_seconds",
		"response_header_timeout_seconds", "idle_connection_timeout_seconds",
		"max_idle_connections", "max_idle_connections_per_host",
		"max_connections_per_host", "extra_headers",
	} {
		require.Contains(t, fields, name)
	}
}

func TestDecodeConfigRejectsUnknownAndProtectedHeaders(t *testing.T) {
	_, err := DecodeConfig([]byte(`{"unexpected":true}`))
	require.ErrorContains(t, err, "unknown field")

	_, err = DecodeConfig([]byte(`null`))
	require.ErrorContains(t, err, "JSON 对象")

	_, err = DecodeConfig([]byte(`{"extra_headers":{"x-plugin-test":"first","X-Plugin-Test":"second"}}`))
	require.ErrorContains(t, err, "重复请求头名称")

	_, err = DecodeConfig([]byte(`{"extra_headers":{"Authorization":"Bearer replacement"}}`))
	require.ErrorContains(t, err, "受保护请求头")

	_, err = DecodeConfig([]byte(`{"extra_headers":{"Bad Header":"value"}}`))
	require.ErrorContains(t, err, "无效请求头名称")
}

func TestDecodeConfigCanonicalizesAndOwnsHeaderMap(t *testing.T) {
	config, err := DecodeConfig([]byte(`{"extra_headers":{"x-plugin-test":"enabled"}}`))
	require.NoError(t, err)
	require.Equal(t, map[string]string{"X-Plugin-Test": "enabled"}, config.ExtraHeaders)

	copy := cloneHeaders(config.ExtraHeaders)
	copy["X-Plugin-Test"] = "changed"
	require.Equal(t, "enabled", config.ExtraHeaders["X-Plugin-Test"])
}

func TestConfigRejectsResourceAndTimeoutBounds(t *testing.T) {
	tests := []string{
		`{"request_timeout_seconds":3601}`,
		`{"response_header_timeout_seconds":0}`,
		`{"idle_connection_timeout_seconds":3601}`,
		`{"max_idle_connections":10001}`,
		`{"max_idle_connections_per_host":1001}`,
		`{"max_connections_per_host":1001}`,
	}
	for _, raw := range tests {
		_, err := DecodeConfig([]byte(raw))
		require.Error(t, err, raw)
	}
}
