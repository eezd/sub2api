package openaitransportplugin

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTransportPoolForwardsBodyHeadersAndResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.Equal(t, "payload", string(body))
		require.Equal(t, "enabled", request.Header.Get("X-Plugin-Test"))
		writer.Header().Add("X-Upstream", "first")
		writer.Header().Add("X-Upstream", "second")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("forwarded"))
	}))
	defer server.Close()

	config := DefaultConfig()
	config.ExtraHeaders = map[string]string{"X-Plugin-Test": "enabled"}
	pool := newTransportPool(config)
	defer pool.closeIdleConnections()
	transport, err := pool.transport("")
	require.NoError(t, err)
	request, err := newHTTPRequest(context.Background(), requestStart{
		Method: http.MethodPost, URL: server.URL, ContentLength: int64(len("payload")), HasBody: true,
	}, strings.NewReader("payload"), config.ExtraHeaders)
	require.NoError(t, err)
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, response.StatusCode)
	require.Equal(t, []string{"first", "second"}, response.Header.Values("X-Upstream"))
	require.Equal(t, "forwarded", string(body))
}

func TestNodeJS24TransportSendsConfiguredClientHello(t *testing.T) {
	type hello struct {
		protocols    []string
		versions     []uint16
		cipherSuites []uint16
	}
	captured := make(chan hello, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.TLS = &tls.Config{GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
		captured <- hello{
			protocols:    append([]string(nil), info.SupportedProtos...),
			versions:     append([]uint16(nil), info.SupportedVersions...),
			cipherSuites: append([]uint16(nil), info.CipherSuites...),
		}
		return nil, nil
	}}
	server.StartTLS()
	defer server.Close()

	transport, err := buildTransport(DefaultConfig(), "")
	require.NoError(t, err)
	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	_, requestErr := transport.RoundTrip(request)
	require.Error(t, requestErr, "self-signed fixture certificate must be rejected")
	transport.CloseIdleConnections()

	select {
	case observed := <-captured:
		require.Equal(t, []string{"http/1.1"}, observed.protocols)
		require.Equal(t, []uint16{0x0304, 0x0303}, observed.versions)
		require.Equal(t, []uint16{0x1301, 0x1302, 0x1303, 0xc02b, 0xc02f, 0xc02c, 0xc030, 0xcca9, 0xcca8, 0xc009, 0xc013, 0xc00a, 0xc014, 0x009c, 0x009d, 0x002f, 0x0035}, observed.cipherSuites)
	default:
		t.Fatal("TLS server did not observe the custom ClientHello")
	}
}

func TestBuildTransportAppliesTLSAndProxyPolicy(t *testing.T) {
	nodeConfig := DefaultConfig()
	nodeTransport, err := buildTransport(nodeConfig, "")
	require.NoError(t, err)
	require.NotNil(t, nodeTransport.DialTLSContext)
	require.False(t, nodeTransport.ForceAttemptHTTP2)
	require.NotNil(t, nodeTransport.TLSNextProto)

	_, err = buildTransport(nodeConfig, "https://proxy.example:8443")
	require.ErrorContains(t, err, "不支持 HTTPS 代理")

	goConfig := DefaultConfig()
	goConfig.ClientHelloProfile = ProfileGoDefault
	goTransport, err := buildTransport(goConfig, "https://proxy.example:8443")
	require.NoError(t, err)
	require.True(t, goTransport.ForceAttemptHTTP2)
	require.NotNil(t, goTransport.Proxy)
}

func TestProxyDisabledNeverParsesAccountProxy(t *testing.T) {
	config := DefaultConfig()
	config.ProxyMode = ProxyModeDisabled
	pool := newTransportPool(config)
	transport, err := pool.transport("://not-a-url")
	require.NoError(t, err)
	require.NotNil(t, transport)
}
