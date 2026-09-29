package openaitransportplugin

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

const (
	dialTimeout      = 10 * time.Second
	dialKeepAlive    = 30 * time.Second
	maxTransportPool = 256
)

type transportPool struct {
	config     Config
	transports map[string]*http.Transport
}

func newTransportPool(config Config) *transportPool {
	return &transportPool{config: config, transports: make(map[string]*http.Transport)}
}

func (p *transportPool) closeIdleConnections() {
	if p == nil {
		return
	}
	for _, transport := range p.transports {
		transport.CloseIdleConnections()
	}
}

func (p *transportPool) transport(proxyURL string) (*http.Transport, error) {
	if p == nil {
		return nil, errors.New("传输池未初始化")
	}
	if p.config.ProxyMode == ProxyModeDisabled {
		proxyURL = ""
	}
	proxyURL = strings.TrimSpace(proxyURL)
	if transport := p.transports[proxyURL]; transport != nil {
		return transport, nil
	}
	if len(p.transports) >= maxTransportPool {
		return nil, fmt.Errorf("代理传输池已达到 %d 个实例上限", maxTransportPool)
	}
	transport, err := buildTransport(p.config, proxyURL)
	if err != nil {
		return nil, err
	}
	p.transports[proxyURL] = transport
	return transport, nil
}

func buildTransport(config Config, rawProxyURL string) (*http.Transport, error) {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: dialKeepAlive}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: time.Duration(config.ResponseHeaderTimeoutSeconds) * time.Second,
		IdleConnTimeout:       time.Duration(config.IdleConnectionTimeoutSeconds) * time.Second,
		MaxIdleConns:          config.MaxIdleConnections,
		MaxIdleConnsPerHost:   config.MaxIdleConnectionsPerHost,
		MaxConnsPerHost:       config.MaxConnectionsPerHost,
	}

	var proxyURL *url.URL
	var err error
	if rawProxyURL != "" {
		proxyURL, err = url.Parse(rawProxyURL)
		if err != nil || proxyURL.Host == "" {
			return nil, errors.New("账号代理 URL 无效")
		}
		scheme := strings.ToLower(proxyURL.Scheme)
		switch scheme {
		case "http", "https":
		case "socks5", "socks5h":
			if config.ClientHelloProfile != ProfileNodeJS24 {
				return nil, errors.New("go_default 不支持 SOCKS5 账号代理")
			}
		default:
			return nil, fmt.Errorf("不支持账号代理协议 %q", scheme)
		}
	}

	if config.ClientHelloProfile == ProfileGoDefault {
		transport.ForceAttemptHTTP2 = true
		if proxyURL != nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
		return transport, nil
	}

	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	profile := &tlsfingerprint.Profile{Name: "Node.js 24.x", ALPNProtocols: []string{"http/1.1"}}
	if proxyURL == nil {
		transport.DialTLSContext = tlsfingerprint.NewDialer(profile, dialer.DialContext).DialTLSContext
		return transport, nil
	}

	switch strings.ToLower(proxyURL.Scheme) {
	case "http":
		transport.DialTLSContext = tlsfingerprint.NewHTTPProxyDialer(profile, proxyURL).DialTLSContext
	case "socks5", "socks5h":
		transport.DialTLSContext = tlsfingerprint.NewSOCKS5ProxyDialer(profile, proxyURL).DialTLSContext
	case "https":
		return nil, errors.New("nodejs_24 ClientHello 不支持 HTTPS 代理，请使用 HTTP 或 SOCKS5 代理")
	default:
		return nil, fmt.Errorf("nodejs_24 ClientHello 不支持代理协议 %q", proxyURL.Scheme)
	}
	return transport, nil
}

func newHTTPRequest(ctx context.Context, start requestStart, body io.Reader, extraHeaders map[string]string) (*http.Request, error) {
	parsed, err := url.Parse(start.URL)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("上游请求 URL 无效")
	}
	if !start.HasBody {
		body = nil
	}
	request, err := http.NewRequestWithContext(ctx, start.Method, parsed.String(), body)
	if err != nil {
		return nil, fmt.Errorf("创建上游请求: %w", err)
	}
	if !start.HasBody {
		request.Body = http.NoBody
	}
	request.Host = start.Host
	request.ContentLength = start.ContentLength
	request.Header = cloneHTTPHeaders(start.Headers)
	for name, value := range extraHeaders {
		request.Header.Set(name, value)
	}
	return request, nil
}

func cloneHTTPHeaders(input http.Header) http.Header {
	output := make(http.Header, len(input))
	for name, values := range input {
		output[name] = append([]string(nil), values...)
	}
	return output
}
