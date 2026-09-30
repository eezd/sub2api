package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func degradationBatchHTTPError(err error) error {
	switch {
	case errors.Is(err, service.ErrDegradationBatchInvalidRequest):
		return infraerrors.New(http.StatusBadRequest, "DEGRADATION_BATCH_INVALID_REQUEST", "Invalid batch request")
	case errors.Is(err, service.ErrDegradationBatchNoRunnableItems):
		return infraerrors.New(http.StatusBadRequest, "DEGRADATION_BATCH_NO_RUNNABLE_ITEMS", "No runnable accounts in this batch")
	case errors.Is(err, service.ErrDegradationBatchRequestConflict):
		return infraerrors.New(http.StatusConflict, "DEGRADATION_BATCH_REQUEST_CONFLICT", "Request key already belongs to a different batch request")
	case errors.Is(err, service.ErrDegradationBatchNotFound):
		return infraerrors.New(http.StatusNotFound, "DEGRADATION_BATCH_NOT_FOUND", "Batch or batch item not found")
	default:
		return infraerrors.New(http.StatusServiceUnavailable, "DEGRADATION_BATCH_UNAVAILABLE", "Batch persistence is unavailable")
	}
}

func degradationBatchID(c *gin.Context, param string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(param), 10, 64)
	if err != nil || id <= 0 {
		response.ErrorFrom(c, degradationBatchHTTPError(service.ErrDegradationBatchInvalidRequest))
		return 0, false
	}
	return id, true
}

func degradationBatchPagination(c *gin.Context) pagination.PaginationParams {
	page, pageSize := response.ParsePagination(c)
	if pageSize > 100 {
		pageSize = 100
	}
	return pagination.PaginationParams{Page: page, PageSize: pageSize}
}

func (h *AccountHandler) CreateDegradationCheckBatch(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	if h.accountDegradationCheckBatchService == nil {
		response.ErrorFrom(c, degradationBatchHTTPError(service.ErrDegradationBatchUnavailable))
		return
	}
	var request service.DegradationBatchCreateRequest
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if err := c.ShouldBindJSON(&request); err != nil || key == "" {
		response.ErrorFrom(c, degradationBatchHTTPError(service.ErrDegradationBatchInvalidRequest))
		return
	}
	normalized, _, err := service.NormalizeDegradationBatchRequest(request)
	if err != nil {
		response.ErrorFrom(c, degradationBatchHTTPError(err))
		return
	}
	request = normalized
	c.Request.Header.Set("Idempotency-Key", key)
	payload := struct {
		OperatorID int64                                 `json:"operator_id"`
		Body       service.DegradationBatchCreateRequest `json:"body"`
	}{subject.UserID, request}
	result, err := executeAdminIdempotent(c, "admin.accounts.degradation_check_batches.create", payload, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		batch, err := h.accountDegradationCheckBatchService.CreateBatch(ctx, subject.UserID, key, request)
		if err != nil {
			return nil, degradationBatchHTTPError(err)
		}
		return batch, nil
	})
	if err != nil {
		if infraerrors.Code(err) == infraerrors.Code(service.ErrIdempotencyKeyConflict) {
			err = degradationBatchHTTPError(service.ErrDegradationBatchRequestConflict)
		}
		if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return
	}
	if result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	response.Success(c, result.Data)
}

func (h *AccountHandler) ListDegradationCheckBatches(c *gin.Context) {
	filter := service.DegradationBatchFilter{}
	if raw := c.Query("check_type"); raw != "" {
		checkType, err := service.ParseAccountDegradationCheckType(raw)
		if err != nil {
			response.ErrorFrom(c, degradationBatchHTTPError(service.ErrDegradationBatchInvalidRequest))
			return
		}
		filter.CheckType = checkType
	}
	filter.Status = c.Query("status")
	switch filter.Status {
	case "", service.DegradationBatchPending, service.DegradationBatchRunning, service.DegradationBatchCanceling, service.DegradationBatchCompleted, service.DegradationBatchCanceled:
	default:
		response.ErrorFrom(c, degradationBatchHTTPError(service.ErrDegradationBatchInvalidRequest))
		return
	}
	params := degradationBatchPagination(c)
	items, result, err := h.accountDegradationCheckBatchService.ListBatches(c.Request.Context(), params, filter)
	if err != nil {
		response.ErrorFrom(c, degradationBatchHTTPError(err))
		return
	}
	response.Paginated(c, items, result.Total, params.Page, params.PageSize)
}

func (h *AccountHandler) GetDegradationCheckBatch(c *gin.Context) {
	id, ok := degradationBatchID(c, "batchId")
	if !ok {
		return
	}
	batch, err := h.accountDegradationCheckBatchService.GetBatch(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, degradationBatchHTTPError(err))
		return
	}
	response.Success(c, batch)
}

func (h *AccountHandler) ListDegradationCheckBatchItems(c *gin.Context) {
	id, ok := degradationBatchID(c, "batchId")
	if !ok {
		return
	}
	params := degradationBatchPagination(c)
	items, result, err := h.accountDegradationCheckBatchService.ListItems(c.Request.Context(), id, params)
	if err != nil {
		response.ErrorFrom(c, degradationBatchHTTPError(err))
		return
	}
	response.Paginated(c, items, result.Total, params.Page, params.PageSize)
}

func (h *AccountHandler) GetDegradationCheckBatchItem(c *gin.Context) {
	batchID, ok := degradationBatchID(c, "batchId")
	if !ok {
		return
	}
	itemID, ok := degradationBatchID(c, "itemId")
	if !ok {
		return
	}
	item, err := h.accountDegradationCheckBatchService.GetItem(c.Request.Context(), batchID, itemID)
	if err != nil {
		response.ErrorFrom(c, degradationBatchHTTPError(err))
		return
	}
	response.Success(c, service.AccountDegradationCheckBatchItemDetail{AccountDegradationCheckBatchItem: *item, Result: item.Result, OutputText: item.OutputText})
}

func (h *AccountHandler) CancelDegradationCheckBatch(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	id, ok := degradationBatchID(c, "batchId")
	if !ok {
		return
	}
	batch, err := h.accountDegradationCheckBatchService.CancelBatch(c.Request.Context(), id, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, degradationBatchHTTPError(err))
		return
	}
	response.Success(c, batch)
}
