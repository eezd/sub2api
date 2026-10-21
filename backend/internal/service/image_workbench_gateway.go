package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const workbenchResponseLimit = 32 << 20

type workbenchResponse struct {
	header http.Header
	code   int
	body   bytes.Buffer
	err    error
	ctx    context.Context
}

func newWorkbenchResponse(ctx context.Context) *workbenchResponse {
	return &workbenchResponse{header: make(http.Header), ctx: ctx}
}
func (w *workbenchResponse) Header() http.Header { return w.header }
func (w *workbenchResponse) WriteHeader(code int) {
	if w.code == 0 && code >= 200 {
		w.code = code
	}
}
func (w *workbenchResponse) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return 0, err
	}
	if w.body.Len()+len(p) > workbenchResponseLimit {
		w.err = errors.New("生图响应超过 32 MiB，请降低分辨率后重新生成")
		return 0, w.err
	}
	w.WriteHeader(http.StatusOK)
	return w.body.Write(p)
}
func (w *workbenchResponse) Flush() { w.WriteHeader(http.StatusOK) }
func (w *workbenchResponse) CloseNotify() <-chan bool {
	done := make(chan bool, 1)
	go func() { <-w.ctx.Done(); done <- true }()
	return done
}

func workbenchRequest(original *http.Request, ctx context.Context, key, method, path, contentType string, body []byte) *http.Request {
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, uuid.NewString())
	r := original.Clone(ctx)
	r.Method = method
	r.URL = &url.URL{Path: strings.SplitN(path, "?", 2)[0]}
	if parts := strings.SplitN(path, "?", 2); len(parts) == 2 {
		r.URL.RawQuery = parts[1]
	}
	r.RequestURI = r.URL.RequestURI()
	for _, name := range []string{"Cookie", "Content-Encoding", "Content-Length", "Transfer-Encoding", "Connection", "Upgrade", "If-None-Match", "If-Modified-Since", "Range"} {
		r.Header.Del(name)
	}
	r.TransferEncoding = nil
	r.Header.Del("x-api-key")
	r.Header.Del("x-goog-api-key")
	r.Header.Del("Idempotency-Key")
	r.Header.Del("X-Client-Request-Id")
	r.Header.Set("X-Request-Id", uuid.NewString())
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("Accept", "application/json")
	r.ContentLength = int64(len(body))
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return r
}

type WorkbenchModels struct {
	IDs                   []string `json:"ids"`
	Protocol              string   `json:"protocol"`
	NativeGeminiAvailable bool     `json:"native_gemini_available"`
}

func (s *ImageWorkbenchService) Models(ctx context.Context, uid, keyID int64, protocol string, original *http.Request) (WorkbenchModels, error) {
	key, err := s.ownedKey(ctx, uid, keyID)
	if err != nil {
		return WorkbenchModels{}, err
	}
	native := key.Group != nil && (key.Group.Platform == PlatformGemini || key.Group.Platform == PlatformAntigravity || key.Group.Platform == PlatformComposite)
	if protocol == "" {
		protocol = "images"
		if key.Group != nil && (key.Group.Platform == PlatformGemini || key.Group.Platform == PlatformAntigravity) {
			protocol = "gemini"
		}
	}
	path := "/v1/models"
	switch protocol {
	case "images":
	case "gemini":
		if !native {
			return WorkbenchModels{}, infraerrors.BadRequest("IMAGE_WORKBENCH_PROTOCOL", "此密钥分组不支持 Gemini 原生协议")
		}
		path = "/v1beta/models"
		if key.Group != nil && key.Group.Platform == PlatformAntigravity {
			path = "/antigravity/v1beta/models"
		}
	default:
		return WorkbenchModels{}, infraerrors.BadRequest("IMAGE_WORKBENCH_PROTOCOL", "生图协议无效")
	}
	if s.gateway == nil {
		return WorkbenchModels{}, infraerrors.ServiceUnavailable("IMAGE_WORKBENCH_UNAVAILABLE", "生图网关尚未就绪")
	}
	r := workbenchRequest(original, ctx, key.Key, http.MethodGet, path, "application/json", nil)
	result := newWorkbenchResponse(ctx)
	s.gateway.ServeHTTP(result, r)
	if err = workbenchResponseError(result); err != nil {
		return WorkbenchModels{}, infraerrors.BadRequest("IMAGE_WORKBENCH_MODELS", workbenchErrorMessage(err, r.Header.Get("Authorization")))
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err = json.Unmarshal(result.body.Bytes(), &body); err != nil {
		return WorkbenchModels{}, infraerrors.New(http.StatusBadGateway, "IMAGE_WORKBENCH_MODELS", "网关模型列表格式无效")
	}
	ids := []string{}
	if protocol == "gemini" {
		for _, m := range body.Models {
			ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
		}
	} else {
		for _, m := range body.Data {
			ids = append(ids, m.ID)
		}
	}
	return WorkbenchModels{IDs: ids, Protocol: protocol, NativeGeminiAvailable: native}, nil
}

type workbenchInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}
type workbenchGeminiPart struct {
	Text            string               `json:"text,omitempty"`
	InlineData      *workbenchInlineData `json:"inlineData,omitempty"`
	InlineDataSnake *workbenchInlineData `json:"inline_data,omitempty"`
}

func (s *ImageWorkbenchService) generationRequest(ctx context.Context, uid int64, in WorkbenchInput, key *APIKey, original *http.Request) (*http.Request, error) {
	var body bytes.Buffer
	contentType := "application/json"
	path := "/v1/images/generations"
	refs := make([]WorkbenchAsset, 0, len(in.ReferenceIDs))
	for _, id := range in.ReferenceIDs {
		a, err := s.repo.Asset(ctx, uid, id)
		if err != nil {
			return nil, err
		}
		refs = append(refs, a)
	}
	if in.Protocol == "gemini" {
		path = "/v1beta/models/" + url.PathEscape(in.Model) + ":streamGenerateContent?alt=sse"
		if key.Group != nil && key.Group.Platform == PlatformAntigravity {
			path = "/antigravity" + path
		}
		parts := []workbenchGeminiPart{{Text: in.Prompt}}
		for _, a := range refs {
			data, err := osReadWorkbenchAsset(ctx, s, uid, a)
			if err != nil {
				return nil, err
			}
			parts = append(parts, workbenchGeminiPart{InlineData: &workbenchInlineData{MimeType: a.MimeType, Data: base64.StdEncoding.EncodeToString(data)}})
		}
		type imageConfig struct {
			AspectRatio string `json:"aspectRatio,omitempty"`
			ImageSize   string `json:"imageSize,omitempty"`
		}
		cfg := imageConfig{}
		if in.AspectRatio != "auto" {
			cfg.AspectRatio = in.AspectRatio
		}
		if in.ImageSize != "auto" {
			cfg.ImageSize = in.ImageSize
		}
		payload := struct {
			Contents []struct {
				Role  string                `json:"role"`
				Parts []workbenchGeminiPart `json:"parts"`
			} `json:"contents"`
			GenerationConfig struct {
				ResponseModalities []string    `json:"responseModalities"`
				ImageConfig        imageConfig `json:"imageConfig"`
			} `json:"generationConfig"`
		}{}
		payload.Contents = append(payload.Contents, struct {
			Role  string                `json:"role"`
			Parts []workbenchGeminiPart `json:"parts"`
		}{Role: "user", Parts: parts})
		payload.GenerationConfig.ResponseModalities = []string{"TEXT", "IMAGE"}
		payload.GenerationConfig.ImageConfig = cfg
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			return nil, err
		}
	} else if len(refs) > 0 {
		path = "/v1/images/edits"
		writer := multipart.NewWriter(&body)
		fields := []struct{ key, value string }{{"model", in.Model}, {"prompt", in.Prompt}, {"n", "1"}, {"response_format", "b64_json"}}
		if in.Size != "auto" {
			fields = append(fields, struct{ key, value string }{"size", in.Size})
		}
		if in.Quality != "auto" {
			fields = append(fields, struct{ key, value string }{"quality", in.Quality})
		}
		for _, f := range fields {
			if err := writer.WriteField(f.key, f.value); err != nil {
				return nil, err
			}
		}
		for _, a := range refs {
			data, err := osReadWorkbenchAsset(ctx, s, uid, a)
			if err != nil {
				return nil, err
			}
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", `form-data; name="image[]"; filename="reference`+extensionForContentType(a.MimeType)+`"`)
			header.Set("Content-Type", a.MimeType)
			part, err := writer.CreatePart(header)
			if err != nil {
				return nil, err
			}
			if _, err = part.Write(data); err != nil {
				return nil, err
			}
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		contentType = writer.FormDataContentType()
	} else {
		payload := struct {
			Model          string `json:"model"`
			Prompt         string `json:"prompt"`
			N              int    `json:"n"`
			Size           string `json:"size,omitempty"`
			Quality        string `json:"quality,omitempty"`
			ResponseFormat string `json:"response_format"`
		}{Model: in.Model, Prompt: in.Prompt, N: 1, ResponseFormat: "b64_json"}
		if in.Size != "auto" {
			payload.Size = in.Size
		}
		if in.Quality != "auto" {
			payload.Quality = in.Quality
		}
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			return nil, err
		}
	}
	if int64(body.Len()) > s.requestLimit {
		return nil, infraerrors.New(http.StatusRequestEntityTooLarge, "IMAGE_WORKBENCH_REQUEST_LIMIT", "参考图编码后的请求超过入口限制，请减少图片数量或大小")
	}
	return workbenchRequest(original, ctx, key.Key, http.MethodPost, path, contentType, body.Bytes()), nil
}

func osReadWorkbenchAsset(ctx context.Context, s *ImageWorkbenchService, uid int64, a WorkbenchAsset) ([]byte, error) {
	_, f, err := s.Open(ctx, uid, a.ID)
	if err != nil {
		return nil, fmt.Errorf("打开参考图失败: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, WorkbenchFileLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != a.Size {
		return nil, errors.New("参考图文件大小与记录不一致")
	}
	return data, nil
}

func workbenchResponseError(w *workbenchResponse) error {
	if w.err != nil {
		return w.err
	}
	if w.code >= 200 && w.code < 300 {
		return nil
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(w.body.Bytes(), &payload) == nil && payload.Error.Message != "" {
		return fmt.Errorf("网关返回 %d：%s", w.code, payload.Error.Message)
	}
	return fmt.Errorf("网关请求失败（HTTP %d）", w.code)
}

func decodeWorkbenchBase64(encoded string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)), WorkbenchFileLimit+1))
	if err != nil {
		return nil, errors.New("生图返回的 Base64 无效")
	}
	if len(data) == 0 || int64(len(data)) > WorkbenchFileLimit {
		return nil, errors.New("生成图片为空或超过 20 MiB，请降低分辨率")
	}
	return data, nil
}

func (s *ImageWorkbenchService) readGenerationResult(ctx context.Context, protocol string, w *workbenchResponse) ([]byte, error) {
	if err := workbenchResponseError(w); err != nil {
		return nil, err
	}
	if protocol == "images" {
		var result struct {
			Data []struct {
				Base64 string `json:"b64_json"`
				URL    string `json:"url"`
			} `json:"data"`
		}
		if json.Unmarshal(bytes.TrimSpace(w.body.Bytes()), &result) != nil {
			return nil, errors.New("生图网关返回了无效 JSON")
		}
		if len(result.Data) != 1 {
			return nil, errors.New("本次请求应返回一张图片，实际结果数量不符")
		}
		item := result.Data[0]
		if item.Base64 != "" {
			return decodeWorkbenchBase64(item.Base64)
		}
		if item.URL == "" {
			return nil, errors.New("生成结果没有图片")
		}
		if strings.HasPrefix(item.URL, "data:") {
			header, data, ok := strings.Cut(item.URL, ",")
			if !ok || !strings.HasSuffix(header, ";base64") {
				return nil, errors.New("生图返回的 Data URL 无效")
			}
			return decodeWorkbenchBase64(data)
		}
		return s.downloadResult(ctx, item.URL)
	}
	var images [][]byte
	finished := false
	consume := func(raw []byte) error {
		payloadHasImage := false
		var envelope struct {
			Candidates []struct {
				Content struct {
					Parts []workbenchGeminiPart `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			PromptFeedback struct {
				BlockReason string `json:"blockReason"`
			} `json:"promptFeedback"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return errors.New("生图响应格式无效（Gemini）")
		}
		if envelope.Error != nil {
			return fmt.Errorf("生图失败（Gemini）：%s", envelope.Error.Message)
		}
		if envelope.PromptFeedback.BlockReason != "" {
			return fmt.Errorf("请求被 Gemini 拒绝：%s", envelope.PromptFeedback.BlockReason)
		}
		for _, c := range envelope.Candidates {
			if c.FinishReason != "" && c.FinishReason != "STOP" {
				return fmt.Errorf("生图未正常完成（Gemini）：%s", c.FinishReason)
			}
			if c.FinishReason == "STOP" {
				finished = true
			}
			for _, part := range c.Content.Parts {
				inline := part.InlineData
				if inline == nil {
					inline = part.InlineDataSnake
				}
				if inline != nil && inline.Data != "" {
					data, err := decodeWorkbenchBase64(inline.Data)
					if err != nil {
						return err
					}
					if payloadHasImage {
						return errors.New("本次 Gemini 返回了多张图片，结果数量不符")
					}
					payloadHasImage = true
					// 现有 Gemini 上游支持累积式 SSE，同一图片可能在结束片段再次出现。
					if len(images) == 0 {
						images = append(images, data)
					} else if !bytes.Equal(images[0], data) {
						return errors.New("本次 Gemini 返回了多张图片，结果数量不符")
					}
				}
			}
		}
		return nil
	}
	if strings.Contains(w.header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(bytes.NewReader(w.body.Bytes()))
		scanner.Buffer(make([]byte, 4096), workbenchResponseLimit)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "data:") {
				raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if raw == "[DONE]" {
					continue
				}
				if err := consume([]byte(raw)); err != nil {
					return nil, err
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	} else if err := consume(w.body.Bytes()); err != nil {
		return nil, err
	}
	if !finished {
		return nil, errors.New("生图响应未正常结束（Gemini），上游可能已经计费")
	}
	if len(images) != 1 {
		return nil, errors.New("未收到 Gemini 图片，模型可能不支持生图或请求已被拒绝")
	}
	return images[0], nil
}

func (s *ImageWorkbenchService) downloadResult(ctx context.Context, rawURL string) ([]byte, error) {
	_, err := urlvalidator.ValidateHTTPURL(rawURL, true, urlvalidator.ValidationOptions{})
	if err != nil {
		return nil, errors.New("生成图片的下载地址不安全或无效")
	}
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamPublicHostsOnly(ctx), 60*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	// 复用网关的 DNS 和每跳重定向防护；不向图片来源发送用户密钥。
	response, err := s.upstream.Do(request, "", 0, 1)
	if err != nil {
		logger.L().Warn("下载生图结果失败", zap.String("host", request.URL.Hostname()), zap.String("error_type", fmt.Sprintf("%T", err)))
		return nil, errors.New("下载生成图片失败，上游可能已经计费")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载生成图片失败（HTTP %d）", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, WorkbenchFileLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || int64(len(data)) > WorkbenchFileLimit {
		return nil, errors.New("生成图片为空或超过 20 MiB")
	}
	return data, nil
}
