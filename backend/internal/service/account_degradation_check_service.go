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

// degradationEventWriter captures errors hidden by the existing SSE writer.
// Only degradation-check adapters install it; other account tests are unchanged.
type degradationEventWriter struct {
	gin.ResponseWriter
	err error
}

func (w *degradationEventWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if err != nil && w.err == nil {
		w.err = err
	}
	return n, err
}

func (w *degradationEventWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	if err != nil && w.err == nil {
		w.err = err
	}
	return n, err
}

func (s *AccountTestService) emitDegradationEvent(c *gin.Context, event TestEvent) error {
	if err := c.Request.Context().Err(); err != nil {
		return err
	}
	writer, ok := c.Writer.(*degradationEventWriter)
	if !ok {
		writer = &degradationEventWriter{ResponseWriter: c.Writer}
		c.Writer = writer
	}
	if writer.err != nil {
		return writer.err
	}
	s.sendEvent(c, event)
	if writer.err != nil {
		return writer.err
	}
	return c.Request.Context().Err()
}

// RunDegradationCheck executes a check without persisting history or emitting a
// completion event. Callers own the result commit and completion notification.
func (s *AccountTestService) RunDegradationCheck(
	ctx context.Context,
	accountID int64,
	checkType AccountDegradationCheckType,
	requestedModel string,
	emit func(TestEvent) error,
) (*AccountDegradationCheckResult, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return nil, errors.New("必须选择用于检测的模型")
	}
	if len(requestedModel) > 256 {
		return nil, errors.New("模型 ID 过长")
	}
	switch checkType {
	case AccountDegradationCheckModelTrace:
		return s.runModelTraceCheck(ctx, accountID, requestedModel, emit)
	case AccountDegradationCheckSVGAnimation:
		return s.runSVGAnimationCheck(ctx, accountID, requestedModel, emit)
	default:
		return nil, errors.New("invalid degradation check type")
	}
}

// TestSVGAnimation adapts the shared core to the single-account SSE endpoint.
// Rendering remains client-side inside a restricted iframe.
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
	ctx := c.Request.Context()
	result, runErr := s.RunDegradationCheck(ctx, accountID, AccountDegradationCheckSVGAnimation, requestedModel, func(event TestEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if event.Type == "svg_animation_start" {
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			c.Writer.Header().Set("Cache-Control", "no-cache")
			c.Writer.Header().Set("Connection", "keep-alive")
			c.Writer.Header().Set("X-Accel-Buffering", "no")
			c.Writer.Flush()
		}
		return s.emitDegradationEvent(c, event)
	})
	if result != nil {
		if runErr == nil && ctx.Err() != nil {
			runErr = ctx.Err()
			result.Status = AccountDegradationCheckStatusError
			result.ErrorMessage = degradationCheckCancelledMessage
		}
		historyResult, persistErr := s.persistDegradationCheck(ctx, result)
		if persistErr != nil {
			if runErr == nil {
				log.Printf("persist SVG animation degradation check: %v", persistErr)
				return s.sendErrorAndEnd(c, "保存 SVG 动画检测历史失败")
			}
			log.Printf("persist account degradation check failure: %v", persistErr)
		} else if runErr == nil {
			s.sendEvent(c, TestEvent{Type: "svg_animation_complete", Success: true, Data: historyResult})
			return nil
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.sendErrorAndEnd(c, runErr.Error())
}

func (s *AccountTestService) runSVGAnimationCheck(
	ctx context.Context,
	accountID int64,
	requestedModel string,
	emit func(TestEvent) error,
) (*AccountDegradationCheckResult, error) {
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
	result := &AccountDegradationCheckResult{
		AccountID:      accountID,
		CheckType:      AccountDegradationCheckSVGAnimation,
		RequestedModel: requestedModel,
		Status:         AccountDegradationCheckStatusError,
		Result:         json.RawMessage(`{}`),
	}
	fail := func(err error) (*AccountDegradationCheckResult, error) {
		result.ErrorMessage = compactDegradationCheckError(err.Error())
		if ctx.Err() != nil {
			result.ErrorMessage = degradationCheckCancelledMessage
			err = ctx.Err()
		}
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if !supportsModelTraceAccount(account) {
		return fail(errors.New("SVG 动画检测仅支持 OpenAI 和 Anthropic 的 OAuth、Setup Token 或 API Key 文本模型账号"))
	}
	if account.IsOpenAI() && isOpenAIImageModel(account.GetMappedModel(requestedModel)) {
		return fail(errors.New("SVG 动画检测不支持图像模型"))
	}
	result.TestedModel, err = s.resolveDegradationTestedModel(ctx, account, requestedModel)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if emit != nil {
		if err := emit(TestEvent{Type: "svg_animation_start", Model: result.TestedModel}); err != nil {
			return fail(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	var probeError string
	result.OutputText, probeError = s.runDegradationCheckProbe(ctx, account, requestedModel, SVGAnimationDegradationPrompt)
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if probeError != "" {
		return fail(errors.New(compactDegradationCheckError(probeError)))
	}
	if !strings.Contains(strings.ToLower(result.OutputText), "<svg") {
		return fail(errors.New("模型响应中没有可预览的 SVG"))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	result.Status = AccountDegradationCheckStatusSuccess
	return result, nil
}
