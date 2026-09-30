//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// These fixtures commit normally: the repository must own its PostgreSQL
// transactions, including the atomic item/history commit and advisory claim lock.
type degradationBatchFixture struct {
	t          *testing.T
	ctx        context.Context
	repo       service.AccountDegradationCheckBatchRepository
	userID     int64
	accountIDs []int64
	batchIDs   []int64
}

func newDegradationBatchFixture(t *testing.T) *degradationBatchFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	user := mustCreateUser(t, testEntClient(t), &service.User{Role: service.RoleAdmin})
	f := &degradationBatchFixture{t: t, ctx: ctx, repo: NewAccountDegradationCheckBatchRepository(integrationDB), userID: user.ID}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, id := range f.batchIDs {
			_, err := integrationDB.ExecContext(ctx, `DELETE FROM account_degradation_check_batches WHERE id=$1`, id)
			require.NoError(t, err)
		}
		for _, id := range f.accountIDs {
			_, err := integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, id)
			require.NoError(t, err)
		}
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, f.userID)
		require.NoError(t, err)
	})
	return f
}

func (f *degradationBatchFixture) account() int64 {
	f.t.Helper()
	a := mustCreateAccount(f.t, testEntClient(f.t), &service.Account{
		Name: f.t.Name(), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
	})
	f.accountIDs = append(f.accountIDs, a.ID)
	return a.ID
}

func (f *degradationBatchFixture) batch(ids ...int64) *service.AccountDegradationCheckBatch {
	f.t.Helper()
	items := make([]*service.AccountDegradationCheckBatchItem, 0, len(ids))
	for position, id := range ids {
		items = append(items, &service.AccountDegradationCheckBatchItem{
			Position: position, AccountID: id, AccountName: f.t.Name(),
			Platform: service.PlatformOpenAI, AccountType: service.AccountTypeAPIKey,
			RequestedModel: "public-alias", Status: service.DegradationItemPending,
			Progress: json.RawMessage(`{}`),
		})
	}
	batch, err := f.repo.CreateBatch(f.ctx, &service.AccountDegradationCheckBatch{
		CheckType: service.AccountDegradationCheckModelTrace, CreatedBy: f.userID,
		RequestKey: fmt.Sprintf("batch-%d-%d", f.userID, len(f.batchIDs)), RequestHash: "normalized-request-hash",
		Status: service.DegradationBatchPending,
	}, items)
	require.NoError(f.t, err)
	f.batchIDs = append(f.batchIDs, batch.ID)
	return batch
}

func (f *degradationBatchFixture) claim() *service.AccountDegradationCheckBatchItem {
	f.t.Helper()
	item, err := f.repo.ClaimNextItem(f.ctx)
	require.NoError(f.t, err)
	require.NotNil(f.t, item)
	require.Contains(f.t, f.batchIDs, item.BatchID, "claim must belong to this isolated fixture")
	require.Equal(f.t, service.DegradationItemRunning, item.Status)
	require.NotEmpty(f.t, item.ClaimToken)
	require.NotNil(f.t, item.LeaseExpiresAt)
	return item
}

func degradationBatchSuccess(item *service.AccountDegradationCheckBatchItem) service.DegradationItemFinish {
	return service.DegradationItemFinish{
		Status: service.DegradationItemSucceeded,
		Result: &service.AccountDegradationCheckResult{
			AccountID: item.AccountID, CheckType: item.CheckType,
			RequestedModel: item.RequestedModel, TestedModel: "resolved-model",
			Status:     service.AccountDegradationCheckStatusSuccess,
			Result:     json.RawMessage(`{"matches_expected":false,"prediction":"other-model","prediction_name":"Other Model","probability":0.75}`),
			OutputText: "collected probe output",
		},
	}
}

func (f *degradationBatchFixture) history(accountID int64) []*service.AccountDegradationCheckResult {
	f.t.Helper()
	history, err := NewAccountDegradationCheckRepository(integrationDB).ListByAccountID(f.ctx, accountID, service.AccountDegradationCheckModelTrace, 50)
	require.NoError(f.t, err)
	return history
}

func (f *degradationBatchFixture) get(item *service.AccountDegradationCheckBatchItem) *service.AccountDegradationCheckBatchItem {
	f.t.Helper()
	stored, err := f.repo.GetItem(f.ctx, item.BatchID, item.ID)
	require.NoError(f.t, err)
	return stored
}

func (f *degradationBatchFixture) expire(item *service.AccountDegradationCheckBatchItem) {
	f.t.Helper()
	_, err := integrationDB.ExecContext(f.ctx, `UPDATE account_degradation_check_batch_items SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, item.ID)
	require.NoError(f.t, err)
}

func TestDegradationCheckBatchConcurrentClaims(t *testing.T) {
	for _, count := range []int{1, 5} {
		t.Run(fmt.Sprintf("pending_%d", count), func(t *testing.T) {
			f := newDegradationBatchFixture(t)
			ids := make([]int64, count)
			for i := range ids {
				ids[i] = f.account()
			}
			f.batch(ids...)
			start := make(chan struct{})
			type claimResult struct {
				item *service.AccountDegradationCheckBatchItem
				err  error
			}
			results := make(chan claimResult, 8)
			var workers sync.WaitGroup
			for range 8 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					// Independent repository instances share only PostgreSQL.
					item, err := NewAccountDegradationCheckBatchRepository(integrationDB).ClaimNextItem(f.ctx)
					results <- claimResult{item, err}
				}()
			}
			close(start)
			workers.Wait()
			close(results)
			claimed := map[int64]bool{}
			for result := range results {
				require.NoError(t, result.err)
				if result.item == nil {
					continue
				}
				require.False(t, claimed[result.item.ID], "same pending item claimed twice")
				claimed[result.item.ID] = true
			}
			want := count
			if want > 2 {
				want = 2
			}
			require.Len(t, claimed, want)
			var effective int
			require.NoError(t, integrationDB.QueryRowContext(f.ctx, `SELECT count(*) FROM account_degradation_check_batch_items WHERE status='running' AND lease_expires_at>NOW()`).Scan(&effective))
			require.Equal(t, want, effective, "cluster claim limit must hold after concurrent transactions")
		})
	}
}

func TestDegradationCheckBatchHeartbeatSerializesWithClaimAdmission(t *testing.T) {
	f := newDegradationBatchFixture(t)
	f.batch(f.account())
	item := f.claim()

	admission, err := integrationDB.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	defer admission.Rollback()
	var blockerPID int
	require.NoError(t, admission.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))
	_, err = admission.ExecContext(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('sub2api:degradation-check-claim',0))`)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		canceled, err := NewAccountDegradationCheckBatchRepository(integrationDB).Heartbeat(f.ctx, item.BatchID, item.ID, item.ClaimToken)
		if err == nil && canceled {
			err = fmt.Errorf("unexpected cancellation while renewing claim")
		}
		done <- err
	}()

	// Observe the real advisory lock wait: lease renewal and new admission must
	// never pass each other around the old lease expiry boundary.
	for {
		var waiting bool
		err := integrationDB.QueryRowContext(f.ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, blockerPID).Scan(&waiting)
		require.NoError(t, err)
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("heartbeat bypassed claim admission lock: %v", err)
		default:
		}
	}

	require.NoError(t, admission.Commit())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
}

func TestDegradationCheckBatchHeartbeatDoesNotBlockUnrelatedAdmission(t *testing.T) {
	f := newDegradationBatchFixture(t)
	first := f.batch(f.account())
	item := f.claim()
	second := f.batch(f.account())

	blocking, err := integrationDB.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	defer blocking.Rollback()
	var blockerPID int
	require.NoError(t, blocking.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))
	_, err = blocking.ExecContext(f.ctx, `SELECT id FROM account_degradation_check_batches WHERE id=$1 FOR UPDATE`, first.ID)
	require.NoError(t, err)

	heartbeatDone := make(chan error, 1)
	go func() {
		_, err := NewAccountDegradationCheckBatchRepository(integrationDB).Heartbeat(f.ctx, item.BatchID, item.ID, item.ClaimToken)
		heartbeatDone <- err
	}()
	for {
		var waiting bool
		err := integrationDB.QueryRowContext(f.ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, blockerPID).Scan(&waiting)
		require.NoError(t, err)
		if waiting {
			break
		}
		select {
		case err := <-heartbeatDone:
			t.Fatalf("heartbeat bypassed parent lock: %v", err)
		default:
		}
	}

	claimCtx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	next, claimErr := NewAccountDegradationCheckBatchRepository(integrationDB).ClaimNextItem(claimCtx)
	cancel()
	require.NoError(t, blocking.Commit())
	select {
	case heartbeatErr := <-heartbeatDone:
		require.NoError(t, heartbeatErr)
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	require.NoError(t, claimErr)
	require.NotNil(t, next)
	require.Equal(t, second.ID, next.BatchID)
}

func TestDegradationCheckBatchFinishCannotCommitAfterLeaseTakeover(t *testing.T) {
	f := newDegradationBatchFixture(t)
	accountID := f.account()
	first := f.batch(accountID)
	item := f.claim()
	second := f.batch(accountID)
	_, err := integrationDB.ExecContext(f.ctx, `UPDATE account_degradation_check_batch_items SET lease_expires_at=clock_timestamp()+interval '2 seconds' WHERE id=$1`, item.ID)
	require.NoError(t, err)

	blocking, err := integrationDB.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	defer blocking.Rollback()
	var blockerPID int
	require.NoError(t, blocking.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))
	_, err = blocking.ExecContext(f.ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, accountID)
	require.NoError(t, err)

	type finishResult struct {
		item *service.AccountDegradationCheckBatchItem
		err  error
	}
	finishDone := make(chan finishResult, 1)
	go func() {
		stored, err := NewAccountDegradationCheckBatchRepository(integrationDB).FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, degradationBatchSuccess(item))
		finishDone <- finishResult{stored, err}
	}()
	for {
		var waiting bool
		err := integrationDB.QueryRowContext(f.ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, blockerPID).Scan(&waiting)
		require.NoError(t, err)
		if waiting {
			break
		}
		select {
		case result := <-finishDone:
			t.Fatalf("finish bypassed account lock: item=%+v error=%v", result.item, result.err)
		default:
		}
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var expired bool
		require.NoError(t, integrationDB.QueryRowContext(f.ctx, `SELECT lease_expires_at<=clock_timestamp() FROM account_degradation_check_batch_items WHERE id=$1`, item.ID).Scan(&expired))
		if expired {
			break
		}
		select {
		case <-ticker.C:
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
	}

	next, err := NewAccountDegradationCheckBatchRepository(integrationDB).ClaimNextItem(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, second.ID, next.BatchID)
	require.Equal(t, accountID, next.AccountID)
	require.NoError(t, blocking.Commit())
	select {
	case result := <-finishDone:
		require.ErrorIs(t, result.err, service.ErrDegradationBatchClaimLost)
		require.Nil(t, result.item)
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	require.Equal(t, service.DegradationItemRunning, f.get(item).Status)
	require.Empty(t, f.history(accountID))
	require.Equal(t, first.ID, item.BatchID)
}

func TestDegradationCheckBatchSameAccountExclusion(t *testing.T) {
	f := newDegradationBatchFixture(t)
	shared, other := f.account(), f.account()
	first := f.batch(shared)
	second := f.batch(shared, other)
	a, b := f.claim(), f.claim()
	require.Equal(t, first.ID, a.BatchID)
	require.Equal(t, second.ID, b.BatchID)
	require.Equal(t, other, b.AccountID, "FIFO must skip an account with an effective claim in another batch")
	_, err := f.repo.FinishItem(f.ctx, a.BatchID, a.ID, a.ClaimToken, degradationBatchSuccess(a))
	require.NoError(t, err)
	c := f.claim()
	require.Equal(t, second.ID, c.BatchID)
	require.Equal(t, shared, c.AccountID)
}

func TestDegradationCheckBatchDuplicateFinishAndSnapshots(t *testing.T) {
	f := newDegradationBatchFixture(t)
	batch := f.batch(f.account())
	item := f.claim()
	finish := degradationBatchSuccess(item)
	stored, err := f.repo.FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, finish)
	require.NoError(t, err)
	require.Equal(t, service.DegradationItemSucceeded, stored.Status)
	require.NotNil(t, stored.HistoryID)
	require.JSONEq(t, string(finish.Result.Result), string(stored.Result))
	require.Equal(t, finish.Result.OutputText, stored.OutputText)
	// A later invocation must not overwrite the first snapshot or add history.
	finish.Result.OutputText = "late replacement"
	finish.Result.Result = json.RawMessage(`{"different":true}`)
	again, err := f.repo.FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, finish)
	require.NoError(t, err)
	require.Equal(t, stored.HistoryID, again.HistoryID)
	require.Equal(t, stored.OutputText, again.OutputText)
	require.JSONEq(t, string(stored.Result), string(again.Result))
	history := f.history(item.AccountID)
	require.Len(t, history, 1)
	require.Equal(t, *stored.HistoryID, history[0].ID)
	require.Equal(t, service.AccountDegradationCheckStatusSuccess, history[0].Status)
	require.Equal(t, stored.OutputText, history[0].OutputText)
	_, err = f.repo.FinishItem(f.ctx, item.BatchID, item.ID, "stale-token", finish)
	require.ErrorIs(t, err, service.ErrDegradationBatchClaimLost)
	require.Len(t, f.history(item.AccountID), 1)
	complete, err := f.repo.GetBatch(f.ctx, batch.ID)
	require.NoError(t, err)
	require.Equal(t, service.DegradationBatchCompleted, complete.Status)
	require.Equal(t, 1, complete.Counts.Succeeded, "fingerprint mismatch remains execution success")
	cancelCompleted, err := f.repo.CancelBatch(f.ctx, batch.ID, f.userID)
	require.NoError(t, err)
	require.Equal(t, service.DegradationBatchCompleted, cancelCompleted.Status)
	require.Nil(t, cancelCompleted.CancelRequestedAt, "canceling a completed batch must be a no-op")
	require.Equal(t, complete.FinishedAt, cancelCompleted.FinishedAt)
	items, _, err := f.repo.ListItems(f.ctx, batch.ID, pagination.PaginationParams{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Empty(t, items[0].Result)
	require.Empty(t, items[0].OutputText)
	require.Empty(t, items[0].ClaimToken)
	require.Nil(t, items[0].LeaseExpiresAt)
	require.NotNil(t, items[0].ModelTraceSummary)
	require.NotNil(t, items[0].ModelTraceSummary.MatchesExpected)
	require.False(t, *items[0].ModelTraceSummary.MatchesExpected)
	require.Equal(t, "Other Model", items[0].ModelTraceSummary.PredictionName)
	require.Equal(t, 0.75, items[0].ModelTraceSummary.Probability)
}

func TestDegradationCheckBatchCancelFinishOrdering(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_first_%t", cancelFirst), func(t *testing.T) {
			f := newDegradationBatchFixture(t)
			batch := f.batch(f.account(), f.account())
			item := f.claim()
			finish := degradationBatchSuccess(item)
			cancelRequested, err := f.repo.Heartbeat(f.ctx, item.BatchID, item.ID, item.ClaimToken)
			require.NoError(t, err)
			require.False(t, cancelRequested, "successful renewal without cancellation returns false")
			if cancelFirst {
				canceled, err := f.repo.CancelBatch(f.ctx, batch.ID, f.userID)
				require.NoError(t, err)
				require.Equal(t, service.DegradationBatchCanceling, canceled.Status)
				cancelRequested, err := f.repo.Heartbeat(f.ctx, item.BatchID, item.ID, item.ClaimToken)
				require.NoError(t, err)
				require.True(t, cancelRequested)
			}
			stored, err := f.repo.FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, finish)
			require.NoError(t, err)
			if !cancelFirst {
				_, err = f.repo.CancelBatch(f.ctx, batch.ID, f.userID)
				require.NoError(t, err)
			}
			wantStatus, wantHistory := service.DegradationItemSucceeded, service.AccountDegradationCheckStatusSuccess
			if cancelFirst {
				wantStatus, wantHistory = service.DegradationItemCanceled, service.AccountDegradationCheckStatusError
			}
			require.Equal(t, wantStatus, stored.Status)
			require.Equal(t, finish.Result.OutputText, stored.OutputText)
			history := f.history(item.AccountID)
			require.Len(t, history, 1)
			require.Equal(t, wantHistory, history[0].Status)
			state, err := f.repo.GetBatch(f.ctx, batch.ID)
			require.NoError(t, err)
			require.Equal(t, service.DegradationBatchCanceled, state.Status)
			require.Equal(t, 0, state.Counts.Pending)
			require.Equal(t, 0, state.Counts.Running)
			wantCanceled := 1
			if cancelFirst {
				wantCanceled = 2
			}
			require.Equal(t, wantCanceled, state.Counts.Canceled)
			require.Empty(t, f.history(f.accountIDs[1]), "queued cancel must not create probe history")
			next, err := f.repo.ClaimNextItem(f.ctx)
			require.NoError(t, err)
			require.Nil(t, next)
			_, err = f.repo.CancelBatch(f.ctx, batch.ID, f.userID)
			require.NoError(t, err, "terminal cancel is idempotent")
			require.Equal(t, wantStatus, f.get(item).Status)
		})
	}
}

func TestDegradationCheckBatchExpiredLeaseContinuesPending(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled_%t", cancelFirst), func(t *testing.T) {
			f := newDegradationBatchFixture(t)
			batch := f.batch(f.account(), f.account())
			item := f.claim()
			progress := json.RawMessage(`{"attempt":2,"max_attempts":6,"received":1,"target":3}`)
			require.NoError(t, f.repo.UpdateProgress(f.ctx, batch.ID, item.ID, item.ClaimToken, "resolved-model", progress))
			if cancelFirst {
				_, err := f.repo.CancelBatch(f.ctx, batch.ID, f.userID)
				require.NoError(t, err)
			}
			f.expire(item)
			require.NoError(t, f.repo.InterruptExpiredItems(f.ctx))
			stored := f.get(item)
			want, reason := service.DegradationItemInterrupted, "lease_expired"
			if cancelFirst {
				want, reason = service.DegradationItemCanceled, "user_canceled"
			}
			require.Equal(t, want, stored.Status)
			require.Equal(t, reason, stored.ReasonCode)
			require.JSONEq(t, string(progress), string(stored.Progress))
			require.Equal(t, "resolved-model", stored.TestedModel)
			history := f.history(item.AccountID)
			require.Len(t, history, 1)
			require.Equal(t, service.AccountDegradationCheckStatusError, history[0].Status)
			_, err := f.repo.Heartbeat(f.ctx, batch.ID, item.ID, item.ClaimToken)
			require.ErrorIs(t, err, service.ErrDegradationBatchClaimLost)
			require.ErrorIs(t, f.repo.UpdateProgress(f.ctx, batch.ID, item.ID, item.ClaimToken, "late", json.RawMessage(`{}`)), service.ErrDegradationBatchClaimLost)
			_, err = f.repo.FinishItem(f.ctx, batch.ID, item.ID, item.ClaimToken, degradationBatchSuccess(item))
			require.ErrorIs(t, err, service.ErrDegradationBatchClaimLost)
			require.NoError(t, f.repo.InterruptExpiredItems(f.ctx))
			require.Len(t, f.history(item.AccountID), 1, "recovery and late owner cannot insert duplicate history")
			require.Equal(t, want, f.get(item).Status)
			if !cancelFirst {
				next := f.claim()
				require.Equal(t, f.accountIDs[1], next.AccountID)
				_, err := f.repo.FinishItem(f.ctx, batch.ID, next.ID, next.ClaimToken, degradationBatchSuccess(next))
				require.NoError(t, err)
				state, err := f.repo.GetBatch(f.ctx, batch.ID)
				require.NoError(t, err)
				require.Equal(t, service.DegradationBatchCompleted, state.Status)
				require.Equal(t, 1, state.Counts.Interrupted)
				require.Equal(t, 1, state.Counts.Succeeded)
			}
		})
	}
}

func TestDegradationCheckBatchAccountDeletionReplay(t *testing.T) {
	for _, deleteFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("delete_first_%t", deleteFirst), func(t *testing.T) {
			f := newDegradationBatchFixture(t)
			f.batch(f.account())
			item := f.claim()
			if deleteFirst {
				_, err := integrationDB.ExecContext(f.ctx, `DELETE FROM accounts WHERE id=$1`, item.AccountID)
				require.NoError(t, err)
			}
			finish := degradationBatchSuccess(item)
			stored, err := f.repo.FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, finish)
			require.NoError(t, err)
			require.Equal(t, service.DegradationItemSucceeded, stored.Status)
			if deleteFirst {
				require.Nil(t, stored.HistoryID)
			} else {
				require.NotNil(t, stored.HistoryID)
				_, err := integrationDB.ExecContext(f.ctx, `DELETE FROM accounts WHERE id=$1`, item.AccountID)
				require.NoError(t, err)
			}
			replay := f.get(item)
			require.Nil(t, replay.HistoryID)
			require.Equal(t, item.AccountName, replay.AccountName)
			require.Equal(t, finish.Result.OutputText, replay.OutputText)
			require.JSONEq(t, string(finish.Result.Result), string(replay.Result))
			require.Empty(t, f.history(item.AccountID))
		})
	}
}

func TestDegradationCheckBatchAccountDeletionRace(t *testing.T) {
	f := newDegradationBatchFixture(t)
	f.batch(f.account())
	item := f.claim()
	deleting, err := integrationDB.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	defer deleting.Rollback()
	var blockerPID int
	require.NoError(t, deleting.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))
	_, err = deleting.ExecContext(f.ctx, `DELETE FROM accounts WHERE id=$1`, item.AccountID)
	require.NoError(t, err)
	type finishResult struct {
		item *service.AccountDegradationCheckBatchItem
		err  error
	}
	done := make(chan finishResult, 1)
	go func() {
		stored, err := f.repo.FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, degradationBatchSuccess(item))
		done <- finishResult{stored, err}
	}()
	// Observe the actual PostgreSQL lock wait instead of using a scheduling sleep.
	for {
		var waiting bool
		err := integrationDB.QueryRowContext(f.ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, blockerPID).Scan(&waiting)
		require.NoError(t, err)
		if waiting {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("finish bypassed account deletion lock: item=%+v error=%v", result.item, result.err)
		default:
		}
	}
	require.NoError(t, deleting.Commit())
	select {
	case result := <-done:
		require.NoError(t, result.err)
		require.Equal(t, service.DegradationItemSucceeded, result.item.Status)
		require.Nil(t, result.item.HistoryID)
		require.Equal(t, "collected probe output", f.get(item).OutputText)
		require.Empty(t, f.history(item.AccountID))
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
}

func TestDegradationCheckBatchHistoryFailureRollsBackSuccess(t *testing.T) {
	f := newDegradationBatchFixture(t)
	f.batch(f.account())
	item := f.claim()
	function := fmt.Sprintf("degradation_fail_history_%d", item.AccountID)
	trigger := function + "_trigger"
	_, err := integrationDB.ExecContext(f.ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected history failure'; END; $$`, function))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON account_degradation_check_results; DROP FUNCTION IF EXISTS %s()`, trigger, function))
		require.NoError(t, err)
	})
	_, err = integrationDB.ExecContext(f.ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON account_degradation_check_results FOR EACH ROW WHEN (NEW.account_id=%d) EXECUTE FUNCTION %s()`, trigger, item.AccountID, function))
	require.NoError(t, err)
	stored, err := f.repo.FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, degradationBatchSuccess(item))
	require.Error(t, err)
	require.Nil(t, stored, "failed history transaction must never report succeeded")
	after := f.get(item)
	require.Equal(t, service.DegradationItemRunning, after.Status)
	require.Nil(t, after.HistoryID)
	require.Nil(t, after.FinishedAt)
	require.Empty(t, after.OutputText)
	require.Empty(t, f.history(item.AccountID))
	batch, err := f.repo.GetBatch(f.ctx, item.BatchID)
	require.NoError(t, err)
	require.Equal(t, service.DegradationBatchRunning, batch.Status)
	require.Equal(t, 0, batch.Counts.Succeeded)
	_, err = integrationDB.ExecContext(f.ctx, fmt.Sprintf(`DROP TRIGGER %s ON account_degradation_check_results`, trigger))
	require.NoError(t, err)
	// Recovery interrupts rather than replaying the upstream or claiming success.
	f.expire(item)
	require.NoError(t, f.repo.InterruptExpiredItems(f.ctx))
	require.Equal(t, service.DegradationItemInterrupted, f.get(item).Status)
	history := f.history(item.AccountID)
	require.Len(t, history, 1)
	require.Equal(t, service.AccountDegradationCheckStatusError, history[0].Status)
}

func TestDegradationCheckBatchConcurrentFinishSoleHistory(t *testing.T) {
	f := newDegradationBatchFixture(t)
	f.batch(f.account())
	item := f.claim()
	start := make(chan struct{})
	type finishResult struct {
		item *service.AccountDegradationCheckBatchItem
		err  error
	}
	results := make(chan finishResult, 2)
	for range 2 {
		go func() {
			<-start
			stored, err := NewAccountDegradationCheckBatchRepository(integrationDB).FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, degradationBatchSuccess(item))
			results <- finishResult{stored, err}
		}()
	}
	close(start)
	var historyID int64
	for range 2 {
		select {
		case result := <-results:
			require.NoError(t, result.err)
			require.Equal(t, service.DegradationItemSucceeded, result.item.Status)
			require.NotNil(t, result.item.HistoryID)
			if historyID == 0 {
				historyID = *result.item.HistoryID
			}
			require.Equal(t, historyID, *result.item.HistoryID)
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
	}
	history := f.history(item.AccountID)
	require.Len(t, history, 1)
	require.Equal(t, historyID, history[0].ID)
	require.Equal(t, "collected probe output", f.get(item).OutputText)
}

func TestDegradationCheckBatchSubmissionIdentitySurvivesAccountDeletion(t *testing.T) {
	f := newDegradationBatchFixture(t)
	accountID := f.account()
	batch := f.batch(accountID)
	_, err := integrationDB.ExecContext(f.ctx, `DELETE FROM accounts WHERE id=$1`, accountID)
	require.NoError(t, err)
	existing, err := f.repo.GetBatchByRequestKey(f.ctx, f.userID, batch.RequestKey)
	require.NoError(t, err)
	require.Equal(t, batch.ID, existing.ID)
	replayed, err := f.repo.CreateBatch(f.ctx, batch, nil)
	require.NoError(t, err)
	require.Equal(t, batch.ID, replayed.ID)
	conflicting := *batch
	conflicting.RequestHash = "different-normalized-payload"
	_, err = f.repo.CreateBatch(f.ctx, &conflicting, nil)
	require.ErrorIs(t, err, service.ErrDegradationBatchRequestConflict)
	items, _, err := f.repo.ListItems(f.ctx, batch.ID, pagination.PaginationParams{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, accountID, items[0].AccountID)
}

func TestDegradationCheckBatchExpiredOwnerCannotCommitBeforeRecovery(t *testing.T) {
	f := newDegradationBatchFixture(t)
	f.batch(f.account(), f.account())
	item := f.claim()
	f.expire(item)
	_, err := f.repo.Heartbeat(f.ctx, item.BatchID, item.ID, item.ClaimToken)
	require.ErrorIs(t, err, service.ErrDegradationBatchClaimLost)
	require.ErrorIs(t, f.repo.UpdateProgress(f.ctx, item.BatchID, item.ID, item.ClaimToken, "late-model", json.RawMessage(`{"received":3}`)), service.ErrDegradationBatchClaimLost)
	_, err = f.repo.FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, degradationBatchSuccess(item))
	require.ErrorIs(t, err, service.ErrDegradationBatchClaimLost)
	require.Equal(t, service.DegradationItemRunning, f.get(item).Status)
	require.Empty(t, f.history(item.AccountID))
	require.NoError(t, f.repo.InterruptExpiredItems(f.ctx))
	require.Equal(t, service.DegradationItemInterrupted, f.get(item).Status)
	require.Len(t, f.history(item.AccountID), 1)
	next := f.claim()
	require.NotEqual(t, item.ID, next.ID, "expired execution must never return to the queue")
}

func TestDegradationCheckBatchPreprobeSkipCreatesNoHistory(t *testing.T) {
	f := newDegradationBatchFixture(t)
	batch := f.batch(f.account())
	item := f.claim()
	stored, err := f.repo.FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, service.DegradationItemFinish{Status: service.DegradationItemSkipped, ReasonCode: "unsupported_account"})
	require.NoError(t, err)
	require.Equal(t, service.DegradationItemSkipped, stored.Status)
	require.Nil(t, stored.HistoryID)
	require.Empty(t, f.history(item.AccountID))
	state, err := f.repo.GetBatch(f.ctx, batch.ID)
	require.NoError(t, err)
	require.Equal(t, service.DegradationBatchCompleted, state.Status)
	require.Equal(t, 1, state.Counts.Skipped)
}

func TestDegradationCheckBatchSoftDeletedAccountRetainsResult(t *testing.T) {
	f := newDegradationBatchFixture(t)
	f.batch(f.account())
	item := f.claim()
	_, err := integrationDB.ExecContext(f.ctx, `UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, item.AccountID)
	require.NoError(t, err)
	finish := degradationBatchSuccess(item)
	stored, err := f.repo.FinishItem(f.ctx, item.BatchID, item.ID, item.ClaimToken, finish)
	require.NoError(t, err)
	require.Equal(t, service.DegradationItemSucceeded, stored.Status)
	require.Nil(t, stored.HistoryID)
	require.Empty(t, f.history(item.AccountID))
	require.JSONEq(t, string(finish.Result.Result), string(stored.Result))
	require.Equal(t, finish.Result.OutputText, stored.OutputText)
}
