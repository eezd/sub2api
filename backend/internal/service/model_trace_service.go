package service

import (
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

	account, err := s.accountRepo.GetByID(c.Request.Context(), accountID)
	if err != nil || account == nil {
		return s.sendErrorAndEnd(c, "Account not found")
	}
	if !supportsModelTraceAccount(account) {
		message := "ModelTrace 仅支持 OpenAI 和 Anthropic 的 OAuth、Setup Token 或 API Key 文本模型账号"
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckModelTrace, requestedModel, "", "", message)
		return s.sendErrorAndEnd(c, message)
	}
	if account.IsOpenAI() && isOpenAIImageModel(account.GetMappedModel(requestedModel)) {
		message := "ModelTrace 不支持图像模型"
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckModelTrace, requestedModel, "", "", message)
		return s.sendErrorAndEnd(c, message)
	}

	bank, err := loadModelTraceBank()
	if err != nil {
		message := "ModelTrace 指纹库不可用"
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckModelTrace, requestedModel, "", "", message)
		return s.sendErrorAndEnd(c, message)
	}
	challenges, err := generateModelTraceChallenges(modelTraceMaxAttempts)
	if err != nil {
		message := "无法生成 ModelTrace 检测任务"
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckModelTrace, requestedModel, "", "", message)
		return s.sendErrorAndEnd(c, message)
	}
	testedModel, err := s.resolveDegradationTestedModel(c, account, requestedModel)
	if err != nil {
		message := err.Error()
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckModelTrace, requestedModel, "", "", message)
		return s.sendErrorAndEnd(c, message)
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	s.sendEvent(c, TestEvent{
		Type:  "model_trace_start",
		Model: testedModel,
		Data: map[string]int{
			"target":       modelTraceTargetOutputs,
			"max_attempts": modelTraceMaxAttempts,
		},
	})

	accepted := make([]modelTraceOutput, 0, modelTraceTargetOutputs)
	errorsSeen := make([]string, 0, modelTraceMaxAttempts)
	attempted := 0
	recordCancelled := func() error {
		message := fmt.Sprintf("%s（已尝试 %d/%d 次）", degradationCheckCancelledMessage, attempted, modelTraceMaxAttempts)
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckModelTrace, requestedModel, testedModel, "", message)
		return c.Request.Context().Err()
	}
	for _, challenge := range challenges {
		if c.Request.Context().Err() != nil {
			return recordCancelled()
		}
		attempted++
		text, probeErr := s.runDegradationCheckProbe(c, account, requestedModel, challenge.Prompt)
		if c.Request.Context().Err() != nil {
			return recordCancelled()
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

		s.sendEvent(c, TestEvent{
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
		})
		if len(accepted) == modelTraceTargetOutputs {
			break
		}
	}

	if len(accepted) < modelTraceTargetOutputs {
		message := "ModelTrace 未收到足够的有效数字序列"
		if len(errorsSeen) > 0 {
			message += "：" + errorsSeen[len(errorsSeen)-1]
		}
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckModelTrace, requestedModel, testedModel, "", message)
		return s.sendErrorAndEnd(c, message)
	}

	if c.Request.Context().Err() != nil {
		return recordCancelled()
	}

	result, err := analyzeModelTraceOutputs(accepted, bank)
	if err != nil {
		message := err.Error()
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckModelTrace, requestedModel, testedModel, "", message)
		return s.sendErrorAndEnd(c, message)
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
	if c.Request.Context().Err() != nil {
		return recordCancelled()
	}
	if _, err := s.persistModelTraceResult(c.Request.Context(), accountID, result); err != nil {
		log.Printf("persist ModelTrace degradation check: %v", err)
		return s.sendErrorAndEnd(c, "保存 ModelTrace 检测历史失败")
	}
	s.sendEvent(c, TestEvent{Type: "model_trace_complete", Success: true, Data: result})
	return nil
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

func (s *AccountTestService) resolveDegradationTestedModel(c *gin.Context, account *Account, requestedModel string) (string, error) {
	if account.IsOpenAI() {
		testedModel := account.GetMappedModel(requestedModel)
		credentialAccount := account
		if account.IsCredentialShadow() {
			resolved, err := resolveCredentialAccount(c.Request.Context(), s.accountRepo, account)
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
func (s *AccountTestService) runDegradationCheckProbe(parent *gin.Context, account *Account, modelID, prompt string) (string, string) {
	recorder := httptest.NewRecorder()
	probeContext, _ := gin.CreateTestContext(recorder)
	probeContext.Set(degradationCheckProbeContextKey, true)
	request := parent.Request.Clone(parent.Request.Context())
	request.Method = http.MethodPost
	probeContext.Request = request

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
			text.WriteString(event.Text)
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
