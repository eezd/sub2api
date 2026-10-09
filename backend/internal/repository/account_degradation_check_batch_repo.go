package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type accountDegradationCheckBatchRepository struct{ db *sql.DB }

func NewAccountDegradationCheckBatchRepository(db *sql.DB) service.AccountDegradationCheckBatchRepository {
	return &accountDegradationCheckBatchRepository{db: db}
}

const degradationBatchColumns = `b.id,b.check_type,b.status,b.created_by,b.canceled_by,b.cancel_requested_at,b.started_at,b.finished_at,b.created_at,b.updated_at,b.request_key,b.request_hash`
const degradationCounts = `COUNT(i.id),COUNT(i.id) FILTER (WHERE i.status='pending'),COUNT(i.id) FILTER (WHERE i.status='running'),COUNT(i.id) FILTER (WHERE i.status='succeeded'),COUNT(i.id) FILTER (WHERE i.status='failed'),COUNT(i.id) FILTER (WHERE i.status='skipped'),COUNT(i.id) FILTER (WHERE i.status='canceled'),COUNT(i.id) FILTER (WHERE i.status='interrupted')`
const degradationItemColumns = `i.id,i.batch_id,i.position,i.account_id,i.account_name,i.platform,i.account_type,i.requested_model,i.tested_model,i.status,i.reason_code,i.error_message,i.progress,i.history_id,i.started_at,i.finished_at,i.updated_at,b.check_type`

// Summaries select only the three diagnostic fields, never the output/result payload.
const degradationSummaryColumn = `CASE WHEN b.check_type='model_trace' AND i.result <> '{}'::jsonb THEN jsonb_build_object('matches_expected',i.result->'matches_expected','prediction_name',i.result->'prediction_name','probability',i.result->'probability') ELSE NULL END`

func scanDegradationBatch(row scannable) (*service.AccountDegradationCheckBatch, error) {
	b := &service.AccountDegradationCheckBatch{}
	err := row.Scan(&b.ID, &b.CheckType, &b.Status, &b.CreatedBy, &b.CanceledBy, &b.CancelRequestedAt, &b.StartedAt, &b.FinishedAt, &b.CreatedAt, &b.UpdatedAt, &b.RequestKey, &b.RequestHash, &b.Counts.Total, &b.Counts.Pending, &b.Counts.Running, &b.Counts.Succeeded, &b.Counts.Failed, &b.Counts.Skipped, &b.Counts.Canceled, &b.Counts.Interrupted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDegradationBatchNotFound
	}
	return b, err
}

func scanDegradationItem(row scannable, detail bool) (*service.AccountDegradationCheckBatchItem, error) {
	i := &service.AccountDegradationCheckBatchItem{}
	var progress, summary, result []byte
	args := []any{&i.ID, &i.BatchID, &i.Position, &i.AccountID, &i.AccountName, &i.Platform, &i.AccountType, &i.RequestedModel, &i.TestedModel, &i.Status, &i.ReasonCode, &i.ErrorMessage, &progress, &i.HistoryID, &i.StartedAt, &i.FinishedAt, &i.UpdatedAt, &i.CheckType, &summary}
	if detail {
		args = append(args, &result, &i.OutputText, &i.ClaimToken, &i.LeaseExpiresAt)
	}
	if err := row.Scan(args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrDegradationBatchNotFound
		}
		return nil, err
	}
	i.Progress = json.RawMessage(progress)
	i.Result = json.RawMessage(result)
	if len(summary) > 0 {
		i.ModelTraceSummary = &service.DegradationModelTraceSummary{}
		if err := json.Unmarshal(summary, i.ModelTraceSummary); err != nil {
			return nil, err
		}
	}
	return i, nil
}

func getDegradationBatch(ctx context.Context, db degradationHistoryInserter, id int64) (*service.AccountDegradationCheckBatch, error) {
	return scanDegradationBatch(db.QueryRowContext(ctx, `SELECT `+degradationBatchColumns+`,`+degradationCounts+` FROM account_degradation_check_batches b LEFT JOIN account_degradation_check_batch_items i ON i.batch_id=b.id WHERE b.id=$1 GROUP BY b.id`, id))
}
func getDegradationItem(ctx context.Context, db degradationHistoryInserter, batchID, itemID int64) (*service.AccountDegradationCheckBatchItem, error) {
	return scanDegradationItem(db.QueryRowContext(ctx, `SELECT `+degradationItemColumns+`,`+degradationSummaryColumn+`,i.result,i.output_text,i.claim_token,i.lease_expires_at FROM account_degradation_check_batch_items i JOIN account_degradation_check_batches b ON b.id=i.batch_id WHERE i.batch_id=$1 AND i.id=$2`, batchID, itemID), true)
}
func (r *accountDegradationCheckBatchRepository) GetBatch(ctx context.Context, id int64) (*service.AccountDegradationCheckBatch, error) {
	return getDegradationBatch(ctx, r.db, id)
}

func (r *accountDegradationCheckBatchRepository) GetBatchByRequestKey(ctx context.Context, operatorID int64, requestKey string) (*service.AccountDegradationCheckBatch, error) {
	return scanDegradationBatch(r.db.QueryRowContext(ctx, `SELECT `+degradationBatchColumns+`,`+degradationCounts+` FROM account_degradation_check_batches b LEFT JOIN account_degradation_check_batch_items i ON i.batch_id=b.id WHERE b.created_by=$1 AND b.request_key=$2 GROUP BY b.id`, operatorID, requestKey))
}
func (r *accountDegradationCheckBatchRepository) GetItem(ctx context.Context, batchID, itemID int64) (*service.AccountDegradationCheckBatchItem, error) {
	return getDegradationItem(ctx, r.db, batchID, itemID)
}

func (r *accountDegradationCheckBatchRepository) CreateBatch(ctx context.Context, b *service.AccountDegradationCheckBatch, items []*service.AccountDegradationCheckBatchItem) (*service.AccountDegradationCheckBatch, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO account_degradation_check_batches(check_type,status,created_by,request_key,request_hash) VALUES($1,'pending',$2,$3,$4) ON CONFLICT(created_by,request_key) DO NOTHING RETURNING id`, b.CheckType, b.CreatedBy, b.RequestKey, b.RequestHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var hash string
		err = tx.QueryRowContext(ctx, `SELECT id,request_hash FROM account_degradation_check_batches WHERE created_by=$1 AND request_key=$2 FOR UPDATE`, b.CreatedBy, b.RequestKey).Scan(&id, &hash)
		if err != nil {
			return nil, err
		}
		if hash != b.RequestHash {
			return nil, service.ErrDegradationBatchRequestConflict
		}
	} else if err != nil {
		return nil, err
	} else {
		for _, i := range items {
			if i.Status != service.DegradationItemPending && i.Status != service.DegradationItemSkipped {
				return nil, fmt.Errorf("invalid initial degradation item status %q", i.Status)
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO account_degradation_check_batch_items(batch_id,position,account_id,account_name,platform,account_type,requested_model,status,reason_code,error_message,finished_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::varchar,$9,$10,CASE WHEN $8::varchar='skipped' THEN NOW() ELSE NULL END)`, id, i.Position, i.AccountID, i.AccountName, i.Platform, i.AccountType, i.RequestedModel, i.Status, i.ReasonCode, i.ErrorMessage)
			if err != nil {
				return nil, err
			}
		}
		if err = refreshDegradationBatch(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	out, err := getDegradationBatch(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *accountDegradationCheckBatchRepository) ListBatches(ctx context.Context, p pagination.PaginationParams, f service.DegradationBatchFilter) ([]*service.AccountDegradationCheckBatch, *pagination.PaginationResult, error) {
	where := ` WHERE ($1='' OR b.check_type=$1) AND ($2='' OR b.status=$2)`
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_degradation_check_batches b`+where, f.CheckType, f.Status).Scan(&total); err != nil {
		return nil, nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+degradationBatchColumns+`,`+degradationCounts+` FROM account_degradation_check_batches b LEFT JOIN account_degradation_check_batch_items i ON i.batch_id=b.id`+where+` GROUP BY b.id ORDER BY b.created_at DESC,b.id DESC LIMIT $3 OFFSET $4`, f.CheckType, f.Status, p.Limit(), p.Offset())
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*service.AccountDegradationCheckBatch, 0)
	for rows.Next() {
		b, e := scanDegradationBatch(rows)
		if e != nil {
			return nil, nil, e
		}
		out = append(out, b)
	}
	return out, paginationResultFromTotal(total, p), rows.Err()
}
func (r *accountDegradationCheckBatchRepository) ListItems(ctx context.Context, batchID int64, p pagination.PaginationParams) ([]*service.AccountDegradationCheckBatchItem, *pagination.PaginationResult, error) {
	var total int64
	err := r.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM account_degradation_check_batch_items WHERE batch_id=b.id) FROM account_degradation_check_batches b WHERE b.id=$1`, batchID).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, service.ErrDegradationBatchNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+degradationItemColumns+`,`+degradationSummaryColumn+` FROM account_degradation_check_batch_items i JOIN account_degradation_check_batches b ON b.id=i.batch_id WHERE b.id=$1 ORDER BY i.position LIMIT $2 OFFSET $3`, batchID, p.Limit(), p.Offset())
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*service.AccountDegradationCheckBatchItem, 0)
	for rows.Next() {
		i, e := scanDegradationItem(rows, false)
		if e != nil {
			return nil, nil, e
		}
		out = append(out, i)
	}
	return out, paginationResultFromTotal(total, p), rows.Err()
}

// Every caller owns the parent lock before changing a child or aggregating its state.
func lockDegradationBatch(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	var canceled bool
	err := tx.QueryRowContext(ctx, `SELECT cancel_requested_at IS NOT NULL FROM account_degradation_check_batches WHERE id=$1 FOR UPDATE`, id).Scan(&canceled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, service.ErrDegradationBatchNotFound
	}
	return canceled, err
}
func refreshDegradationBatch(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE account_degradation_check_batches b SET status=CASE WHEN cancel_requested_at IS NOT NULL THEN CASE WHEN EXISTS(SELECT 1 FROM account_degradation_check_batch_items WHERE batch_id=b.id AND status='running') THEN 'canceling' ELSE 'canceled' END WHEN NOT EXISTS(SELECT 1 FROM account_degradation_check_batch_items WHERE batch_id=b.id AND status IN ('pending','running')) THEN 'completed' WHEN started_at IS NOT NULL THEN 'running' ELSE 'pending' END,finished_at=CASE WHEN NOT EXISTS(SELECT 1 FROM account_degradation_check_batch_items WHERE batch_id=b.id AND status IN ('pending','running')) THEN COALESCE(finished_at,NOW()) ELSE NULL END,updated_at=NOW() WHERE id=$1`, id)
	return err
}
func lockDegradationClaimAdmission(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('sub2api:degradation-check-claim',0))`)
	return err
}

func (r *accountDegradationCheckBatchRepository) ClaimNextItem(ctx context.Context) (*service.AccountDegradationCheckBatchItem, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockDegradationClaimAdmission(ctx, tx); err != nil {
		return nil, err
	}
	var running int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_degradation_check_batch_items WHERE status='running' AND lease_expires_at>clock_timestamp()`).Scan(&running); err != nil {
		return nil, err
	}
	if running >= 2 {
		return nil, nil
	}
	var batchID int64
	err = tx.QueryRowContext(ctx, `SELECT b.id FROM account_degradation_check_batches b WHERE b.cancel_requested_at IS NULL AND b.status IN ('pending','running') AND EXISTS(SELECT 1 FROM account_degradation_check_batch_items i WHERE i.batch_id=b.id AND i.status='pending' AND NOT EXISTS(SELECT 1 FROM account_degradation_check_batch_items active WHERE active.account_id=i.account_id AND active.status='running' AND active.lease_expires_at>clock_timestamp())) ORDER BY b.id LIMIT 1 FOR UPDATE OF b SKIP LOCKED`).Scan(&batchID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var itemID int64
	err = tx.QueryRowContext(ctx, `SELECT i.id FROM account_degradation_check_batch_items i WHERE i.batch_id=$1 AND i.status='pending' AND NOT EXISTS(SELECT 1 FROM account_degradation_check_batch_items active WHERE active.account_id=i.account_id AND active.status='running' AND active.lease_expires_at>clock_timestamp()) ORDER BY i.position LIMIT 1 FOR UPDATE OF i`, batchID).Scan(&itemID)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_degradation_check_batch_items SET status='running',claim_token=$2,lease_expires_at=clock_timestamp()+interval '30 seconds',started_at=NOW(),updated_at=NOW() WHERE id=$1`, itemID, uuid.NewString())
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_degradation_check_batches SET status='running',started_at=COALESCE(started_at,NOW()),updated_at=NOW() WHERE id=$1`, batchID)
	if err != nil {
		return nil, err
	}
	item, err := getDegradationItem(ctx, tx, batchID, itemID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}

func lockOwnedDegradationItem(ctx context.Context, tx *sql.Tx, batchID, itemID int64, token string) (*service.AccountDegradationCheckBatchItem, bool, error) {
	if _, err := tx.ExecContext(ctx, `SELECT id FROM account_degradation_check_batch_items WHERE batch_id=$1 AND id=$2 FOR UPDATE`, batchID, itemID); err != nil {
		return nil, false, err
	}
	i, err := getDegradationItem(ctx, tx, batchID, itemID)
	if err != nil {
		return nil, false, err
	}
	if token == "" || i.ClaimToken != token {
		return nil, false, service.ErrDegradationBatchClaimLost
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT status='running' AND lease_expires_at>clock_timestamp() FROM account_degradation_check_batch_items WHERE id=$1`, itemID).Scan(&valid)
	return i, valid, err
}
func (r *accountDegradationCheckBatchRepository) Heartbeat(ctx context.Context, batchID, itemID int64, token string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Lock the parent first. Claim admission skips locked parents, so a slow
	// mutation in this batch cannot make this heartbeat monopolize the
	// cluster-wide admission lock and expire unrelated claims.
	canceled, err := lockDegradationBatch(ctx, tx, batchID)
	if err != nil {
		return false, err
	}
	// Admission and renewal still serialize across the old lease boundary.
	if err = lockDegradationClaimAdmission(ctx, tx); err != nil {
		return false, err
	}
	_, valid, err := lockOwnedDegradationItem(ctx, tx, batchID, itemID, token)
	if err != nil {
		return false, err
	}
	if !valid {
		return false, service.ErrDegradationBatchClaimLost
	}
	res, err := tx.ExecContext(ctx, `UPDATE account_degradation_check_batch_items SET lease_expires_at=clock_timestamp()+interval '30 seconds',updated_at=NOW() WHERE id=$1 AND claim_token=$2 AND status='running' AND lease_expires_at>clock_timestamp()`, itemID, token)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, service.ErrDegradationBatchClaimLost
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return canceled, nil
}
func (r *accountDegradationCheckBatchRepository) UpdateProgress(ctx context.Context, batchID, itemID int64, token, testedModel string, progress json.RawMessage) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	canceled, err := lockDegradationBatch(ctx, tx, batchID)
	if err != nil {
		return err
	}
	_, valid, err := lockOwnedDegradationItem(ctx, tx, batchID, itemID, token)
	if err != nil {
		return err
	}
	if !valid || canceled {
		return service.ErrDegradationBatchClaimLost
	}
	if len(progress) == 0 {
		progress = json.RawMessage(`{}`)
	}
	res, err := tx.ExecContext(ctx, `UPDATE account_degradation_check_batch_items SET tested_model=CASE WHEN $2='' THEN tested_model ELSE $2 END,progress=$3::jsonb,updated_at=NOW() WHERE id=$1 AND claim_token=$4 AND status='running' AND lease_expires_at>clock_timestamp()`, itemID, testedModel, string(progress), token)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrDegradationBatchClaimLost
	}
	return tx.Commit()
}

// The terminal CAS precedes history insertion. Any insertion/commit failure rolls
// the whole transaction back, leaving the claim for lease recovery, never success.
func normalizeDegradationItemFinish(f service.DegradationItemFinish, canceled bool) service.DegradationItemFinish {
	if canceled {
		f.Status = service.DegradationItemCanceled
		f.ReasonCode = "user_canceled"
		f.ErrorMessage = "检测已取消"
	}
	return f
}

func lockDegradationHistoryAccount(ctx context.Context, tx *sql.Tx, accountID int64) (bool, error) {
	// FOR UPDATE blocks both soft deletion (an UPDATE) and hard deletion.
	// Missing/deleted accounts deliberately retain only the batch snapshot.
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func finishDegradationItem(ctx context.Context, tx *sql.Tx, i *service.AccountDegradationCheckBatchItem, f service.DegradationItemFinish, expired, accountExists bool) error {
	switch f.Status {
	case service.DegradationItemSucceeded, service.DegradationItemFailed, service.DegradationItemCanceled, service.DegradationItemInterrupted, service.DegradationItemSkipped:
	default:
		return fmt.Errorf("invalid terminal degradation item status %q", f.Status)
	}
	result := service.AccountDegradationCheckResult{AccountID: i.AccountID, CheckType: i.CheckType, RequestedModel: i.RequestedModel, TestedModel: i.TestedModel, Result: json.RawMessage(`{}`)}
	if f.Result != nil {
		result.TestedModel = f.Result.TestedModel
		result.Result = f.Result.Result
		result.OutputText = f.Result.OutputText
	}
	if len(result.Result) == 0 {
		result.Result = json.RawMessage(`{}`)
	}
	if f.Status == service.DegradationItemSucceeded {
		result.Status = "success"
	} else {
		result.Status = "error"
	}
	result.ErrorMessage = f.ErrorMessage
	if f.Status == service.DegradationItemSucceeded {
		f.ReasonCode = ""
		f.ErrorMessage = ""
		result.ErrorMessage = ""
	}
	// Normal completions reach this point after potentially blocking account
	// locks and while the caller holds claim admission through commit. Recovery
	// uses the expired-lease branch and cannot commit a successful stale result.
	res, err := tx.ExecContext(ctx, `UPDATE account_degradation_check_batch_items SET status=$4,reason_code=$5,error_message=$6,tested_model=$7,result=$8::jsonb,output_text=$9,finished_at=NOW(),updated_at=NOW() WHERE batch_id=$1 AND id=$2 AND claim_token=$3 AND status='running' AND (($10 AND lease_expires_at<=clock_timestamp()) OR (NOT $10 AND lease_expires_at>clock_timestamp()))`, i.BatchID, i.ID, i.ClaimToken, f.Status, f.ReasonCode, f.ErrorMessage, result.TestedModel, string(result.Result), result.OutputText, expired)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrDegradationBatchClaimLost
	}
	if f.Status != service.DegradationItemSkipped && accountExists {
		history, e := insertAccountDegradationCheckResult(ctx, tx, &result)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `UPDATE account_degradation_check_batch_items SET history_id=$2 WHERE id=$1`, i.ID, history.ID); err != nil {
			return err
		}
	}
	return refreshDegradationBatch(ctx, tx, i.BatchID)
}

func (r *accountDegradationCheckBatchRepository) FinishItem(ctx context.Context, batchID, itemID int64, token string, f service.DegradationItemFinish) (*service.AccountDegradationCheckBatchItem, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	canceled, err := lockDegradationBatch(ctx, tx, batchID)
	if err != nil {
		return nil, err
	}
	i, valid, err := lockOwnedDegradationItem(ctx, tx, batchID, itemID, token)
	if err != nil {
		return nil, err
	}
	if i.Status != service.DegradationItemRunning {
		// Same-owner duplicate completion is read-only. Recovery revokes the
		// token, so an expired owner cannot enter this path.
		return i, nil
	}
	if !valid {
		return nil, service.ErrDegradationBatchClaimLost
	}
	f = normalizeDegradationItemFinish(f, canceled)
	accountExists := false
	if f.Status != service.DegradationItemSkipped {
		accountExists, err = lockDegradationHistoryAccount(ctx, tx, i.AccountID)
		if err != nil {
			return nil, err
		}
	}
	// Acquire the global fence only after potentially blocking account locks.
	// ClaimNextItem skips our locked parent and therefore cannot deadlock here.
	if err = lockDegradationClaimAdmission(ctx, tx); err != nil {
		return nil, err
	}
	if err = finishDegradationItem(ctx, tx, i, f, false, accountExists); err != nil {
		return nil, err
	}
	out, err := getDegradationItem(ctx, tx, batchID, itemID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *accountDegradationCheckBatchRepository) CancelBatch(ctx context.Context, batchID, operatorID int64) (*service.AccountDegradationCheckBatch, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = lockDegradationBatch(ctx, tx, batchID); err != nil {
		return nil, err
	}
	b, err := getDegradationBatch(ctx, tx, batchID)
	if err != nil {
		return nil, err
	}
	if b.Status == service.DegradationBatchCompleted || b.Status == service.DegradationBatchCanceled {
		return b, nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_degradation_check_batches SET cancel_requested_at=COALESCE(cancel_requested_at,NOW()),canceled_by=COALESCE(canceled_by,$2),updated_at=NOW() WHERE id=$1`, batchID, operatorID)
	if err != nil {
		return nil, err
	}
	// The UPDATE obtains child locks only after the parent is locked.
	_, err = tx.ExecContext(ctx, `UPDATE account_degradation_check_batch_items SET status='canceled',reason_code='user_canceled',error_message='检测已取消',finished_at=NOW(),updated_at=NOW() WHERE batch_id=$1 AND status='pending'`, batchID)
	if err != nil {
		return nil, err
	}
	if err = refreshDegradationBatch(ctx, tx, batchID); err != nil {
		return nil, err
	}
	out, err := getDegradationBatch(ctx, tx, batchID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *accountDegradationCheckBatchRepository) InterruptExpiredItems(ctx context.Context) error {
	// One expired item per short transaction: history failure cannot roll back
	// previously recovered items or cause any upstream execution to be retried.
	for {
		recovered, err := r.interruptOneExpiredItem(ctx)
		if err != nil {
			return err
		}
		if !recovered {
			return nil
		}
	}
}
func (r *accountDegradationCheckBatchRepository) interruptOneExpiredItem(ctx context.Context) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var batchID int64
	err = tx.QueryRowContext(ctx, `SELECT b.id FROM account_degradation_check_batches b WHERE EXISTS(SELECT 1 FROM account_degradation_check_batch_items i WHERE i.batch_id=b.id AND i.status='running' AND i.lease_expires_at<=clock_timestamp()) ORDER BY b.id LIMIT 1 FOR UPDATE OF b SKIP LOCKED`).Scan(&batchID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var canceled bool
	if err = tx.QueryRowContext(ctx, `SELECT cancel_requested_at IS NOT NULL FROM account_degradation_check_batches WHERE id=$1`, batchID).Scan(&canceled); err != nil {
		return false, err
	}
	var itemID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM account_degradation_check_batch_items WHERE batch_id=$1 AND status='running' AND lease_expires_at<=clock_timestamp() ORDER BY position LIMIT 1 FOR UPDATE`, batchID).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	i, err := getDegradationItem(ctx, tx, batchID, itemID)
	if err != nil {
		return false, err
	}
	f := normalizeDegradationItemFinish(service.DegradationItemFinish{Status: service.DegradationItemInterrupted, ReasonCode: "lease_expired", ErrorMessage: "执行中断，未自动重试", Result: &service.AccountDegradationCheckResult{TestedModel: i.TestedModel, Result: i.Result, OutputText: i.OutputText}}, canceled)
	accountExists := false
	if f.Status != service.DegradationItemSkipped {
		accountExists, err = lockDegradationHistoryAccount(ctx, tx, i.AccountID)
		if err != nil {
			return false, err
		}
	}
	if err = finishDegradationItem(ctx, tx, i, f, true, accountExists); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_degradation_check_batch_items SET claim_token='' WHERE id=$1`, i.ID); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
