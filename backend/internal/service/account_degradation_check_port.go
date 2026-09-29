package service

import (
	"context"
	"encoding/json"
	"time"
)

type AccountDegradationCheckType string

const (
	AccountDegradationCheckModelTrace   AccountDegradationCheckType = "model_trace"
	AccountDegradationCheckSVGAnimation AccountDegradationCheckType = "svg_animation"
)

const (
	AccountDegradationCheckStatusSuccess = "success"
	AccountDegradationCheckStatusError   = "error"
)

// AccountDegradationCheckResult is one persisted administrator-run model check.
// Result contains structured output for ModelTrace; OutputText contains the
// generated HTML/SVG response for the visual check.
type AccountDegradationCheckResult struct {
	ID             int64                       `json:"id"`
	AccountID      int64                       `json:"account_id"`
	CheckType      AccountDegradationCheckType `json:"check_type"`
	RequestedModel string                      `json:"requested_model"`
	TestedModel    string                      `json:"tested_model"`
	Status         string                      `json:"status"`
	Result         json.RawMessage             `json:"result"`
	OutputText     string                      `json:"output_text,omitempty"`
	ErrorMessage   string                      `json:"error_message,omitempty"`
	CreatedAt      time.Time                   `json:"created_at"`
}

type AccountDegradationCheckRepository interface {
	Create(ctx context.Context, result *AccountDegradationCheckResult) (*AccountDegradationCheckResult, error)
	ListByAccountID(ctx context.Context, accountID int64, checkType AccountDegradationCheckType, limit int) ([]*AccountDegradationCheckResult, error)
}
