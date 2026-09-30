//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type batchHandlerAccounts struct{ service.AccountRepository }

func (batchHandlerAccounts) GetByID(_ context.Context, id int64) (*service.Account, error) {
	if id != 1 {
		return nil, service.ErrAccountNotFound
	}
	return &service.Account{ID: 1, Name: "selected", Platform: "openai", Type: "apikey"}, nil
}

type batchHandlerRepository struct {
	service.AccountDegradationCheckBatchRepository
	batch *service.AccountDegradationCheckBatch
	items []*service.AccountDegradationCheckBatchItem
}

func (r *batchHandlerRepository) GetBatchByRequestKey(_ context.Context, actor int64, key string) (*service.AccountDegradationCheckBatch, error) {
	if r.batch == nil || r.batch.CreatedBy != actor || r.batch.RequestKey != key {
		return nil, service.ErrDegradationBatchNotFound
	}
	return r.batch, nil
}
func (r *batchHandlerRepository) CreateBatch(_ context.Context, b *service.AccountDegradationCheckBatch, items []*service.AccountDegradationCheckBatchItem) (*service.AccountDegradationCheckBatch, error) {
	b.ID, b.Status = 9, service.DegradationBatchPending
	b.Counts.Total = len(items)
	for index, item := range items {
		item.ID, item.BatchID = int64(index+1), b.ID
		if item.Status == service.DegradationItemPending {
			b.Counts.Pending++
		} else {
			b.Counts.Skipped++
		}
	}
	r.batch, r.items = b, items
	return b, nil
}
func (r *batchHandlerRepository) GetBatch(_ context.Context, id int64) (*service.AccountDegradationCheckBatch, error) {
	if r.batch == nil || id != r.batch.ID {
		return nil, service.ErrDegradationBatchNotFound
	}
	return r.batch, nil
}
func (r *batchHandlerRepository) ListItems(ctx context.Context, id int64, _ pagination.PaginationParams) ([]*service.AccountDegradationCheckBatchItem, *pagination.PaginationResult, error) {
	if _, err := r.GetBatch(ctx, id); err != nil {
		return nil, nil, err
	}
	return r.items, &pagination.PaginationResult{Total: int64(len(r.items))}, nil
}
func (r *batchHandlerRepository) GetItem(ctx context.Context, batchID, itemID int64) (*service.AccountDegradationCheckBatchItem, error) {
	if _, err := r.GetBatch(ctx, batchID); err != nil {
		return nil, err
	}
	for _, item := range r.items {
		if item.ID == itemID {
			return item, nil
		}
	}
	return nil, service.ErrDegradationBatchNotFound
}

func degradationBatchTestRouter(actor bool) (*gin.Engine, *batchHandlerRepository) {
	gin.SetMode(gin.TestMode)
	repo := &batchHandlerRepository{}
	svc := service.NewAccountDegradationCheckBatchService(repo, batchHandlerAccounts{}, &service.AccountTestService{}, nil)
	h := &AccountHandler{accountDegradationCheckBatchService: svc}
	router := gin.New()
	if actor {
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
			c.Next()
		})
	}
	base := "/accounts/degradation-check-batches"
	router.POST(base, h.CreateDegradationCheckBatch)
	router.GET(base, h.ListDegradationCheckBatches)
	router.GET(base+"/:batchId", h.GetDegradationCheckBatch)
	router.GET(base+"/:batchId/items", h.ListDegradationCheckBatchItems)
	router.GET(base+"/:batchId/items/:itemId", h.GetDegradationCheckBatchItem)
	return router, repo
}
func batchHandlerRequest(router *gin.Engine, method, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestDegradationCheckBatchHTTPCreateIdentityAndReplay(t *testing.T) {
	body := `{"check_type":"svg_animation","items":[{"account_id":1,"model_id":" text-model "},{"account_id":2,"model_id":null}]}`
	router, repo := degradationBatchTestRouter(true)
	path := "/accounts/degradation-check-batches"
	first := batchHandlerRequest(router, "POST", path, body, "operation-1")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Equal(t, int64(7), repo.batch.CreatedBy)
	require.Equal(t, 1, repo.batch.Counts.Pending)
	require.Equal(t, 1, repo.batch.Counts.Skipped)
	require.Equal(t, "text-model", repo.items[0].RequestedModel)
	replay := batchHandlerRequest(router, "POST", path, body, "operation-1")
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	require.Equal(t, first.Body.String(), replay.Body.String())
	conflict := batchHandlerRequest(router, "POST", path, strings.Replace(body, "text-model", "different-model", 1), "operation-1")
	require.Equal(t, http.StatusConflict, conflict.Code)
	require.Contains(t, conflict.Body.String(), "DEGRADATION_BATCH_REQUEST_CONFLICT")
	unauthenticated, _ := degradationBatchTestRouter(false)
	require.Equal(t, http.StatusUnauthorized, batchHandlerRequest(unauthenticated, "POST", path, body, "operation-2").Code)
}

func TestDegradationCheckBatchHTTPCanonicalIdempotencyCoordinator(t *testing.T) {
	config := service.DefaultIdempotencyConfig()
	config.ObserveOnly = false
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(newMemoryIdempotencyRepoStub(), config))
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(nil) })

	router, _ := degradationBatchTestRouter(true)
	path := "/accounts/degradation-check-batches"
	first := batchHandlerRequest(router, "POST", path, `{"check_type":"svg_animation","items":[{"account_id":1,"model_id":" text-model "}]}`, "canonical-operation")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	replay := batchHandlerRequest(router, "POST", path, `{"check_type":"svg_animation","items":[{"account_id":1,"model_id":"text-model"}]}`, "canonical-operation")
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("X-Idempotency-Replayed"))
	require.JSONEq(t, first.Body.String(), replay.Body.String())

	conflict := batchHandlerRequest(router, "POST", path, `{"check_type":"svg_animation","items":[{"account_id":1,"model_id":"other-model"}]}`, "canonical-operation")
	require.Equal(t, http.StatusConflict, conflict.Code)
	require.Contains(t, conflict.Body.String(), "DEGRADATION_BATCH_REQUEST_CONFLICT")
}

func TestDegradationCheckBatchHTTPRejectsInvalidRequests(t *testing.T) {
	for _, test := range []struct{ name, body, key string }{
		{"missing key", `{"check_type":"model_trace","items":[{"account_id":1,"model_id":"m"}]}`, ""},
		{"malformed", "{", "bad-1"},
		{"invalid type", `{"check_type":"other","items":[{"account_id":1,"model_id":"m"}]}`, "bad-2"},
		{"empty items", `{"check_type":"model_trace","items":[]}`, "bad-3"},
		{"duplicate accounts", `{"check_type":"model_trace","items":[{"account_id":1,"model_id":"m"},{"account_id":1,"model_id":"n"}]}`, "bad-4"},
		{"empty model", `{"check_type":"model_trace","items":[{"account_id":1,"model_id":" "}]}`, "bad-5"},
		{"no runnable", `{"check_type":"model_trace","items":[{"account_id":2,"model_id":"m"}]}`, "bad-6"},
	} {
		t.Run(test.name, func(t *testing.T) {
			router, repo := degradationBatchTestRouter(true)
			response := batchHandlerRequest(router, "POST", "/accounts/degradation-check-batches", test.body, test.key)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Nil(t, repo.batch)
		})
	}
}

func TestDegradationCheckBatchHTTPSummaryKeepsOutputPrivateAndScopesDetail(t *testing.T) {
	router, repo := degradationBatchTestRouter(true)
	repo.batch = &service.AccountDegradationCheckBatch{ID: 9, RequestKey: "private-key", RequestHash: "private-hash"}
	repo.items = []*service.AccountDegradationCheckBatchItem{{ID: 1, BatchID: 9, Status: "succeeded", Progress: json.RawMessage(`{}`), Result: json.RawMessage(`{"prediction_name":"result-only"}`), OutputText: "<svg>detail-only</svg>", ClaimToken: "private-token"}}
	base := "/accounts/degradation-check-batches"
	summary := batchHandlerRequest(router, "GET", base+"/9/items", "", "")
	require.Equal(t, http.StatusOK, summary.Code, summary.Body.String())
	for _, hidden := range []string{"output_text", "result-only", "detail-only", "private-token"} {
		require.NotContains(t, summary.Body.String(), hidden)
	}
	detail := batchHandlerRequest(router, "GET", base+"/9/items/1", "", "")
	require.Equal(t, http.StatusOK, detail.Code)
	require.Contains(t, detail.Body.String(), "detail-only")
	require.NotContains(t, detail.Body.String(), "private-token")
	require.Equal(t, http.StatusNotFound, batchHandlerRequest(router, "GET", base+"/10/items/1", "", "").Code)
	require.Equal(t, http.StatusBadRequest, batchHandlerRequest(router, "GET", base+"/0", "", "").Code)
	require.Equal(t, http.StatusBadRequest, batchHandlerRequest(router, "GET", base+"?status=other", "", "").Code)
	batch := batchHandlerRequest(router, "GET", base+"/9", "", "")
	require.NotContains(t, batch.Body.String(), "private-key")
	require.NotContains(t, batch.Body.String(), "private-hash")
}
