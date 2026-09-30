//go:build unit

package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestParseModelTraceNumbersKeepsLongestRun(t *testing.T) {
	numbers := parseModelTraceNumbers("说明 1, 2, 999, 3 words 41 42 0 43 44")
	require.Equal(t, []int{41, 42, 43, 44}, numbers)
}

func TestAnalyzeModelTraceOutputsMatchesReferenceScorer(t *testing.T) {
	bank, err := loadModelTraceBank()
	require.NoError(t, err)

	outputs := make([]modelTraceOutput, 0, 3)
	for _, fixture := range []struct {
		seed  int
		count int
	}{{1, 300}, {2, 310}, {3, 320}} {
		values := make([]string, fixture.count)
		for index := range values {
			values[index] = fmt.Sprintf("%d", ((index*37+index*index*13+fixture.seed*17)%355)+1)
		}
		outputs = append(outputs, modelTraceOutput{
			Text:          strings.Join(values, ","),
			ExpectedCount: fixture.count,
		})
	}

	result, err := analyzeModelTraceOutputs(outputs, bank)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-luna", result.Prediction)
	require.InDelta(t, 0.506307317329251, result.Probability, 1e-10)
	require.Equal(t, "gpt", result.FamilyPrediction)
	require.InDelta(t, 0.6526826579979079, result.FamilyProbability, 1e-10)
	require.Equal(t, 3, result.UsedOutputs)
}

func TestDetectModelTraceUsesExplicitRequestedModelAndProbePrompt(t *testing.T) {
	sequence := make([]string, 220)
	for index := range sequence {
		sequence[index] = fmt.Sprintf("%d", index%modelTraceValueMax+1)
	}
	text := strings.Join(sequence, ",")
	stream := fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":%s}\n\ndata: {\"type\":\"response.completed\"}\n\n", modelTraceJSONString(text))
	responses := make([]*http.Response, modelTraceTargetOutputs)
	for index := range responses {
		responses[index] = &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(stream)),
		}
	}

	account := &Account{
		ID:          87,
		Name:        "model-trace-openai",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}
	repo := &openAIAccountTestRepo{
		mockAccountRepoForGemini: mockAccountRepoForGemini{
			accountsByID: map[int64]*Account{account.ID: account},
		},
	}
	upstream := &queuedHTTPUpstream{responses: responses}
	historyRepo := &memoryDegradationCheckRepository{}
	svc := &AccountTestService{accountRepo: repo, degradationCheckRepo: historyRepo, httpUpstream: upstream}
	ctx, recorder := newTestContext()

	err := svc.DetectModelTrace(ctx, account.ID, "gpt-5.4")
	require.NoError(t, err)
	require.Len(t, upstream.requests, modelTraceTargetOutputs)
	for _, request := range upstream.requests {
		body, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		require.Equal(t, "gpt-5.4", gjson.GetBytes(body, "model").String())
	}
	require.Contains(t, recorder.Body.String(), `"type":"model_trace_complete"`)
	require.Contains(t, recorder.Body.String(), `"requested_model":"gpt-5.4"`)
	require.Contains(t, recorder.Body.String(), `"attempted":3`)
	require.Len(t, historyRepo.results, 1)
	require.Equal(t, AccountDegradationCheckModelTrace, historyRepo.results[0].CheckType)
	require.Equal(t, AccountDegradationCheckStatusSuccess, historyRepo.results[0].Status)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(historyRepo.results[0].Result, "requested_model").String())
}

func TestDetectModelTraceRequiresAllThreeValidSamples(t *testing.T) {
	sequence := make([]string, 220)
	for index := range sequence {
		sequence[index] = fmt.Sprintf("%d", index%modelTraceValueMax+1)
	}
	valid := strings.Join(sequence, ",")
	responses := make([]*http.Response, modelTraceMaxAttempts)
	responses[0] = &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":%s}\n\ndata: {\"type\":\"response.completed\"}\n\n", modelTraceJSONString(valid)))),
	}
	for index := 1; index < len(responses); index++ {
		responses[index] = &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"1\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")),
		}
	}
	account := &Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1, Credentials: map[string]any{"access_token": "test-token"}}
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	historyRepo := &memoryDegradationCheckRepository{}
	svc := &AccountTestService{accountRepo: repo, degradationCheckRepo: historyRepo, httpUpstream: &queuedHTTPUpstream{responses: responses}}
	ctx, _ := newTestContext()

	err := svc.DetectModelTrace(ctx, account.ID, "gpt-5.4")
	require.ErrorContains(t, err, "足够的有效数字序列")
	require.Len(t, historyRepo.results, 1)
	require.Equal(t, AccountDegradationCheckStatusError, historyRepo.results[0].Status)
}

func TestModelTraceClaudePayloadUsesProbePromptAndOutputBudget(t *testing.T) {
	payload, err := createTestPayload("claude-sonnet-4-6", "explicit fingerprint prompt")
	require.NoError(t, err)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-6", gjson.GetBytes(body, "model").String())
	require.Equal(t, "explicit fingerprint prompt", gjson.GetBytes(body, "messages.0.content.0.text").String())
	require.Equal(t, int64(4096), gjson.GetBytes(body, "max_tokens").Int())
}

func modelTraceJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
