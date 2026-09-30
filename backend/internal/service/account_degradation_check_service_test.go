//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type memoryDegradationCheckRepository struct {
	results       []*AccountDegradationCheckResult
	contextErrors []error
}

func (r *memoryDegradationCheckRepository) Create(ctx context.Context, result *AccountDegradationCheckResult) (*AccountDegradationCheckResult, error) {
	r.contextErrors = append(r.contextErrors, ctx.Err())
	copy := *result
	copy.ID = int64(len(r.results) + 1)
	copy.CreatedAt = time.Date(2026, time.September, 29, 12, len(r.results), 0, 0, time.UTC)
	copy.Result = append(json.RawMessage(nil), result.Result...)
	r.results = append(r.results, &copy)
	return &copy, nil
}

func (r *memoryDegradationCheckRepository) ListByAccountID(
	_ context.Context,
	accountID int64,
	checkType AccountDegradationCheckType,
	limit int,
) ([]*AccountDegradationCheckResult, error) {
	results := make([]*AccountDegradationCheckResult, 0, limit)
	for index := len(r.results) - 1; index >= 0 && len(results) < limit; index-- {
		result := r.results[index]
		if result.AccountID == accountID && result.CheckType == checkType {
			results = append(results, result)
		}
	}
	return results, nil
}

func TestSVGAnimationCheckUsesFixedPromptAndPersistsReplayableOutput(t *testing.T) {
	generated := `<!doctype html><html><body><svg id="pelican-bike"><animate attributeName="x" values="0;1" /></svg></body></html>`
	stream := fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":%s}\n\ndata: {\"type\":\"response.completed\"}\n\n", modelTraceJSONString(generated))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(stream)),
	}}}
	account := &Account{
		ID:          91,
		Name:        "svg-history-openai",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}
	accountRepo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{
		accountsByID: map[int64]*Account{account.ID: account},
	}}
	historyRepo := &memoryDegradationCheckRepository{}
	svc := &AccountTestService{
		accountRepo:          accountRepo,
		degradationCheckRepo: historyRepo,
		httpUpstream:         upstream,
	}
	ctx, recorder := newTestContext()

	err := svc.TestSVGAnimation(ctx, account.ID, "gpt-5.4")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	requestBody, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(requestBody, "model").String())

	require.Len(t, historyRepo.results, 1)
	saved := historyRepo.results[0]
	require.Equal(t, AccountDegradationCheckSVGAnimation, saved.CheckType)
	require.Equal(t, AccountDegradationCheckStatusSuccess, saved.Status)
	require.Equal(t, generated, saved.OutputText)
	var completeEvent string
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		payload := strings.TrimPrefix(line, "data: ")
		if gjson.Get(payload, "type").String() == "svg_animation_complete" {
			completeEvent = payload
			break
		}
	}
	require.NotEmpty(t, completeEvent)
	require.Equal(t, generated, gjson.Get(completeEvent, "data.output_text").String())

	history, err := svc.ListDegradationCheckHistory(context.Background(), account.ID, AccountDegradationCheckSVGAnimation, 10)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, saved.ID, history[0].ID)
}

func TestSVGAnimationCheckPersistsMissingSVGFailure(t *testing.T) {
	stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"plain text\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(stream)),
	}}}
	account := &Account{
		ID:          92,
		Name:        "svg-history-invalid",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}
	accountRepo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{
		accountsByID: map[int64]*Account{account.ID: account},
	}}
	historyRepo := &memoryDegradationCheckRepository{}
	svc := &AccountTestService{accountRepo: accountRepo, degradationCheckRepo: historyRepo, httpUpstream: upstream}
	ctx, _ := newTestContext()

	err := svc.TestSVGAnimation(ctx, account.ID, "gpt-5.4")
	require.ErrorContains(t, err, "没有可预览的 SVG")
	require.Len(t, historyRepo.results, 1)
	require.Equal(t, AccountDegradationCheckStatusError, historyRepo.results[0].Status)
	require.Equal(t, "plain text", historyRepo.results[0].OutputText)
}

// cancelingHTTPUpstream simulates the admin closing the modal while the first
// upstream call is in flight.
type cancelingHTTPUpstream struct {
	queuedHTTPUpstream
	cancel context.CancelFunc
}

func (u *cancelingHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.cancel()
	return u.queuedHTTPUpstream.DoWithTLS(req, proxyURL, accountID, concurrency, profile)
}

func newDegradationCheckTestService(account *Account, upstream HTTPUpstream) (*AccountTestService, *memoryDegradationCheckRepository) {
	accountRepo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{
		accountsByID: map[int64]*Account{account.ID: account},
	}}
	historyRepo := &memoryDegradationCheckRepository{}
	return &AccountTestService{accountRepo: accountRepo, degradationCheckRepo: historyRepo, httpUpstream: upstream}, historyRepo
}

func newOpenAIDegradationTestAccount(id int64) *Account {
	return &Account{
		ID:          id,
		Name:        "degradation-openai",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}
}

func newAnthropicDegradationTestAccount(id int64) *Account {
	return &Account{
		ID:          id,
		Name:        "degradation-anthropic",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}
}

func truncatedClaudeStream(text string) *http.Response {
	body := fmt.Sprintf(
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\n"+
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n"+
			"data: {\"type\":\"message_stop\"}\n\n",
		modelTraceJSONString(text),
	)
	return newJSONResponse(http.StatusOK, body)
}

func TestDegradationChecksPersistCancelledRunsWithLiveContext(t *testing.T) {
	for _, check := range []struct {
		name    string
		run     func(*AccountTestService, *gin.Context, int64) error
		message string
	}{
		{
			name: "model trace",
			run: func(s *AccountTestService, c *gin.Context, id int64) error {
				return s.DetectModelTrace(c, id, "gpt-5.4")
			},
			message: "检测已取消（已尝试 1/6 次）",
		},
		{
			name: "svg animation",
			run: func(s *AccountTestService, c *gin.Context, id int64) error {
				return s.TestSVGAnimation(c, id, "gpt-5.4")
			},
			message: "检测已取消",
		},
	} {
		t.Run(check.name, func(t *testing.T) {
			requestCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<svg></svg>\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"
			upstream := &cancelingHTTPUpstream{
				queuedHTTPUpstream: queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusOK, stream)}},
				cancel:             cancel,
			}
			account := newOpenAIDegradationTestAccount(93)
			svc, historyRepo := newDegradationCheckTestService(account, upstream)
			ginCtx, _ := newTestContext()
			ginCtx.Request = ginCtx.Request.WithContext(requestCtx)

			err := check.run(svc, ginCtx, account.ID)
			require.ErrorIs(t, err, context.Canceled)
			require.Len(t, upstream.requests, 1, "a cancelled run must stop spending upstream quota")
			require.Len(t, historyRepo.results, 1)
			require.Equal(t, AccountDegradationCheckStatusError, historyRepo.results[0].Status)
			require.Equal(t, check.message, historyRepo.results[0].ErrorMessage)
			require.NoError(t, historyRepo.contextErrors[0], "history must be written with a context that outlives the request")
		})
	}
}

func TestSVGAnimationStoresSanitizedBoundedUpstreamError(t *testing.T) {
	body := "upstream rejected https://relay.example/v1?key=secret-value " + strings.Repeat("错", 600)
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusBadRequest, body)}}
	account := newOpenAIDegradationTestAccount(94)
	svc, historyRepo := newDegradationCheckTestService(account, upstream)
	ginCtx, _ := newTestContext()

	err := svc.TestSVGAnimation(ginCtx, account.ID, "gpt-5.4")
	require.Error(t, err)
	require.Len(t, historyRepo.results, 1)
	message := historyRepo.results[0].ErrorMessage
	require.NotContains(t, message, "secret-value")
	require.True(t, utf8.ValidString(message))
	require.LessOrEqual(t, utf8.RuneCountInString(message), degradationCheckErrorMaxRunes+1)
	require.NotContains(t, err.Error(), "secret-value")
}

func TestModelTraceRecordsRejectedAccountsAndImageModels(t *testing.T) {
	gemini := &Account{ID: 95, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 1}
	svc, historyRepo := newDegradationCheckTestService(gemini, &queuedHTTPUpstream{})
	ginCtx, _ := newTestContext()
	require.Error(t, svc.DetectModelTrace(ginCtx, gemini.ID, "gemini-3-pro"))
	require.Len(t, historyRepo.results, 1)
	require.Equal(t, AccountDegradationCheckModelTrace, historyRepo.results[0].CheckType)
	require.Contains(t, historyRepo.results[0].ErrorMessage, "仅支持 OpenAI 和 Anthropic")

	openai := newOpenAIDegradationTestAccount(96)
	svc, historyRepo = newDegradationCheckTestService(openai, &queuedHTTPUpstream{})
	ginCtx, _ = newTestContext()
	require.Error(t, svc.DetectModelTrace(ginCtx, openai.ID, "gpt-image-2"))
	require.Len(t, historyRepo.results, 1)
	require.Equal(t, "ModelTrace 不支持图像模型", historyRepo.results[0].ErrorMessage)
}

func TestSVGAnimationRejectsTruncatedClaudeOutput(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{truncatedClaudeStream("<html><svg><g>")}}
	account := newAnthropicDegradationTestAccount(97)
	svc, historyRepo := newDegradationCheckTestService(account, upstream)
	ginCtx, recorder := newTestContext()

	err := svc.TestSVGAnimation(ginCtx, account.ID, "claude-sonnet-4-6")
	require.ErrorContains(t, err, "回答未正常完成（max_tokens）")
	require.Len(t, historyRepo.results, 1)
	require.Equal(t, AccountDegradationCheckStatusError, historyRepo.results[0].Status)
	require.Equal(t, "<html><svg><g>", historyRepo.results[0].OutputText)
	require.NotContains(t, recorder.Body.String(), `"type":"svg_animation_complete"`)
}

func TestModelTraceDiscardsTruncatedClaudeAnswers(t *testing.T) {
	sequence := make([]string, 320)
	for index := range sequence {
		sequence[index] = fmt.Sprintf("%d", index%modelTraceValueMax+1)
	}
	responses := make([]*http.Response, modelTraceMaxAttempts)
	for index := range responses {
		responses[index] = truncatedClaudeStream(strings.Join(sequence, ","))
	}
	upstream := &queuedHTTPUpstream{responses: responses}
	account := newAnthropicDegradationTestAccount(98)
	svc, historyRepo := newDegradationCheckTestService(account, upstream)
	ginCtx, _ := newTestContext()

	err := svc.DetectModelTrace(ginCtx, account.ID, "claude-sonnet-4-6")
	require.ErrorContains(t, err, "回答未正常完成（max_tokens）")
	require.Len(t, upstream.requests, modelTraceMaxAttempts, "truncated answers use up attempts instead of being scored")
	require.Len(t, historyRepo.results, 1)
	require.Equal(t, AccountDegradationCheckStatusError, historyRepo.results[0].Status)
}

func TestSVGAnimationRejectsClaudeStreamEOFBeforeMessageStop(t *testing.T) {
	stream := "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"<svg><g>\"}}\n\n"
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusOK, stream)}}
	account := newAnthropicDegradationTestAccount(100)
	svc, historyRepo := newDegradationCheckTestService(account, upstream)
	ginCtx, _ := newTestContext()

	err := svc.TestSVGAnimation(ginCtx, account.ID, "claude-sonnet-4-6")
	require.ErrorContains(t, err, "Claude stream ended before message_stop")
	require.Len(t, historyRepo.results, 1)
	require.Equal(t, AccountDegradationCheckStatusError, historyRepo.results[0].Status)
}
