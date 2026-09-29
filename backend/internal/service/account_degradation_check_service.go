package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const SVGAnimationDegradationPrompt = "Create an HTML with content that's an SVG drawing of a 2D animation of a pelican riding a bicycle."

const (
	degradationCheckPersistTimeout   = 5 * time.Second
	degradationCheckErrorMaxRunes    = 500
	accountTestStopReasonContextKey  = "account_test_stop_reason"
	degradationCheckProbeContextKey  = "account_degradation_check_probe"
	degradationCheckCancelledMessage = "检测已取消"
)

// recordAccountTestStopReason keeps the upstream stop/finish reason on the
// test context so degradation probes can discard truncated or filtered answers.
// Ordinary connectivity tests never read it.
func recordAccountTestStopReason(c *gin.Context, reason string) {
	if c == nil {
		return
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		c.Set(accountTestStopReasonContextKey, reason)
	}
}

func isDegradationCheckProbe(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, exists := c.Get(degradationCheckProbeContextKey)
	probe, _ := value.(bool)
	return exists && probe
}

func compactAccountTestError(c *gin.Context, message string) string {
	if isDegradationCheckProbe(c) {
		return compactDegradationCheckError(message)
	}
	return message
}

// degradationProbeIncompleteError mirrors the ModelTrace reference enrollment:
// answers that stopped on an output cap, refusal, or content filter are not
// complete samples and must not be scored or shown as successful output.
func degradationProbeIncompleteError(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "max_tokens", "max_output_tokens", "length", "refusal", "content_filter":
		return fmt.Sprintf("回答未正常完成（%s），本次不计入", reason)
	default:
		return ""
	}
}

// compactDegradationCheckError masks credential-like parameters and bounds the
// message by runes so persisted history never stores raw upstream bodies or
// invalid UTF-8.
func compactDegradationCheckError(message string) string {
	message = sanitizeUpstreamErrorMessage(strings.TrimSpace(message))
	runes := []rune(message)
	if len(runes) <= degradationCheckErrorMaxRunes {
		return message
	}
	return string(runes[:degradationCheckErrorMaxRunes]) + "…"
}

func ParseAccountDegradationCheckType(raw string) (AccountDegradationCheckType, error) {
	checkType := AccountDegradationCheckType(strings.TrimSpace(strings.ToLower(raw)))
	switch checkType {
	case AccountDegradationCheckModelTrace, AccountDegradationCheckSVGAnimation:
		return checkType, nil
	default:
		return "", errors.New("invalid degradation check type")
	}
}

func (s *AccountTestService) ListDegradationCheckHistory(
	ctx context.Context,
	accountID int64,
	checkType AccountDegradationCheckType,
	limit int,
) ([]*AccountDegradationCheckResult, error) {
	if s == nil || s.accountRepo == nil || s.degradationCheckRepo == nil {
		return nil, errors.New("account degradation check history is unavailable")
	}
	if _, err := s.accountRepo.GetByID(ctx, accountID); err != nil {
		return nil, err
	}
	if _, err := ParseAccountDegradationCheckType(string(checkType)); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	results, err := s.degradationCheckRepo.ListByAccountID(ctx, accountID, checkType, limit)
	if err != nil {
		return nil, err
	}
	if results == nil {
		results = []*AccountDegradationCheckResult{}
	}
	return results, nil
}

func (s *AccountTestService) persistDegradationCheck(
	ctx context.Context,
	result *AccountDegradationCheckResult,
) (*AccountDegradationCheckResult, error) {
	if len(result.Result) == 0 {
		result.Result = json.RawMessage(`{}`)
	}
	if s.degradationCheckRepo == nil {
		copy := *result
		copy.CreatedAt = time.Now().UTC()
		return &copy, nil
	}
	// The admin closing the modal cancels the request, but a run that already
	// spent upstream quota must still be recorded.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), degradationCheckPersistTimeout)
	defer cancel()
	return s.degradationCheckRepo.Create(persistCtx, result)
}

func (s *AccountTestService) recordDegradationCheckFailure(
	ctx context.Context,
	accountID int64,
	checkType AccountDegradationCheckType,
	requestedModel string,
	testedModel string,
	outputText string,
	message string,
) {
	_, err := s.persistDegradationCheck(ctx, &AccountDegradationCheckResult{
		AccountID:      accountID,
		CheckType:      checkType,
		RequestedModel: requestedModel,
		TestedModel:    testedModel,
		Status:         AccountDegradationCheckStatusError,
		OutputText:     outputText,
		ErrorMessage:   message,
	})
	if err != nil {
		log.Printf("persist account degradation check failure: %v", err)
	}
}

// TestSVGAnimation runs the fixed visual degradation prompt once and persists
// the raw model output. Rendering remains client-side inside a restricted iframe.
func (s *AccountTestService) TestSVGAnimation(c *gin.Context, accountID int64, requestedModel string) error {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return s.sendErrorAndEnd(c, "必须选择用于 SVG 动画检测的模型")
	}
	if len(requestedModel) > 256 {
		return s.sendErrorAndEnd(c, "模型 ID 过长")
	}
	if s == nil || s.accountRepo == nil {
		return errors.New("account test service is unavailable")
	}

	account, err := s.accountRepo.GetByID(c.Request.Context(), accountID)
	if err != nil || account == nil {
		return s.sendErrorAndEnd(c, "Account not found")
	}
	if !supportsModelTraceAccount(account) {
		message := "SVG 动画检测仅支持 OpenAI 和 Anthropic 的 OAuth、Setup Token 或 API Key 文本模型账号"
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckSVGAnimation, requestedModel, "", "", message)
		return s.sendErrorAndEnd(c, message)
	}
	if account.IsOpenAI() && isOpenAIImageModel(account.GetMappedModel(requestedModel)) {
		message := "SVG 动画检测不支持图像模型"
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckSVGAnimation, requestedModel, "", "", message)
		return s.sendErrorAndEnd(c, message)
	}

	testedModel, err := s.resolveDegradationTestedModel(c, account, requestedModel)
	if err != nil {
		message := err.Error()
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckSVGAnimation, requestedModel, "", "", message)
		return s.sendErrorAndEnd(c, message)
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	s.sendEvent(c, TestEvent{Type: "svg_animation_start", Model: testedModel})

	outputText, probeError := s.runDegradationCheckProbe(c, account, requestedModel, SVGAnimationDegradationPrompt)
	if c.Request.Context().Err() != nil {
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckSVGAnimation, requestedModel, testedModel, outputText, degradationCheckCancelledMessage)
		return c.Request.Context().Err()
	}
	if probeError != "" {
		message := compactDegradationCheckError(probeError)
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckSVGAnimation, requestedModel, testedModel, outputText, message)
		return s.sendErrorAndEnd(c, message)
	}
	if !strings.Contains(strings.ToLower(outputText), "<svg") {
		message := "模型响应中没有可预览的 SVG"
		s.recordDegradationCheckFailure(c.Request.Context(), accountID, AccountDegradationCheckSVGAnimation, requestedModel, testedModel, outputText, message)
		return s.sendErrorAndEnd(c, message)
	}

	historyResult, err := s.persistDegradationCheck(c.Request.Context(), &AccountDegradationCheckResult{
		AccountID:      accountID,
		CheckType:      AccountDegradationCheckSVGAnimation,
		RequestedModel: requestedModel,
		TestedModel:    testedModel,
		Status:         AccountDegradationCheckStatusSuccess,
		OutputText:     outputText,
	})
	if err != nil {
		log.Printf("persist SVG animation degradation check: %v", err)
		return s.sendErrorAndEnd(c, "保存 SVG 动画检测历史失败")
	}

	s.sendEvent(c, TestEvent{Type: "svg_animation_complete", Success: true, Data: historyResult})
	return nil
}

func (s *AccountTestService) persistModelTraceResult(
	ctx context.Context,
	accountID int64,
	result *ModelTraceResult,
) (*AccountDegradationCheckResult, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode ModelTrace result: %w", err)
	}
	return s.persistDegradationCheck(ctx, &AccountDegradationCheckResult{
		AccountID:      accountID,
		CheckType:      AccountDegradationCheckModelTrace,
		RequestedModel: result.RequestedModel,
		TestedModel:    result.TestedModel,
		Status:         AccountDegradationCheckStatusSuccess,
		Result:         resultJSON,
	})
}
