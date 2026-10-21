//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func workbenchTestPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return b.Bytes()
}
func workbenchTestInput() WorkbenchInput {
	return WorkbenchInput{RequestID: uuid.NewString(), APIKeyID: 1, Protocol: "images", Model: "image-test", Prompt: "测试", Count: 1, Size: "auto", Quality: "auto", ReferenceIDs: []string{}}
}
func TestWorkbenchInputBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*WorkbenchInput)
	}{
		{"数量", func(in *WorkbenchInput) { in.Count = 5 }}, {"未知协议", func(in *WorkbenchInput) { in.Protocol = "unknown" }},
		{"混用协议参数", func(in *WorkbenchInput) { in.AspectRatio = "1:1" }}, {"重复参考图", func(in *WorkbenchInput) { id := uuid.NewString(); in.ReferenceIDs = []string{id, id} }},
		{"提示词过长", func(in *WorkbenchInput) { in.Prompt = strings.Repeat("图", 8001) }},
		{"路径注入", func(in *WorkbenchInput) {
			in.Protocol = "gemini"
			in.Size = ""
			in.Quality = ""
			in.AspectRatio = "auto"
			in.ImageSize = "auto"
			in.Model = "../models/test"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := workbenchTestInput()
			tc.edit(&in)
			require.Error(t, validateWorkbenchInput(&in))
		})
	}
	in := workbenchTestInput()
	require.NoError(t, validateWorkbenchInput(&in))
	_, _, _, err := workbenchImageInfo([]byte("<svg/>"))
	require.Error(t, err)
	_, _, _, err = workbenchImageInfo(workbenchTestPNG(t))
	require.NoError(t, err)
}
func TestWorkbenchGatewayRequestContract(t *testing.T) {
	s := &ImageWorkbenchService{requestLimit: WorkbenchRequestLimit}
	in := workbenchTestInput()
	original := httptest.NewRequest("POST", "/api/v1/image-workbench/tasks", nil)
	original.RemoteAddr = "203.0.113.5:1234"
	original.Header.Set("X-Forwarded-For", "198.51.100.1")
	original.Header.Set("Authorization", "Bearer jwt")
	original.Header.Set("Cookie", "session=private")
	original.Header.Set("Range", "bytes=0-1")
	r, err := s.generationRequest(context.Background(), 1, in, &APIKey{Key: "test-key"}, original)
	require.NoError(t, err)
	require.Equal(t, "/v1/images/generations", r.URL.Path)
	require.Equal(t, original.RemoteAddr, r.RemoteAddr)
	require.Equal(t, "198.51.100.1", r.Header.Get("X-Forwarded-For"))
	require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
	require.Empty(t, r.Header.Get("Cookie"))
	require.Empty(t, r.Header.Get("Range"))
	data, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &body))
	require.JSONEq(t, `1`, string(body["n"]))
	require.NotContains(t, body, "size")
	in.Protocol = "gemini"
	in.Size = ""
	in.Quality = ""
	in.AspectRatio = "16:9"
	in.ImageSize = "2K"
	r, err = s.generationRequest(context.Background(), 1, in, &APIKey{Key: "test-key", Group: &Group{Platform: PlatformAntigravity}}, original)
	require.NoError(t, err)
	require.Equal(t, "/antigravity/v1beta/models/image-test:streamGenerateContent", r.URL.Path)
	require.Equal(t, "sse", r.URL.Query().Get("alt"))
	data, err = io.ReadAll(r.Body)
	require.NoError(t, err)
	require.Contains(t, string(data), `"responseModalities":["TEXT","IMAGE"]`)
	s.requestLimit = 10
	_, err = s.generationRequest(context.Background(), 1, in, &APIKey{}, original)
	require.Error(t, err)
}
func TestWorkbenchResponseParsing(t *testing.T) {
	s := &ImageWorkbenchService{}
	encoded := base64.StdEncoding.EncodeToString(workbenchTestPNG(t))
	for _, tc := range []struct {
		name, protocol, contentType, body string
		ok                                bool
	}{
		{"Images", "images", "application/json", `{"data":[{"b64_json":"` + encoded + `"}]}`, true},
		{"空结果", "images", "application/json", `{"data":[]}`, false},
		{"错误编码", "images", "application/json", `{"data":[{"b64_json":"!?"}]}`, false},
		{"Gemini JSON", "gemini", "application/json", `{"candidates":[{"content":{"parts":[{"inlineData":{"data":"` + encoded + `"}}]},"finishReason":"STOP"}]}`, true},
		{"Gemini SSE", "gemini", "text/event-stream", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"inline_data\":{\"data\":\"" + encoded + "\"}}]}}]}\n\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n", true},
		{"Gemini 累积式 SSE", "gemini", "text/event-stream", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"inlineData\":{\"data\":\"" + encoded + "\"}}]}}]}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"inlineData\":{\"data\":\"" + encoded + "\"}}]},\"finishReason\":\"STOP\"}]}\n\n", true},
		{"截断流", "gemini", "text/event-stream", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"inlineData\":{\"data\":\"" + encoded + "\"}}]}}]}\n\n", false},
		{"安全拦截", "gemini", "application/json", `{"promptFeedback":{"blockReason":"SAFETY"}}`, false},
		{"多结果", "images", "application/json", `{"data":[{},{}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorkbenchResponse(context.Background())
			w.Header().Set("Content-Type", tc.contentType)
			_, err := w.Write([]byte(tc.body))
			require.NoError(t, err)
			data, err := s.readGenerationResult(context.Background(), tc.protocol, w)
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, workbenchTestPNG(t), data)
			} else {
				require.Error(t, err)
			}
		})
	}
}

type workbenchTestUpstream struct {
	HTTPUpstream
	request *http.Request
}

func (u *workbenchTestUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.request = r
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("image"))}, nil
}
func TestWorkbenchDownloadSecurity(t *testing.T) {
	upstream := &workbenchTestUpstream{}
	s := &ImageWorkbenchService{upstream: upstream}
	for _, raw := range []string{"http://127.0.0.1/private", "http://169.254.169.254/latest", "http://[::1]/", "file:///etc/passwd"} {
		_, err := s.downloadResult(context.Background(), raw)
		require.Error(t, err)
		require.Nil(t, upstream.request)
	}
	raw := "https://images.example.com/a%2Fb/?signature=literal%2Fvalue"
	_, err := s.downloadResult(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, raw, upstream.request.URL.String())
	require.True(t, HTTPUpstreamPublicHostsOnly(upstream.request.Context()))
	require.Empty(t, upstream.request.Header.Get("Authorization"))
}

type workbenchTestKeys struct {
	APIKeyRepository
	key *APIKey
}

func (r workbenchTestKeys) GetByID(context.Context, int64) (*APIKey, error) { return r.key, nil }

type workbenchTaskStore struct {
	ImageWorkbenchRepository
	mu    sync.Mutex
	job   WorkbenchJob
	done  chan struct{}
	saved []WorkbenchItem
}

func (r *workbenchTaskStore) Create(_ context.Context, _ int64, in WorkbenchInput) (WorkbenchJob, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.job.ID != "" {
		return r.job, true, nil
	}
	r.job = WorkbenchJob{ID: uuid.NewString(), Input: in, Status: "running"}
	return r.job, false, nil
}
func (r *workbenchTaskStore) Get(context.Context, int64, string) (WorkbenchJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.job, nil
}
func (r *workbenchTaskStore) SaveItem(_ context.Context, _ int64, _ string, item WorkbenchItem, _ *WorkbenchAsset, write func() error) error {
	if err := write(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saved = append(r.saved, item)
	return nil
}
func (r *workbenchTaskStore) Finish(_ context.Context, _ int64, _ string, status string) error {
	r.mu.Lock()
	r.job.Status = status
	r.mu.Unlock()
	close(r.done)
	return nil
}
func TestWorkbenchBackgroundLifecycle(t *testing.T) {
	for _, cancelTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "离页继续并保留部分结果", true: "主动取消"}[cancelTask], func(t *testing.T) {
			repo := &workbenchTaskStore{done: make(chan struct{})}
			s := &ImageWorkbenchService{repo: repo, keys: &APIKeyService{apiKeyRepo: workbenchTestKeys{key: &APIKey{UserID: 1, Key: "private-test-key"}}}, dir: t.TempDir(), requestLimit: WorkbenchRequestLimit, cancels: make(map[string]context.CancelFunc)}
			entered := make(chan struct{})
			release := make(chan struct{})
			calls := 0
			ids := map[string]bool{}
			encoded := base64.StdEncoding.EncodeToString(workbenchTestPNG(t))
			s.BindGateway(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				id := resolveUsageBillingRequestID(r.Context(), "")
				if ids[id] {
					t.Error("每张图必须使用独立请求标识")
				}
				ids[id] = true
				if calls == 1 {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				if calls == 2 {
					w.WriteHeader(502)
					_, _ = io.WriteString(w, `{"error":{"message":"private-test-key failed"}}`)
					return
				}
				_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+encoded+`"}]}`)
			}))
			clientCtx, leave := context.WithCancel(context.WithValue(context.Background(), ctxkey.ClientRequestID, "panel-request"))
			defer leave()
			in := workbenchTestInput()
			in.Count = 2
			job, existing, err := s.Submit(clientCtx, 1, in, httptest.NewRequest("POST", "/", nil))
			require.NoError(t, err)
			require.False(t, existing)
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("任务未开始")
			}
			_, existing, err = s.Submit(context.Background(), 1, in, httptest.NewRequest("POST", "/", nil))
			require.NoError(t, err)
			require.True(t, existing)
			leave()
			if cancelTask {
				require.NoError(t, s.Cancel(context.Background(), 1, job.ID))
			} else {
				close(release)
			}
			select {
			case <-repo.done:
			case <-time.After(3 * time.Second):
				t.Fatal("任务未结束")
			}
			require.NoError(t, s.Stop(context.Background()))
			if cancelTask {
				require.Equal(t, "cancelled", repo.job.Status)
				require.Equal(t, 1, calls)
			} else {
				require.Equal(t, "partial", repo.job.Status)
				require.Len(t, repo.saved, 2)
				require.Equal(t, "complete", repo.saved[0].Status)
				require.NotContains(t, repo.saved[1].Error, "private-test-key")
			}
			_, _, err = s.Submit(context.Background(), 1, in, httptest.NewRequest("POST", "/", nil))
			require.Error(t, err)
			_, _, err = s.Submit(context.Background(), 2, in, httptest.NewRequest("POST", "/", nil))
			require.ErrorIs(t, err, ErrWorkbenchNotFound)
		})
	}
}
func TestWorkbenchWriterLimit(t *testing.T) {
	w := newWorkbenchResponse(context.Background())
	_, err := w.Write(make([]byte, workbenchResponseLimit+1))
	require.Error(t, err)
	require.Zero(t, w.body.Len())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = newWorkbenchResponse(ctx).Write([]byte("x"))
	require.True(t, errors.Is(err, context.Canceled))
}

type workbenchReferenceStore struct {
	ImageWorkbenchRepository
	asset WorkbenchAsset
}

func (r workbenchReferenceStore) Asset(_ context.Context, uid int64, id string) (WorkbenchAsset, error) {
	if uid != 1 || id != r.asset.ID {
		return WorkbenchAsset{}, ErrWorkbenchNotFound
	}
	return r.asset, nil
}
func TestWorkbenchReferenceProtocols(t *testing.T) {
	data := workbenchTestPNG(t)
	a := WorkbenchAsset{ID: uuid.NewString(), Filename: "ref.png", MimeType: "image/png", Size: int64(len(data)), Width: 2, Height: 2, Draft: true}
	s := &ImageWorkbenchService{dir: t.TempDir(), requestLimit: WorkbenchRequestLimit, repo: workbenchReferenceStore{asset: a}}
	require.NoError(t, s.writeAsset(1, a, data))
	in := workbenchTestInput()
	in.ReferenceIDs = []string{a.ID}
	request, err := s.generationRequest(context.Background(), 1, in, &APIKey{Key: "test"}, httptest.NewRequest("POST", "/", nil))
	require.NoError(t, err)
	require.Equal(t, "/v1/images/edits", request.URL.Path)
	require.NoError(t, request.ParseMultipartForm(1<<20))
	form := request.MultipartForm
	defer func() { require.NoError(t, form.RemoveAll()) }()
	require.Equal(t, "1", request.FormValue("n"))
	require.Len(t, request.MultipartForm.File["image[]"], 1)
	f, err := request.MultipartForm.File["image[]"][0].Open()
	require.NoError(t, err)
	got, err := io.ReadAll(f)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.Equal(t, data, got)
	in.Protocol = "gemini"
	in.Size = ""
	in.Quality = ""
	in.AspectRatio = "auto"
	in.ImageSize = "auto"
	request, err = s.generationRequest(context.Background(), 1, in, &APIKey{Key: "test"}, httptest.NewRequest("POST", "/", nil))
	require.NoError(t, err)
	raw, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.Contains(t, string(raw), base64.StdEncoding.EncodeToString(data))
	require.Contains(t, string(raw), `"mimeType":"image/png"`)
	_, err = s.generationRequest(context.Background(), 2, in, &APIKey{Key: "test"}, httptest.NewRequest("POST", "/", nil))
	require.ErrorIs(t, err, ErrWorkbenchNotFound)
}
func TestWorkbenchShutdownInterruptsWithoutRetry(t *testing.T) {
	repo := &workbenchTaskStore{done: make(chan struct{})}
	s := &ImageWorkbenchService{repo: repo, keys: &APIKeyService{apiKeyRepo: workbenchTestKeys{key: &APIKey{UserID: 1, Key: "test"}}}, dir: t.TempDir(), requestLimit: WorkbenchRequestLimit, cancels: make(map[string]context.CancelFunc)}
	entered := make(chan struct{})
	calls := 0
	s.BindGateway(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { calls++; close(entered); <-r.Context().Done() }))
	_, _, err := s.Submit(context.Background(), 1, workbenchTestInput(), httptest.NewRequest("POST", "/", nil))
	require.NoError(t, err)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("任务未开始")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, s.Stop(ctx))
	require.Equal(t, "interrupted", repo.job.Status)
	require.Equal(t, 1, calls)
}
