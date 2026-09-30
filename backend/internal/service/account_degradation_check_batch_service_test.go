//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// Embedded unused methods deliberately fail if the worker unexpectedly invokes
// another repository operation. All state touched by execution is locked.
type batchWorkerTestRepo struct {
	AccountDegradationCheckBatchRepository
	mu                sync.Mutex
	finishes          []DegradationItemFinish
	progress          []json.RawMessage
	progressErr       error
	finishErr         error
	created           []*AccountDegradationCheckBatchItem
	heartbeatErr      error
	heartbeatCanceled bool
	heartbeatBarrier  <-chan struct{}
	existing          *AccountDegradationCheckBatch
	pending           []*AccountDegradationCheckBatchItem
	claimed           int
}

func (r *batchWorkerTestRepo) GetBatchByRequestKey(context.Context, int64, string) (*AccountDegradationCheckBatch, error) {
	if r.existing != nil {
		return r.existing, nil
	}
	return nil, ErrDegradationBatchNotFound
}
func (r *batchWorkerTestRepo) Heartbeat(ctx context.Context, _ int64, _ int64, _ string) (bool, error) {
	if r.heartbeatBarrier != nil {
		select {
		case <-r.heartbeatBarrier:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return r.heartbeatCanceled, r.heartbeatErr
}
func (r *batchWorkerTestRepo) UpdateProgress(_ context.Context, _ int64, _ int64, _ string, _ string, p json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progress = append(r.progress, append(json.RawMessage(nil), p...))
	return r.progressErr
}
func (r *batchWorkerTestRepo) FinishItem(_ context.Context, _ int64, _ int64, _ string, f DegradationItemFinish) (*AccountDegradationCheckBatchItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finishErr != nil {
		return nil, r.finishErr
	}
	r.finishes = append(r.finishes, f)
	return &AccountDegradationCheckBatchItem{Status: f.Status}, nil
}
func (r *batchWorkerTestRepo) CreateBatch(_ context.Context, b *AccountDegradationCheckBatch, items []*AccountDegradationCheckBatchItem) (*AccountDegradationCheckBatch, error) {
	r.created = items
	return b, nil
}
func (r *batchWorkerTestRepo) InterruptExpiredItems(context.Context) error { return nil }
func (r *batchWorkerTestRepo) ClaimNextItem(context.Context) (*AccountDegradationCheckBatchItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) == 0 {
		return nil, nil
	}
	item := r.pending[0]
	r.pending = r.pending[1:]
	r.claimed++
	return item, nil
}

type batchAccountTestRepo struct{ AccountRepository }

func (r *batchAccountTestRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	account, err := r.AccountRepository.GetByID(ctx, id)
	if account == nil && err != nil {
		return nil, ErrAccountNotFound
	}
	return account, err
}

type batchControlledUpstream struct {
	mu        sync.Mutex
	requests  []string
	accounts  []int64
	texts     map[int64]string
	text      string
	reject    int
	truncated bool
	entered   chan struct{}
}

func (u *batchControlledUpstream) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxy, accountID, concurrency, nil)
}
func (u *batchControlledUpstream) DoWithTLS(req *http.Request, _ string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err = json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.requests = append(u.requests, fmt.Sprint(payload["model"]))
	u.accounts = append(u.accounts, accountID)
	n := len(u.requests)
	u.mu.Unlock()
	if u.entered != nil {
		u.entered <- struct{}{}
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	text := u.text
	if accountText, ok := u.texts[accountID]; ok {
		text = accountText
	}
	if n <= u.reject {
		text = "invalid fingerprint"
	}
	var stream string
	if strings.Contains(req.URL.Path, "messages") {
		reason := "end_turn"
		if u.truncated {
			reason = "max_tokens"
		}
		stream = fmt.Sprintf("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%s}}\n\ndata: {\"type\":\"message_stop\"}\n\n", modelTraceJSONString(text), modelTraceJSONString(reason))
	} else {
		stream = fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":%s}\n\ndata: {\"type\":\"response.completed\"}\n\n", modelTraceJSONString(text))
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
}
func batchFingerprintText() string {
	numbers := make([]string, 220)
	for i := range numbers {
		numbers[i] = fmt.Sprint(i%modelTraceValueMax + 1)
	}
	return strings.Join(numbers, ",")
}
func batchWorkerFixture(account *Account, u HTTPUpstream, r *batchWorkerTestRepo) *AccountDegradationCheckBatchService {
	core, _ := newDegradationCheckTestService(account, u)
	core.cfg = &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}
	return NewAccountDegradationCheckBatchService(r, &batchAccountTestRepo{AccountRepository: core.accountRepo}, core, nil)
}
func batchWorkerItem(id int64, kind AccountDegradationCheckType) *AccountDegradationCheckBatchItem {
	return &AccountDegradationCheckBatchItem{ID: 1, BatchID: 1, AccountID: id, CheckType: kind, RequestedModel: "public-alias", ClaimToken: "claim"}
}

func TestDegradationCheckBatchModelTraceSamplesAndAliases(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		for _, reject := range []int{0, 3, 6} {
			t.Run(fmt.Sprintf("%s-rejected-%d", platform, reject), func(t *testing.T) {
				account := newOpenAIDegradationTestAccount(801)
				if platform == PlatformAnthropic {
					account = newAnthropicDegradationTestAccount(801)
					account.Type = AccountTypeAPIKey
					account.Credentials = map[string]any{"api_key": "test-key"}
				}
				account.Credentials["model_mapping"] = map[string]any{"public-alias": "actual-text-model"}
				upstream := &batchControlledUpstream{text: batchFingerprintText(), reject: reject}
				repo := &batchWorkerTestRepo{}
				worker := batchWorkerFixture(account, upstream, repo)
				worker.execute(batchWorkerItem(801, AccountDegradationCheckModelTrace))
				require.Len(t, repo.finishes, 1)
				expected := DegradationItemSucceeded
				requests := 3 + reject
				if requests > 6 {
					requests = 6
					expected = DegradationItemFailed
				}
				require.Equal(t, expected, repo.finishes[0].Status)
				require.Len(t, upstream.requests, requests)
				for _, model := range upstream.requests {
					require.Equal(t, "actual-text-model", model)
				}
				require.NotNil(t, repo.finishes[0].Result)
			})
		}
	}
}
func TestDegradationCheckBatchSVGFailureAndFollowingSuccess(t *testing.T) {
	for _, test := range []struct {
		text      string
		truncated bool
		status    string
	}{{"not SVG", false, DegradationItemFailed}, {"<svg><animate /></svg>", true, DegradationItemFailed}, {"<svg><animate /></svg>", false, DegradationItemSucceeded}} {
		upstream := &batchControlledUpstream{text: test.text, truncated: test.truncated}
		repo := &batchWorkerTestRepo{}
		worker := batchWorkerFixture(newAnthropicDegradationTestAccount(802), upstream, repo)
		worker.execute(batchWorkerItem(802, AccountDegradationCheckSVGAnimation))
		require.Len(t, upstream.requests, 1)
		require.Equal(t, test.status, repo.finishes[0].Status)
	}
}
func TestDegradationCheckBatchProgressFailureSpendsNoQuota(t *testing.T) {
	upstream := &batchControlledUpstream{text: batchFingerprintText()}
	repo := &batchWorkerTestRepo{progressErr: errors.New("database unavailable")}
	worker := batchWorkerFixture(newOpenAIDegradationTestAccount(803), upstream, repo)
	worker.execute(batchWorkerItem(803, AccountDegradationCheckModelTrace))
	require.Empty(t, upstream.requests)
	require.Empty(t, repo.finishes)
}
func TestDegradationCheckBatchStopInterruptsWithoutRetry(t *testing.T) {
	upstream := &batchControlledUpstream{entered: make(chan struct{}, 1)}
	repo := &batchWorkerTestRepo{}
	worker := batchWorkerFixture(newOpenAIDegradationTestAccount(804), upstream, repo)
	done := make(chan struct{})
	go func() { worker.execute(batchWorkerItem(804, AccountDegradationCheckSVGAnimation)); close(done) }()
	<-upstream.entered
	worker.Stop()
	<-done
	require.Len(t, upstream.requests, 1)
	require.Len(t, repo.finishes, 1)
	require.Equal(t, DegradationItemInterrupted, repo.finishes[0].Status)
	require.Equal(t, "worker_interrupted", repo.finishes[0].ReasonCode)
}
func TestDegradationCheckBatchDeletedAccountSkipsWithoutProbe(t *testing.T) {
	upstream := &batchControlledUpstream{}
	repo := &batchWorkerTestRepo{}
	worker := batchWorkerFixture(newOpenAIDegradationTestAccount(805), upstream, repo)
	worker.execute(batchWorkerItem(999, AccountDegradationCheckSVGAnimation))
	require.Empty(t, upstream.requests)
	require.Len(t, repo.finishes, 1)
	require.Equal(t, DegradationItemSkipped, repo.finishes[0].Status)
	require.Nil(t, repo.finishes[0].Result)
}
func TestDegradationCheckBatchNormalizationIdentity(t *testing.T) {
	model := " model "
	trimmed := "model"
	request := DegradationBatchCreateRequest{CheckType: AccountDegradationCheckModelTrace, Items: []DegradationBatchCreateItem{{AccountID: 1, ModelID: &model}, {AccountID: 2}}}
	normalized, hash, err := NormalizeDegradationBatchRequest(request)
	require.NoError(t, err)
	require.Equal(t, "model", *normalized.Items[0].ModelID)
	request.Items[0].ModelID = &trimmed
	_, same, err := NormalizeDegradationBatchRequest(request)
	require.NoError(t, err)
	require.Equal(t, hash, same)
	request.Items[0], request.Items[1] = request.Items[1], request.Items[0]
	_, reordered, err := NormalizeDegradationBatchRequest(request)
	require.NoError(t, err)
	require.NotEqual(t, hash, reordered)
	empty := " "
	request.Items[0].ModelID = &empty
	_, _, err = NormalizeDegradationBatchRequest(request)
	require.ErrorIs(t, err, ErrDegradationBatchInvalidRequest)
}

func TestDegradationCheckBatchHeartbeatStopsRunningQuota(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		canceled bool
	}{{"claim lost", ErrDegradationBatchClaimLost, false}, {"database failed", errors.New("database offline"), false}, {"user canceled", nil, true}} {
		t.Run(test.name, func(t *testing.T) {
			barrier := make(chan struct{})
			upstream := &batchControlledUpstream{entered: make(chan struct{}, 1)}
			repo := &batchWorkerTestRepo{heartbeatErr: test.err, heartbeatCanceled: test.canceled, heartbeatBarrier: barrier}
			worker := batchWorkerFixture(newOpenAIDegradationTestAccount(806), upstream, repo)
			done := make(chan struct{})
			go func() { worker.execute(batchWorkerItem(806, AccountDegradationCheckModelTrace)); close(done) }()
			<-upstream.entered
			close(barrier)
			<-done
			require.Len(t, upstream.requests, 1)
			if test.canceled {
				require.Len(t, repo.finishes, 1)
				require.Equal(t, DegradationItemCanceled, repo.finishes[0].Status)
			} else {
				require.Empty(t, repo.finishes)
			}
		})
	}
}
func TestDegradationCheckBatchCommitFailureDoesNotReplayProbe(t *testing.T) {
	upstream := &batchControlledUpstream{text: "<svg><animate /></svg>"}
	repo := &batchWorkerTestRepo{finishErr: errors.New("commit failed")}
	worker := batchWorkerFixture(newOpenAIDegradationTestAccount(807), upstream, repo)
	worker.execute(batchWorkerItem(807, AccountDegradationCheckSVGAnimation))
	require.Len(t, upstream.requests, 1)
	require.Empty(t, repo.finishes)
}
func TestDegradationCheckBatchIdempotencySurvivesDeletedAccounts(t *testing.T) {
	model := "selected-model"
	request := DegradationBatchCreateRequest{CheckType: AccountDegradationCheckSVGAnimation, Items: []DegradationBatchCreateItem{{AccountID: 999, ModelID: &model}}}
	_, hash, err := NormalizeDegradationBatchRequest(request)
	require.NoError(t, err)
	repo := &batchWorkerTestRepo{existing: &AccountDegradationCheckBatch{ID: 71, RequestHash: hash}}
	worker := batchWorkerFixture(newOpenAIDegradationTestAccount(808), &batchControlledUpstream{}, repo)
	batch, err := worker.CreateBatch(context.Background(), 1, "submission", request)
	require.NoError(t, err)
	require.Equal(t, int64(71), batch.ID)
	require.Empty(t, repo.created)
	other := "different-model"
	request.Items[0].ModelID = &other
	_, err = worker.CreateBatch(context.Background(), 1, "submission", request)
	require.ErrorIs(t, err, ErrDegradationBatchRequestConflict)
}
func TestDegradationCheckBatchCreateFreezesMixedItems(t *testing.T) {
	upstream := &batchControlledUpstream{}
	repo := &batchWorkerTestRepo{}
	worker := batchWorkerFixture(newOpenAIDegradationTestAccount(809), upstream, repo)
	model := " selected "
	batch, err := worker.CreateBatch(context.Background(), 1, "new-submission", DegradationBatchCreateRequest{CheckType: AccountDegradationCheckModelTrace, Items: []DegradationBatchCreateItem{{AccountID: 809, ModelID: &model}, {AccountID: 999, ModelID: &model}, {AccountID: 888}}})
	require.NoError(t, err)
	require.NotEmpty(t, batch.RequestHash)
	require.Equal(t, DegradationItemPending, repo.created[0].Status)
	require.Equal(t, "selected", repo.created[0].RequestedModel)
	require.Equal(t, "account_not_found", repo.created[1].ReasonCode)
	require.Equal(t, "no_model_selected", repo.created[2].ReasonCode)
	model = "mutated"
	require.Equal(t, "selected", repo.created[0].RequestedModel)
	require.Empty(t, upstream.requests)
}

func TestDegradationCheckBatchScannerBoundsLocalExecutions(t *testing.T) {
	upstream := &batchControlledUpstream{entered: make(chan struct{}, 2)}
	repo := &batchWorkerTestRepo{}
	worker := batchWorkerFixture(newOpenAIDegradationTestAccount(810), upstream, repo)
	for i := int64(0); i < 3; i++ {
		item := batchWorkerItem(810+i, AccountDegradationCheckSVGAnimation)
		item.ID = i + 1
		repo.pending = append(repo.pending, item)
	}
	accountRepo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{810: newOpenAIDegradationTestAccount(810), 811: newAnthropicDegradationTestAccount(811), 812: newOpenAIDegradationTestAccount(812)}}}
	worker.accountRepo = accountRepo
	worker.testService.accountRepo = accountRepo
	worker.scan()
	<-upstream.entered
	<-upstream.entered
	worker.scan()
	repo.mu.Lock()
	claimed := repo.claimed
	pending := len(repo.pending)
	repo.mu.Unlock()
	require.Equal(t, 2, claimed)
	require.Equal(t, 1, pending)
	worker.Stop()
	require.Len(t, upstream.requests, 2)
	require.Len(t, repo.finishes, 2)
	for _, finish := range repo.finishes {
		require.Equal(t, DegradationItemInterrupted, finish.Status)
	}
}

func TestDegradationCheckBatchMismatchAndUnknownAreSuccessful(t *testing.T) {
	account := newOpenAIDegradationTestAccount(813)
	upstream := &batchControlledUpstream{text: batchFingerprintText()}
	repo := &batchWorkerTestRepo{}
	worker := batchWorkerFixture(account, upstream, repo)
	worker.execute(batchWorkerItem(813, AccountDegradationCheckModelTrace))
	require.Equal(t, DegradationItemSucceeded, repo.finishes[0].Status)
	var unknown ModelTraceResult
	require.NoError(t, json.Unmarshal(repo.finishes[0].Result.Result, &unknown))
	require.Nil(t, unknown.MatchesExpected)
	bank, err := loadModelTraceBank()
	require.NoError(t, err)
	expected := ""
	for _, model := range bank.Models {
		if model.ID != unknown.Prediction {
			expected = model.ID
			break
		}
	}
	require.NotEmpty(t, expected)
	account.Credentials["model_mapping"] = map[string]any{"public-alias": expected}
	worker.execute(batchWorkerItem(813, AccountDegradationCheckModelTrace))
	require.Len(t, upstream.requests, 6)
	require.Equal(t, DegradationItemSucceeded, repo.finishes[1].Status)
	var mismatch ModelTraceResult
	require.NoError(t, json.Unmarshal(repo.finishes[1].Result.Result, &mismatch))
	require.NotNil(t, mismatch.MatchesExpected)
	require.False(t, *mismatch.MatchesExpected)
}

// The core resolves the account again after the worker's eligibility check.
// Changes in that window must not spend quota or disguise storage failures.
type batchChangingAccountRepo struct {
	AccountRepository
	account *Account
	err     error
	reads   int
}

func (r *batchChangingAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	if r.reads == 1 {
		return r.account, nil
	}
	return nil, r.err
}

func TestDegradationCheckBatchAccountChangesBeforeCoreProbe(t *testing.T) {
	for _, kind := range []AccountDegradationCheckType{AccountDegradationCheckModelTrace, AccountDegradationCheckSVGAnimation} {
		for _, lookupErr := range []error{nil, ErrAccountNotFound, errors.New("database offline")} {
			t.Run(fmt.Sprintf("%s/%v", kind, lookupErr), func(t *testing.T) {
				upstream := &batchControlledUpstream{}
				repo := &batchWorkerTestRepo{}
				account := newOpenAIDegradationTestAccount(814)
				worker := batchWorkerFixture(account, upstream, repo)
				accounts := &batchChangingAccountRepo{account: account, err: lookupErr}
				worker.accountRepo = accounts
				worker.testService.accountRepo = accounts
				worker.execute(batchWorkerItem(account.ID, kind))
				require.Empty(t, upstream.requests)
				require.Len(t, repo.finishes, 1)
				if lookupErr == nil || errors.Is(lookupErr, ErrAccountNotFound) {
					require.Equal(t, DegradationItemSkipped, repo.finishes[0].Status)
					require.Equal(t, "account_not_found", repo.finishes[0].ReasonCode)
				} else {
					require.Equal(t, DegradationItemFailed, repo.finishes[0].Status)
					require.Contains(t, repo.finishes[0].ErrorMessage, "database offline")
				}
			})
		}
	}
}

func TestDegradationCheckBatchMixedQueueContinuesAfterFailure(t *testing.T) {
	upstream := &batchControlledUpstream{texts: map[int64]string{815: "not SVG", 816: "<svg><animate /></svg>"}}
	repo := &batchWorkerTestRepo{}
	worker := batchWorkerFixture(newOpenAIDegradationTestAccount(815), upstream, repo)
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{815: newOpenAIDegradationTestAccount(815), 816: newAnthropicDegradationTestAccount(816)}}}
	worker.accountRepo = &batchAccountTestRepo{AccountRepository: accounts}
	worker.testService.accountRepo = accounts
	for i, accountID := range []int64{815, 816, 999} {
		item := batchWorkerItem(accountID, AccountDegradationCheckSVGAnimation)
		item.ID = int64(i + 1)
		repo.pending = append(repo.pending, item)
	}
	worker.scan()
	worker.wg.Wait()
	worker.scan()
	worker.wg.Wait()
	require.ElementsMatch(t, []int64{815, 816}, upstream.accounts)
	require.Equal(t, 3, repo.claimed)
	require.Empty(t, repo.pending)
	statuses := make([]string, 0, len(repo.finishes))
	for _, finish := range repo.finishes {
		statuses = append(statuses, finish.Status)
	}
	require.ElementsMatch(t, []string{DegradationItemFailed, DegradationItemSucceeded, DegradationItemSkipped}, statuses)
	worker.Stop()
}
