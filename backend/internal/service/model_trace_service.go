package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	modelTraceTargetOutputs      = 3
	modelTraceMaxAttempts        = 6
	modelTraceMinChallengeLength = 292
	modelTraceMaxChallengeLength = 332
)

type modelTraceChallenge struct {
	ExpectedCount int
	Prompt        string
}

type modelTraceProgress struct {
	Attempt        int    `json:"attempt"`
	MaxAttempts    int    `json:"max_attempts"`
	Received       int    `json:"received"`
	Target         int    `json:"target"`
	Accepted       bool   `json:"accepted"`
	ParsedNumbers  int    `json:"parsed_numbers"`
	MinimumNumbers int    `json:"minimum_numbers"`
	Error          string `json:"error,omitempty"`
}

// DetectModelTrace runs up to six fingerprint probes against exactly the model
// selected by the administrator and streams progress plus the final attribution.
func (s *AccountTestService) DetectModelTrace(c *gin.Context, accountID int64, requestedModel string) error {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return s.sendErrorAndEnd(c, "必须选择用于 ModelTrace 检测的模型")
	}
	if len(requestedModel) > 256 {
		return s.sendErrorAndEnd(c, "模型 ID 过长")
	}
	if s == nil || s.accountRepo == nil {
		return fmt.Errorf("account test service is unavailable")
	}

	ctx := c.Request.Context()
	started := false
	record, runErr := s.runModelTraceCheck(ctx, accountID, requestedModel, func(event TestEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !started {
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			c.Writer.Header().Set("Cache-Control", "no-cache")
			c.Writer.Header().Set("Connection", "keep-alive")
			c.Writer.Header().Set("X-Accel-Buffering", "no")
			c.Writer.Flush()
			started = true
		}
		return s.emitDegradationEvent(c, event)
	})
	if record == nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.sendErrorAndEnd(c, "Account not found")
	}
	var result ModelTraceResult
	if runErr == nil {
		if err := json.Unmarshal(record.Result, &result); err != nil {
			runErr = fmt.Errorf("decode ModelTrace result: %w", err)
			record.Status = AccountDegradationCheckStatusError
			record.ErrorMessage = compactDegradationCheckError(runErr.Error())
		}
	}
	if runErr == nil && ctx.Err() != nil {
		runErr = ctx.Err()
		record.Status = AccountDegradationCheckStatusError
		record.ErrorMessage = fmt.Sprintf("%s（已尝试 %d/%d 次）", degradationCheckCancelledMessage, result.APITest.Attempted, modelTraceMaxAttempts)
	}
	if _, err := s.persistDegradationCheck(ctx, record); err != nil {
		if runErr == nil {
			log.Printf("persist ModelTrace degradation check: %v", err)
			return s.sendErrorAndEnd(c, "保存 ModelTrace 检测历史失败")
		}
		log.Printf("persist account degradation check failure: %v", err)
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.sendErrorAndEnd(c, record.ErrorMessage)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.sendEvent(c, TestEvent{Type: "model_trace_complete", Success: true, Data: &result})
	return nil
}

// runModelTraceCheck returns an unpersisted history record. Attribution mismatch
// and models absent from the reference bank remain successful executions.
func (s *AccountTestService) runModelTraceCheck(ctx context.Context, accountID int64, requestedModel string, emit func(TestEvent) error) (*AccountDegradationCheckResult, error) {
	if s == nil || s.accountRepo == nil {
		return nil, errors.New("account test service is unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, ErrAccountNotFound
	}
	record := &AccountDegradationCheckResult{
		AccountID:      accountID,
		CheckType:      AccountDegradationCheckModelTrace,
		RequestedModel: requestedModel,
		Status:         AccountDegradationCheckStatusError,
		Result:         json.RawMessage(`{}`),
	}
	attempted := 0
	fail := func(message string, cause error) (*AccountDegradationCheckResult, error) {
		record.ErrorMessage = compactDegradationCheckError(message)
		if cause == nil {
			cause = errors.New(record.ErrorMessage)
		}
		return record, cause
	}
	cancelled := func() (*AccountDegradationCheckResult, error) {
		return fail(fmt.Sprintf("%s（已尝试 %d/%d 次）", degradationCheckCancelledMessage, attempted, modelTraceMaxAttempts), ctx.Err())
	}
	if ctx.Err() != nil {
		return cancelled()
	}
	if !supportsModelTraceAccount(account) {
		return fail("ModelTrace 仅支持 OpenAI 和 Anthropic 的 OAuth、Setup Token 或 API Key 文本模型账号", nil)
	}
	if account.IsOpenAI() && isOpenAIImageModel(account.GetMappedModel(requestedModel)) {
		return fail("ModelTrace 不支持图像模型", nil)
	}
	bank, err := loadModelTraceBank()
	if err != nil {
		return fail("ModelTrace 指纹库不可用", nil)
	}
	challenges, err := generateModelTraceChallenges(modelTraceMaxAttempts)
	if err != nil {
		return fail("无法生成 ModelTrace 检测任务", nil)
	}
	testedModel, err := s.resolveDegradationTestedModel(ctx, account, requestedModel)
	if ctx.Err() != nil {
		return cancelled()
	}
	if err != nil {
		return fail(err.Error(), err)
	}
	record.TestedModel = testedModel
	if emit != nil {
		if err := emit(TestEvent{
			Type:  "model_trace_start",
			Model: testedModel,
			Data: map[string]int{
				"target":       modelTraceTargetOutputs,
				"max_attempts": modelTraceMaxAttempts,
			},
		}); err != nil {
			if ctx.Err() != nil {
				return cancelled()
			}
			return fail(err.Error(), err)
		}
	}

	accepted := make([]modelTraceOutput, 0, modelTraceTargetOutputs)
	errorsSeen := make([]string, 0, modelTraceMaxAttempts)
	for _, challenge := range challenges {
		if ctx.Err() != nil {
			return cancelled()
		}
		attempted++
		text, probeErr := s.runDegradationCheckProbe(ctx, account, requestedModel, challenge.Prompt)
		if ctx.Err() != nil {
			return cancelled()
		}
		numbers := parseModelTraceNumbers(text)
		minimum := max(80, int(math.Ceil(float64(challenge.ExpectedCount)*0.55)))
		acceptedProbe := probeErr == "" && len(numbers) >= minimum
		if acceptedProbe {
			accepted = append(accepted, modelTraceOutput{Text: text, ExpectedCount: challenge.ExpectedCount})
		} else {
			if probeErr == "" {
				probeErr = fmt.Sprintf("有效数字不足：%d/%d", len(numbers), minimum)
			}
			probeErr = compactDegradationCheckError(probeErr)
			errorsSeen = append(errorsSeen, probeErr)
		}
		if emit != nil {
			if err := emit(TestEvent{
				Type: "model_trace_progress",
				Data: modelTraceProgress{
					Attempt:        attempted,
					MaxAttempts:    modelTraceMaxAttempts,
					Received:       len(accepted),
					Target:         modelTraceTargetOutputs,
					Accepted:       acceptedProbe,
					ParsedNumbers:  len(numbers),
					MinimumNumbers: minimum,
					Error:          probeErr,
				},
			}); err != nil {
				if ctx.Err() != nil {
					return cancelled()
				}
				return fail(err.Error(), err)
			}
		}
		if len(accepted) == modelTraceTargetOutputs {
			break
		}
	}
	if ctx.Err() != nil {
		return cancelled()
	}
	if len(accepted) < modelTraceTargetOutputs {
		message := "ModelTrace 未收到足够的有效数字序列"
		if len(errorsSeen) > 0 {
			message += "：" + errorsSeen[len(errorsSeen)-1]
		}
		return fail(message, nil)
	}
	result, err := analyzeModelTraceOutputs(accepted, bank)
	if ctx.Err() != nil {
		return cancelled()
	}
	if err != nil {
		return fail(err.Error(), err)
	}
	result.RequestedModel = requestedModel
	result.TestedModel = testedModel
	result.ExpectedModelInBank = modelTraceBankHasModel(bank, testedModel)
	if result.ExpectedModelInBank {
		matches := strings.EqualFold(result.Prediction, testedModel)
		result.MatchesExpected = &matches
	}
	result.APITest = ModelTraceAPITest{
		Requested:   modelTraceTargetOutputs,
		Attempted:   attempted,
		MaxAttempts: modelTraceMaxAttempts,
		Received:    len(accepted),
		Errors:      errorsSeen,
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fail(fmt.Sprintf("encode ModelTrace result: %v", err), err)
	}
	if ctx.Err() != nil {
		return cancelled()
	}
	record.Status = AccountDegradationCheckStatusSuccess
	record.Result = resultJSON
	return record, nil
}

func supportsModelTraceAccount(account *Account) bool {
	if account == nil {
		return false
	}
	if !account.IsOpenAI() && account.Platform != PlatformAnthropic {
		return false
	}
	switch account.Type {
	case AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey:
		return true
	default:
		return false
	}
}

func (s *AccountTestService) resolveDegradationTestedModel(ctx context.Context, account *Account, requestedModel string) (string, error) {
	if account.IsOpenAI() {
		testedModel := account.GetMappedModel(requestedModel)
		credentialAccount := account
		if account.IsCredentialShadow() {
			resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
			if err != nil {
				return "", err
			}
			credentialAccount = resolved
		}
		if credentialAccount.IsOAuth() {
			testedModel = normalizeOpenAIModelForUpstream(credentialAccount, testedModel)
		}
		return testedModel, nil
	}
	if account.Type == AccountTypeAPIKey {
		return account.GetMappedModel(requestedModel), nil
	}
	return requestedModel, nil
}

// runDegradationCheckProbe reuses the connectivity-test transport and returns
// the streamed text plus an error for failed, truncated, refused, or filtered
// answers.
func (s *AccountTestService) runDegradationCheckProbe(ctx context.Context, account *Account, modelID, prompt string) (string, string) {
	recorder := httptest.NewRecorder()
	probeContext, _ := gin.CreateTestContext(recorder)
	probeContext.Set(degradationCheckProbeContextKey, true)
	probeContext.Request = (&http.Request{Method: http.MethodPost}).WithContext(ctx)

	testErr := s.testAccountConnectionForAccount(probeContext, account, modelID, prompt, AccountTestModeDefault, AccountTestOptions{})
	text, eventErr := parseAccountTestSSEOutput(recorder.Body.String())
	if eventErr != "" {
		return text, eventErr
	}
	if testErr != nil {
		return text, testErr.Error()
	}
	if incomplete := degradationProbeIncompleteError(probeContext.GetString(accountTestStopReasonContextKey)); incomplete != "" {
		return text, incomplete
	}
	return text, ""
}

func parseAccountTestSSEOutput(body string) (responseText, errorMessage string) {
	var text strings.Builder
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var event TestEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		switch event.Type {
		case "content":
			_, _ = text.WriteString(event.Text)
		case "error":
			errorMessage = event.Error
		}
	}
	return text.String(), errorMessage
}

func generateModelTraceChallenges(count int) ([]modelTraceChallenge, error) {
	available := modelTraceMaxChallengeLength - modelTraceMinChallengeLength + 1
	if count <= 0 || count > available {
		return nil, fmt.Errorf("invalid ModelTrace challenge count: %d", count)
	}
	lengths := make([]int, available)
	for index := range lengths {
		lengths[index] = modelTraceMinChallengeLength + index
	}
	for index := 0; index < count; index++ {
		offset, err := randomModelTraceIndex(available - index)
		if err != nil {
			return nil, err
		}
		swapIndex := index + offset
		lengths[index], lengths[swapIndex] = lengths[swapIndex], lengths[index]
	}

	openings := []string{
		"这是一次独立的数值选择记录",
		"请完成下面的无语义整数选择任务",
		"执行一次第一反应取值记录",
		"生成一组不承载语义的整数选择",
		"进行一轮快速逐项取值",
	}
	actions := []string{
		"为各个位置分别凭第一反应选择",
		"逐项选择",
		"每次只决定当前一项，共给出",
		"分别凭第一反应给出",
		"逐个直接选择",
	}
	endings := []string{
		"允许某个数字再次出现；每项写出后不要回头排序、去重或替换。",
		"偶然重复是有效的；不要重新排列或修正已经写出的项目。",
		"相同值可以再次出现；输出过程中不要整理或改写前面的项目。",
		"重复值无需删除；不要筛选、重排或补成某种规律。",
		"不必赋予数字任何含义；已经给出的值保持不变。",
	}
	separatorHints := []string{
		"数字之间用逗号或空格分隔均可。",
		"使用一种一致的常见分隔符即可。",
		"可以用逗号、空格或换行分隔。",
		"只要每个整数边界清楚，格式可自行选择。",
	}

	challenges := make([]modelTraceChallenge, count)
	for index := range challenges {
		opening, err := randomModelTraceChoice(openings)
		if err != nil {
			return nil, err
		}
		action, err := randomModelTraceChoice(actions)
		if err != nil {
			return nil, err
		}
		ending, err := randomModelTraceChoice(endings)
		if err != nil {
			return nil, err
		}
		separatorHint, err := randomModelTraceChoice(separatorHints)
		if err != nil {
			return nil, err
		}
		length := lengths[index]
		challenges[index] = modelTraceChallenge{
			ExpectedCount: length,
			Prompt: fmt.Sprintf(
				"%s。%s %d 个 1 到 355（含端点）的整数。每个位置都要单独选择；不要从 1 开始计数，不要连续递增或递减，也不要采用等差、循环、重复区块或其他规则化模式。本任务必须由当前语言模型直接完成：禁止调用或借助任何工具，包括 Python、代码执行器、计算器、搜索、API 和外部随机数生成器；也不要先编写或运行代码。%s%s直接从第一个取值开始输出，不要在序列前重复数量、范围或任务说明。",
				opening,
				action,
				length,
				ending,
				separatorHint,
			),
		}
	}
	return challenges, nil
}

func randomModelTraceChoice(values []string) (string, error) {
	index, err := randomModelTraceIndex(len(values))
	if err != nil {
		return "", err
	}
	return values[index], nil
}

func randomModelTraceIndex(limit int) (int, error) {
	if limit <= 0 {
		return 0, errors.New("invalid random choice limit")
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(limit)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}
