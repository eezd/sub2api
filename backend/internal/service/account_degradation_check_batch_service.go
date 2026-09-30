package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const degradationBatchWorkerName = "account_degradation_check_batch_worker"

var errDegradationBatchUserCanceled = errors.New("batch canceled by user")

// AccountDegradationCheckBatchService owns only local execution. PostgreSQL
// claims remain the authority for cluster concurrency and terminal transitions.
type AccountDegradationCheckBatchService struct {
	repo        AccountDegradationCheckBatchRepository
	accountRepo AccountRepository
	testService *AccountTestService
	timingWheel *TimingWheelService
	root        context.Context
	cancel      context.CancelFunc
	startOnce   sync.Once
	stopOnce    sync.Once
	wake        chan struct{}
	wg          sync.WaitGroup
	scanMu      sync.Mutex
	mu          sync.Mutex
	active      int
	stopped     bool
}

func NewAccountDegradationCheckBatchService(repo AccountDegradationCheckBatchRepository, accountRepo AccountRepository, testService *AccountTestService, timingWheel *TimingWheelService) *AccountDegradationCheckBatchService {
	ctx, cancel := context.WithCancel(context.Background())
	return &AccountDegradationCheckBatchService{repo: repo, accountRepo: accountRepo, testService: testService, timingWheel: timingWheel, root: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
}

// NormalizeDegradationBatchRequest validates and canonicalizes a create request,
// returning the stable payload hash shared by HTTP and persistent idempotency.
func NormalizeDegradationBatchRequest(request DegradationBatchCreateRequest) (DegradationBatchCreateRequest, string, error) {
	if (request.CheckType != AccountDegradationCheckModelTrace && request.CheckType != AccountDegradationCheckSVGAnimation) || len(request.Items) < 1 || len(request.Items) > 1000 {
		return request, "", ErrDegradationBatchInvalidRequest
	}
	normalized := DegradationBatchCreateRequest{CheckType: request.CheckType, Items: make([]DegradationBatchCreateItem, len(request.Items))}
	seen := make(map[int64]struct{}, len(request.Items))
	for i, item := range request.Items {
		if item.AccountID <= 0 {
			return request, "", ErrDegradationBatchInvalidRequest
		}
		if _, ok := seen[item.AccountID]; ok {
			return request, "", ErrDegradationBatchInvalidRequest
		}
		seen[item.AccountID] = struct{}{}
		normalized.Items[i].AccountID = item.AccountID
		if item.ModelID != nil {
			model := strings.TrimSpace(*item.ModelID)
			if !utf8.ValidString(model) || len(model) < 1 || len(model) > 256 {
				return request, "", ErrDegradationBatchInvalidRequest
			}
			normalized.Items[i].ModelID = &model
		}
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return request, "", err
	}
	hash := sha256.Sum256(payload)
	return normalized, hex.EncodeToString(hash[:]), nil
}

func (s *AccountDegradationCheckBatchService) available() bool {
	return s != nil && s.repo != nil && s.accountRepo != nil && s.testService != nil
}

func (s *AccountDegradationCheckBatchService) CreateBatch(ctx context.Context, operatorID int64, requestKey string, request DegradationBatchCreateRequest) (*AccountDegradationCheckBatch, error) {
	if !s.available() {
		return nil, ErrDegradationBatchUnavailable
	}
	if operatorID <= 0 || strings.TrimSpace(requestKey) == "" {
		return nil, ErrDegradationBatchInvalidRequest
	}
	normalized, hash, err := NormalizeDegradationBatchRequest(request)
	if err != nil {
		return nil, err
	}
	existing, lookupErr := s.repo.GetBatchByRequestKey(ctx, operatorID, requestKey)
	if lookupErr == nil {
		if existing.RequestHash != hash {
			return nil, ErrDegradationBatchRequestConflict
		}
		s.signal()
		return existing, nil
	}
	if !errors.Is(lookupErr, ErrDegradationBatchNotFound) {
		return nil, fmt.Errorf("%w: %v", ErrDegradationBatchUnavailable, lookupErr)
	}
	items := make([]*AccountDegradationCheckBatchItem, 0, len(normalized.Items))
	runnable := 0
	for i, input := range normalized.Items {
		item := &AccountDegradationCheckBatchItem{Position: i, AccountID: input.AccountID, Status: DegradationItemPending, Progress: json.RawMessage(`{}`)}
		if input.ModelID != nil {
			item.RequestedModel = *input.ModelID
		}
		account, lookupErr := s.accountRepo.GetByID(ctx, input.AccountID)
		if lookupErr != nil && !errors.Is(lookupErr, ErrAccountNotFound) {
			return nil, fmt.Errorf("%w: %v", ErrDegradationBatchUnavailable, lookupErr)
		}
		if account != nil {
			item.AccountName = account.Name
			item.Platform = account.Platform
			item.AccountType = account.Type
		}
		switch {
		case input.ModelID == nil:
			item.ReasonCode = "no_model_selected"
		case errors.Is(lookupErr, ErrAccountNotFound) || account == nil:
			item.ReasonCode = "account_not_found"
		case !supportsModelTraceAccount(account):
			item.ReasonCode = "unsupported_account"
		case account.IsOpenAI() && isOpenAIImageModel(account.GetMappedModel(item.RequestedModel)):
			item.ReasonCode = "image_model"
		}
		if item.ReasonCode != "" {
			item.Status = DegradationItemSkipped
		} else {
			runnable++
		}
		items = append(items, item)
	}
	if runnable == 0 {
		return nil, ErrDegradationBatchNoRunnableItems
	}
	batch, err := s.repo.CreateBatch(ctx, &AccountDegradationCheckBatch{CheckType: normalized.CheckType, CreatedBy: operatorID, RequestKey: requestKey, RequestHash: hash}, items)
	if err == nil {
		s.signal()
	}
	return batch, err
}

func (s *AccountDegradationCheckBatchService) ListBatches(ctx context.Context, p pagination.PaginationParams, f DegradationBatchFilter) ([]*AccountDegradationCheckBatch, *pagination.PaginationResult, error) {
	if !s.available() {
		return nil, nil, ErrDegradationBatchUnavailable
	}
	return s.repo.ListBatches(ctx, p, f)
}
func (s *AccountDegradationCheckBatchService) GetBatch(ctx context.Context, id int64) (*AccountDegradationCheckBatch, error) {
	if !s.available() {
		return nil, ErrDegradationBatchUnavailable
	}
	return s.repo.GetBatch(ctx, id)
}
func (s *AccountDegradationCheckBatchService) ListItems(ctx context.Context, id int64, p pagination.PaginationParams) ([]*AccountDegradationCheckBatchItem, *pagination.PaginationResult, error) {
	if !s.available() {
		return nil, nil, ErrDegradationBatchUnavailable
	}
	return s.repo.ListItems(ctx, id, p)
}
func (s *AccountDegradationCheckBatchService) GetItem(ctx context.Context, batchID, itemID int64) (*AccountDegradationCheckBatchItem, error) {
	if !s.available() {
		return nil, ErrDegradationBatchUnavailable
	}
	return s.repo.GetItem(ctx, batchID, itemID)
}
func (s *AccountDegradationCheckBatchService) CancelBatch(ctx context.Context, batchID, operatorID int64) (*AccountDegradationCheckBatch, error) {
	if !s.available() {
		return nil, ErrDegradationBatchUnavailable
	}
	batch, err := s.repo.CancelBatch(ctx, batchID, operatorID)
	if err == nil {
		s.signal()
	}
	return batch, err
}

func (s *AccountDegradationCheckBatchService) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *AccountDegradationCheckBatchService) Start() {
	if !s.available() || s.timingWheel == nil {
		return
	}
	s.startOnce.Do(func() {
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}
		s.wg.Add(1)
		s.mu.Unlock()
		s.timingWheel.ScheduleRecurring(degradationBatchWorkerName, 2*time.Second, s.signal)
		go func() {
			defer s.wg.Done()
			s.scan()
			for {
				select {
				case <-s.root.Done():
					return
				case <-s.wake:
					s.scan()
				}
			}
		}()
	})
}
func (s *AccountDegradationCheckBatchService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.cancel()
		s.mu.Unlock()
		if s.timingWheel != nil {
			s.timingWheel.Cancel(degradationBatchWorkerName)
		}
		done := make(chan struct{})
		go func() { s.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
		}
	})
}
func (s *AccountDegradationCheckBatchService) scan() {
	if !s.scanMu.TryLock() {
		return
	}
	defer s.scanMu.Unlock()
	if s.root.Err() != nil {
		return
	}
	if err := s.repo.InterruptExpiredItems(s.root); err != nil {
		return
	}
	for {
		s.mu.Lock()
		if s.stopped || s.active >= 2 {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		item, err := s.repo.ClaimNextItem(s.root)
		if err != nil || item == nil {
			return
		}
		s.mu.Lock()
		s.active++
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer func() { s.mu.Lock(); s.active--; s.mu.Unlock(); s.wg.Done(); s.signal() }()
			s.execute(item)
		}()
	}
}

func (s *AccountDegradationCheckBatchService) execute(item *AccountDegradationCheckBatchItem) {
	deadlineCtx, deadlineCancel := context.WithTimeout(s.root, 10*time.Minute)
	defer deadlineCancel()
	ctx, cancel := context.WithCancelCause(deadlineCtx)
	defer cancel(nil)
	heartbeatDone := make(chan struct{})
	heartbeatStop := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatStop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				canceled, err := s.repo.Heartbeat(ctx, item.BatchID, item.ID, item.ClaimToken)
				if err != nil {
					cancel(err)
					return
				}
				if canceled {
					cancel(errDegradationBatchUserCanceled)
					return
				}
			}
		}
	}()
	var finish DegradationItemFinish
	account, err := s.accountRepo.GetByID(ctx, item.AccountID)
	switch {
	case errors.Is(err, ErrAccountNotFound) || err == nil && account == nil:
		finish = DegradationItemFinish{Status: DegradationItemSkipped, ReasonCode: "account_not_found"}
	case err != nil:
		finish = DegradationItemFinish{Status: DegradationItemFailed, ReasonCode: "probe_failed", ErrorMessage: compactDegradationCheckError(err.Error())}
	case !supportsModelTraceAccount(account):
		finish = DegradationItemFinish{Status: DegradationItemSkipped, ReasonCode: "unsupported_account"}
	case account.IsOpenAI() && isOpenAIImageModel(account.GetMappedModel(item.RequestedModel)):
		finish = DegradationItemFinish{Status: DegradationItemSkipped, ReasonCode: "image_model"}
	default:
		record, runErr := s.testService.RunDegradationCheck(ctx, item.AccountID, item.CheckType, item.RequestedModel, func(event TestEvent) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			var progress json.RawMessage
			testedModel := ""
			switch event.Type {
			case "model_trace_start", "svg_animation_start":
				testedModel = event.Model
				progress = json.RawMessage(`{}`)
			case "model_trace_progress":
				raw, err := json.Marshal(event.Data)
				if err != nil {
					return err
				}
				var allowed modelTraceProgress
				if err = json.Unmarshal(raw, &allowed); err != nil {
					return err
				}
				progress, err = json.Marshal(allowed)
				if err != nil {
					return err
				}
			default:
				return nil
			}
			err := s.repo.UpdateProgress(ctx, item.BatchID, item.ID, item.ClaimToken, testedModel, progress)
			if err != nil {
				cancel(err)
			}
			return err
		})
		finish = DegradationItemFinish{Status: DegradationItemSucceeded, Result: record}
		if runErr != nil {
			finish.Status = DegradationItemFailed
			finish.ReasonCode = "probe_failed"
			finish.ErrorMessage = compactDegradationCheckError(runErr.Error())
			if record == nil && errors.Is(runErr, ErrAccountNotFound) {
				finish.Status = DegradationItemSkipped
				finish.ReasonCode = "account_not_found"
				finish.ErrorMessage = ""
			}
		}
	}
	close(heartbeatStop)
	<-heartbeatDone
	// Ownership/DB failures stop probes but must not pretend a result committed.
	// Leave the claim for lease recovery; never replay its upstream work.
	cause := context.Cause(ctx)
	if cause != nil {
		switch {
		case errors.Is(cause, errDegradationBatchUserCanceled):
			finish.Status = DegradationItemCanceled
			finish.ReasonCode = "user_canceled"
			finish.ErrorMessage = "检测已取消"
		case s.root.Err() != nil:
			finish.Status = DegradationItemInterrupted
			finish.ReasonCode = "worker_interrupted"
			finish.ErrorMessage = "执行中断，未自动重试"
		case errors.Is(cause, context.DeadlineExceeded):
			finish.Status = DegradationItemFailed
			finish.ReasonCode = "timeout"
			finish.ErrorMessage = "检测超时"
		default:
			return
		}
	}
	commitCtx, commitCancel := context.WithTimeout(context.WithoutCancel(s.root), 5*time.Second)
	defer commitCancel()
	_, _ = s.repo.FinishItem(commitCtx, item.BatchID, item.ID, item.ClaimToken, finish)
}
