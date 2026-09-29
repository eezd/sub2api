package openaitransportplugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	hcplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
)

const (
	PluginID   = "local.sub2api.openai-transport"
	Capability = "openai.oauth.outbound_transport.v1"
	probeURL   = "https://chatgpt.com/backend-api/models"
)

type requestStart struct {
	Method        string
	URL           string
	Host          string
	Headers       http.Header
	ProxyURL      string
	ContentLength int64
	HasBody       bool
}

type Plugin struct {
	pluginv1.UnimplementedTransportPluginServer

	version string

	mu       sync.Mutex
	config   Config
	pool     *transportPool
	broker   *hcplugin.GRPCBroker
	host     pluginv1.HostServiceClient
	hostConn *grpc.ClientConn

	activeRequests atomic.Int64
	requestsTotal  atomic.Uint64
	errorsTotal    atomic.Uint64
	bytesSent      atomic.Uint64
	bytesReceived  atomic.Uint64
	lastRequestAt  atomic.Int64
}

func New(version string) *Plugin {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "0.0.0-dev"
	}
	config := DefaultConfig()
	return &Plugin{version: version, config: config, pool: newTransportPool(config)}
}

func (p *Plugin) SetHostBroker(broker *hcplugin.GRPCBroker) {
	p.mu.Lock()
	p.broker = broker
	p.mu.Unlock()
}

func (p *Plugin) GetInfo(context.Context, *pluginv1.GetInfoRequest) (*pluginv1.GetInfoResponse, error) {
	return &pluginv1.GetInfoResponse{
		PluginId:            PluginID,
		PluginVersion:       p.version,
		ProtocolVersion:     pluginv1.ProtocolVersion,
		TransportApiVersion: pluginv1.TransportAPIVersion,
		Capabilities:        []string{Capability},
	}, nil
}

func (p *Plugin) Health(context.Context, *pluginv1.HealthRequest) (*pluginv1.HealthResponse, error) {
	status, err := p.statusJSON()
	if err != nil {
		return &pluginv1.HealthResponse{Healthy: false, Message: "无法生成运行状态"}, nil
	}
	return &pluginv1.HealthResponse{Healthy: true, Message: "OpenAI Transport 运行正常", StatusJson: string(status)}, nil
}

func (p *Plugin) ValidateConfig(_ context.Context, request *pluginv1.ValidateConfigRequest) (*pluginv1.ValidateConfigResponse, error) {
	if request == nil {
		return &pluginv1.ValidateConfigResponse{Valid: false, Message: "配置为空"}, nil
	}
	config, err := DecodeConfig(request.ConfigJson)
	if err != nil {
		return &pluginv1.ValidateConfigResponse{Valid: false, Message: err.Error()}, nil
	}
	normalized, err := config.NormalizedJSON()
	if err != nil {
		return &pluginv1.ValidateConfigResponse{Valid: false, Message: err.Error()}, nil
	}
	return &pluginv1.ValidateConfigResponse{Valid: true, Message: "配置有效", NormalizedConfigJson: normalized}, nil
}

func (p *Plugin) ApplyConfig(_ context.Context, request *pluginv1.ApplyConfigRequest) (*pluginv1.ApplyConfigResponse, error) {
	if request == nil {
		return &pluginv1.ApplyConfigResponse{Applied: false, Message: "配置为空"}, nil
	}
	config, err := DecodeConfig(request.ConfigJson)
	if err != nil {
		return &pluginv1.ApplyConfigResponse{Applied: false, Message: err.Error()}, nil
	}
	pool := newTransportPool(config)
	p.mu.Lock()
	previous := p.pool
	p.config = config
	p.pool = pool
	p.mu.Unlock()
	previous.closeIdleConnections()
	return &pluginv1.ApplyConfigResponse{Applied: true, Message: "配置已应用"}, nil
}

func (p *Plugin) InitHostServices(_ context.Context, request *pluginv1.InitHostServicesRequest) (*pluginv1.InitHostServicesResponse, error) {
	if request == nil || request.HostServiceApiVersion != pluginv1.HostServiceAPIVersion {
		return &pluginv1.InitHostServicesResponse{Ready: false, Message: "宿主服务协议不兼容"}, nil
	}
	p.mu.Lock()
	broker := p.broker
	p.mu.Unlock()
	if broker == nil {
		return &pluginv1.InitHostServicesResponse{Ready: false, Message: "宿主服务 Broker 不可用"}, nil
	}
	connection, err := broker.Dial(request.HostServiceId)
	if err != nil {
		return &pluginv1.InitHostServicesResponse{Ready: false, Message: "无法连接宿主服务"}, nil
	}
	p.mu.Lock()
	previous := p.hostConn
	p.hostConn = connection
	p.host = pluginv1.NewHostServiceClient(connection)
	p.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return &pluginv1.InitHostServicesResponse{Ready: true, Message: "宿主服务已连接"}, nil
}

func (p *Plugin) TestConfig(ctx context.Context, request *pluginv1.TestConfigRequest) (*pluginv1.TestConfigResponse, error) {
	if request == nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: "配置为空"}, nil
	}
	config, err := DecodeConfig(request.ConfigJson)
	if err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: err.Error()}, nil
	}
	p.mu.Lock()
	host := p.host
	p.mu.Unlock()
	if host == nil {
		return &pluginv1.TestConfigResponse{Success: true, Message: "配置有效；宿主未提供账号目录，未执行上游探测"}, nil
	}

	started := time.Now()
	accounts, err := host.ListAccounts(ctx, &pluginv1.ListAccountsRequest{Platform: "openai", AccountType: "oauth"})
	if err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: "读取 OpenAI OAuth 账号失败"}, nil
	}
	if accounts == nil || len(accounts.AccountIds) == 0 {
		return &pluginv1.TestConfigResponse{Success: false, Message: "没有可用于连通性测试的 OpenAI OAuth 账号"}, nil
	}
	identity, err := host.ResolveOutboundIdentity(ctx, &pluginv1.ResolveOutboundIdentityRequest{AccountId: accounts.AccountIds[0]})
	if err != nil || identity == nil || !identity.Found {
		return &pluginv1.TestConfigResponse{Success: false, Message: "无法解析 OpenAI OAuth 出站身份"}, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	headers := pluginHeadersToHTTP(identity.Headers)
	if headers.Get("Authorization") == "" && strings.TrimSpace(identity.Token) != "" {
		headers.Set("Authorization", "Bearer "+strings.TrimSpace(identity.Token))
	}
	probe, err := newHTTPRequest(probeCtx, requestStart{
		Method: http.MethodGet, URL: probeURL, Headers: headers,
	}, nil, config.ExtraHeaders)
	if err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: "无法创建连通性测试请求"}, nil
	}
	pool := newTransportPool(config)
	defer pool.closeIdleConnections()
	transport, err := pool.transport(identity.ProxyUrl)
	if err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: sanitizeMessage(err.Error())}, nil
	}
	response, err := transport.RoundTrip(probe)
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: "OpenAI 连通性测试失败: " + sanitizeMessage(err.Error()), LatencyMs: latency}, nil
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return &pluginv1.TestConfigResponse{Success: false, Message: "OpenAI 连通性测试返回 HTTP " + strconv.Itoa(response.StatusCode), LatencyMs: latency}, nil
	}
	status, _ := p.statusJSON()
	return &pluginv1.TestConfigResponse{Success: true, Message: "OpenAI 连通性测试成功", LatencyMs: latency, StatusJson: string(status)}, nil
}

func (p *Plugin) Forward(stream grpc.BidiStreamingServer[pluginv1.ForwardRequest, pluginv1.ForwardResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	startFrame := first.GetStart()
	if startFrame == nil {
		return sendTransportError(stream, "PLUGIN_INVALID_REQUEST", "首个请求帧必须是 start", false)
	}
	start := requestStart{
		Method:        strings.TrimSpace(startFrame.Method),
		URL:           strings.TrimSpace(startFrame.Url),
		Host:          startFrame.Host,
		Headers:       pluginHeadersToHTTP(startFrame.Headers),
		ProxyURL:      startFrame.ProxyUrl,
		ContentLength: startFrame.ContentLength,
		HasBody:       startFrame.HasBody,
	}
	if start.Method == "" {
		return sendTransportError(stream, "PLUGIN_INVALID_REQUEST", "请求方法不能为空", false)
	}

	var bodyReader *io.PipeReader
	var bodyWriter *io.PipeWriter
	if start.HasBody {
		bodyReader, bodyWriter = io.Pipe()
	}
	bodyDone := make(chan error, 1)
	go receiveRequestBody(stream, bodyWriter, start.HasBody, &p.bytesSent, bodyDone)

	p.mu.Lock()
	config := p.config
	transport, transportErr := p.pool.transport(start.ProxyURL)
	p.mu.Unlock()
	if transportErr != nil {
		if bodyReader != nil {
			_ = bodyReader.Close()
		}
		return sendTransportError(stream, "PLUGIN_PROXY_ERROR", transportErr.Error(), false)
	}

	requestCtx := stream.Context()
	var cancel context.CancelFunc
	if config.RequestTimeoutSeconds > 0 {
		requestCtx, cancel = context.WithTimeout(requestCtx, time.Duration(config.RequestTimeoutSeconds)*time.Second)
		defer cancel()
	}
	request, err := newHTTPRequest(requestCtx, start, bodyReader, config.ExtraHeaders)
	if bodyReader != nil {
		defer func() { _ = bodyReader.Close() }()
	}
	if err != nil {
		if bodyReader != nil {
			_ = bodyReader.Close()
		}
		return sendTransportError(stream, "PLUGIN_INVALID_REQUEST", err.Error(), false)
	}
	if request.URL.Scheme != "https" && config.ClientHelloProfile == ProfileNodeJS24 && config.ProxyMode != ProxyModeDisabled && strings.TrimSpace(start.ProxyURL) != "" {
		return sendTransportError(stream, "PLUGIN_PROXY_ERROR", "nodejs_24 使用账号代理时只支持 HTTPS 上游请求", false)
	}

	p.activeRequests.Add(1)
	p.requestsTotal.Add(1)
	p.lastRequestAt.Store(time.Now().Unix())
	defer p.activeRequests.Add(-1)
	response, err := transport.RoundTrip(request)
	if err != nil {
		p.errorsTotal.Add(1)
		return sendTransportError(stream, "PLUGIN_UPSTREAM_ERROR", err.Error(), true)
	}
	defer func() { _ = response.Body.Close() }()

	if err := stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Start{Start: &pluginv1.ForwardResponseStart{
		StatusCode:    int32(response.StatusCode),
		Status:        response.Status,
		Protocol:      response.Proto,
		ProtocolMajor: int32(response.ProtoMajor),
		ProtocolMinor: int32(response.ProtoMinor),
		Headers:       httpHeadersToPlugin(response.Header),
		ContentLength: response.ContentLength,
	}}}); err != nil {
		return err
	}

	buffer := make([]byte, 32*1024)
	var received int64
	for {
		read, readErr := response.Body.Read(buffer)
		if read > 0 {
			chunk := buffer[:read]
			if err := stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_BodyChunk{BodyChunk: chunk}}); err != nil {
				return err
			}
			received += int64(read)
			p.bytesReceived.Add(uint64(read))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			p.errorsTotal.Add(1)
			return sendTransportError(stream, "PLUGIN_RESPONSE_READ_ERROR", readErr.Error(), true)
		}
	}
	select {
	case bodyErr := <-bodyDone:
		if bodyErr != nil {
			p.errorsTotal.Add(1)
			return sendTransportError(stream, "PLUGIN_REQUEST_BODY_ERROR", bodyErr.Error(), true)
		}
	default:
	}
	return stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_End{End: &pluginv1.ForwardResponseEnd{
		BytesReceived: received,
	}}})
}

func receiveRequestBody(stream grpc.BidiStreamingServer[pluginv1.ForwardRequest, pluginv1.ForwardResponse], writer *io.PipeWriter, hasBody bool, bytesSent *atomic.Uint64, done chan<- error) {
	var result error
	defer func() {
		if writer != nil {
			if result != nil {
				_ = writer.CloseWithError(result)
			} else {
				_ = writer.Close()
			}
		}
		done <- result
	}()
	for {
		frame, err := stream.Recv()
		if err != nil {
			result = err
			return
		}
		if chunk := frame.GetBodyChunk(); len(chunk) > 0 {
			if !hasBody || writer == nil {
				result = errors.New("无请求体的请求包含 body_chunk")
				return
			}
			if _, err := writer.Write(chunk); err != nil {
				result = err
				return
			}
			if bytesSent != nil {
				bytesSent.Add(uint64(len(chunk)))
			}
			continue
		}
		if frame.GetBodyEnd() {
			return
		}
		result = errors.New("请求体包含无效帧")
		return
	}
}

func (p *Plugin) statusJSON() ([]byte, error) {
	p.mu.Lock()
	profile := p.config.ClientHelloProfile
	proxyMode := p.config.ProxyMode
	poolSize := 0
	if p.pool != nil {
		poolSize = len(p.pool.transports)
	}
	hostReady := p.host != nil
	p.mu.Unlock()
	lastRequestAt := ""
	if timestamp := p.lastRequestAt.Load(); timestamp > 0 {
		lastRequestAt = time.Unix(timestamp, 0).UTC().Format(time.RFC3339)
	}
	return json.Marshal(struct {
		ClientHelloProfile string `json:"client_hello_profile"`
		ProxyMode          string `json:"proxy_mode"`
		HostServicesReady  bool   `json:"host_services_ready"`
		TransportPoolSize  int    `json:"transport_pool_size"`
		ActiveRequests     int64  `json:"active_requests"`
		RequestsTotal      uint64 `json:"requests_total"`
		ErrorsTotal        uint64 `json:"errors_total"`
		BytesSent          uint64 `json:"bytes_sent_total"`
		BytesReceived      uint64 `json:"bytes_received_total"`
		LastRequestAt      string `json:"last_request_at,omitempty"`
	}{
		ClientHelloProfile: profile,
		ProxyMode:          proxyMode,
		HostServicesReady:  hostReady,
		TransportPoolSize:  poolSize,
		ActiveRequests:     p.activeRequests.Load(),
		RequestsTotal:      p.requestsTotal.Load(),
		ErrorsTotal:        p.errorsTotal.Load(),
		BytesSent:          p.bytesSent.Load(),
		BytesReceived:      p.bytesReceived.Load(),
		LastRequestAt:      lastRequestAt,
	})
}

func sendTransportError(stream grpc.BidiStreamingServer[pluginv1.ForwardRequest, pluginv1.ForwardResponse], code, message string, requestSent bool) error {
	return stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Error{Error: &pluginv1.ForwardResponseError{
		Code: code, Message: sanitizeMessage(message), RequestSent: requestSent,
	}}})
}

func sanitizeMessage(message string) string {
	message = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(message))
	if len(message) > 500 {
		message = message[:500]
	}
	if message == "" {
		return "传输失败"
	}
	return message
}

func pluginHeadersToHTTP(input map[string]*pluginv1.HeaderValues) http.Header {
	output := make(http.Header, len(input))
	for name, values := range input {
		if values != nil {
			output[name] = append([]string(nil), values.Values...)
		}
	}
	return output
}

func httpHeadersToPlugin(input http.Header) map[string]*pluginv1.HeaderValues {
	output := make(map[string]*pluginv1.HeaderValues, len(input))
	for name, values := range input {
		output[name] = &pluginv1.HeaderValues{Values: append([]string(nil), values...)}
	}
	return output
}
