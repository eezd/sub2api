package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

var (
	ErrDegradationBatchNotFound        = errors.New("degradation batch not found")
	ErrDegradationBatchRequestConflict = errors.New("degradation batch request conflict")
	ErrDegradationBatchClaimLost       = errors.New("degradation batch claim lost")
	ErrDegradationBatchInvalidRequest  = errors.New("invalid degradation batch request")
	ErrDegradationBatchNoRunnableItems = errors.New("degradation batch has no runnable items")
	ErrDegradationBatchUnavailable     = errors.New("degradation batch unavailable")
)

const (
	DegradationBatchPending    = "pending"
	DegradationBatchRunning    = "running"
	DegradationBatchCanceling  = "canceling"
	DegradationBatchCompleted  = "completed"
	DegradationBatchCanceled   = "canceled"
	DegradationItemPending     = "pending"
	DegradationItemRunning     = "running"
	DegradationItemSucceeded   = "succeeded"
	DegradationItemFailed      = "failed"
	DegradationItemSkipped     = "skipped"
	DegradationItemCanceled    = "canceled"
	DegradationItemInterrupted = "interrupted"
)

type DegradationBatchCreateRequest struct {
	CheckType AccountDegradationCheckType  `json:"check_type"`
	Items     []DegradationBatchCreateItem `json:"items"`
}

type DegradationBatchCreateItem struct {
	AccountID int64   `json:"account_id"`
	ModelID   *string `json:"model_id"`
}

type DegradationBatchCounts struct {
	Total       int `json:"total"`
	Pending     int `json:"pending"`
	Running     int `json:"running"`
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
	Canceled    int `json:"canceled"`
	Interrupted int `json:"interrupted"`
}

type AccountDegradationCheckBatch struct {
	ID                int64                       `json:"id"`
	CheckType         AccountDegradationCheckType `json:"check_type"`
	Status            string                      `json:"status"`
	CreatedBy         int64                       `json:"created_by"`
	CanceledBy        *int64                      `json:"canceled_by"`
	CancelRequestedAt *time.Time                  `json:"cancel_requested_at"`
	StartedAt         *time.Time                  `json:"started_at"`
	FinishedAt        *time.Time                  `json:"finished_at"`
	CreatedAt         time.Time                   `json:"created_at"`
	UpdatedAt         time.Time                   `json:"updated_at"`
	Counts            DegradationBatchCounts      `json:"counts"`
	RequestKey        string                      `json:"-"`
	RequestHash       string                      `json:"-"`
}

type DegradationModelTraceSummary struct {
	MatchesExpected *bool   `json:"matches_expected"`
	PredictionName  string  `json:"prediction_name"`
	Probability     float64 `json:"probability"`
}

type AccountDegradationCheckBatchItem struct {
	ID                int64                         `json:"id"`
	BatchID           int64                         `json:"-"`
	Position          int                           `json:"position"`
	AccountID         int64                         `json:"account_id"`
	AccountName       string                        `json:"account_name"`
	Platform          string                        `json:"platform"`
	AccountType       string                        `json:"account_type"`
	RequestedModel    string                        `json:"requested_model"`
	TestedModel       string                        `json:"tested_model"`
	Status            string                        `json:"status"`
	ReasonCode        string                        `json:"reason_code"`
	ErrorMessage      string                        `json:"error_message"`
	Progress          json.RawMessage               `json:"progress"`
	HistoryID         *int64                        `json:"history_id"`
	StartedAt         *time.Time                    `json:"started_at"`
	FinishedAt        *time.Time                    `json:"finished_at"`
	UpdatedAt         time.Time                     `json:"updated_at"`
	ModelTraceSummary *DegradationModelTraceSummary `json:"model_trace_summary"`
	Result            json.RawMessage               `json:"-"`
	OutputText        string                        `json:"-"`
	ClaimToken        string                        `json:"-"`
	LeaseExpiresAt    *time.Time                    `json:"-"`
	CheckType         AccountDegradationCheckType   `json:"-"`
}

type AccountDegradationCheckBatchItemDetail struct {
	AccountDegradationCheckBatchItem
	Result     json.RawMessage `json:"result"`
	OutputText string          `json:"output_text"`
}

type DegradationBatchFilter struct {
	CheckType AccountDegradationCheckType
	Status    string
}

type DegradationItemFinish struct {
	Status       string
	ReasonCode   string
	ErrorMessage string
	Result       *AccountDegradationCheckResult
}

// Every mutation locks the parent batch before its item. FinishItem atomically
// commits the terminal item and its sole history row; stale tokens cannot commit.
type AccountDegradationCheckBatchRepository interface {
	CreateBatch(context.Context, *AccountDegradationCheckBatch, []*AccountDegradationCheckBatchItem) (*AccountDegradationCheckBatch, error)
	ListBatches(context.Context, pagination.PaginationParams, DegradationBatchFilter) ([]*AccountDegradationCheckBatch, *pagination.PaginationResult, error)
	GetBatch(context.Context, int64) (*AccountDegradationCheckBatch, error)
	GetBatchByRequestKey(context.Context, int64, string) (*AccountDegradationCheckBatch, error)
	ListItems(context.Context, int64, pagination.PaginationParams) ([]*AccountDegradationCheckBatchItem, *pagination.PaginationResult, error)
	GetItem(context.Context, int64, int64) (*AccountDegradationCheckBatchItem, error)
	ClaimNextItem(context.Context) (*AccountDegradationCheckBatchItem, error)
	Heartbeat(context.Context, int64, int64, string) (bool, error)
	UpdateProgress(context.Context, int64, int64, string, string, json.RawMessage) error
	FinishItem(context.Context, int64, int64, string, DegradationItemFinish) (*AccountDegradationCheckBatchItem, error)
	CancelBatch(context.Context, int64, int64) (*AccountDegradationCheckBatch, error)
	InterruptExpiredItems(context.Context) error
}
