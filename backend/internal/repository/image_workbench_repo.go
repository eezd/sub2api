package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type imageWorkbenchRepository struct{ db *sql.DB }

func NewImageWorkbenchRepository(db *sql.DB) service.ImageWorkbenchRepository {
	return &imageWorkbenchRepository{db: db}
}

const workbenchJobColumns = `id,api_key_id,input,status,deleting,created_at,updated_at`
const workbenchAssetColumns = `id,filename,mime_type,size,width,height,draft`

func scanWorkbenchJob(row interface{ Scan(...any) error }) (service.WorkbenchJob, error) {
	j := service.WorkbenchJob{Items: []service.WorkbenchItem{}, Assets: []service.WorkbenchAsset{}}
	var raw []byte
	err := row.Scan(&j.ID, &j.APIKeyID, &raw, &j.Status, &j.Deleting, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return j, service.ErrWorkbenchNotFound
	}
	if err != nil {
		return j, err
	}
	err = json.Unmarshal(raw, &j.Input)
	return j, err
}
func scanWorkbenchAsset(row interface{ Scan(...any) error }) (service.WorkbenchAsset, error) {
	var a service.WorkbenchAsset
	err := row.Scan(&a.ID, &a.Filename, &a.MimeType, &a.Size, &a.Width, &a.Height, &a.Draft)
	if errors.Is(err, sql.ErrNoRows) {
		err = service.ErrWorkbenchNotFound
	}
	return a, err
}
func workbenchUserLock(ctx context.Context, tx *sql.Tx, uid int64) error {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, uid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrWorkbenchNotFound
	}
	return err
}
func workbenchUsage(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, uid int64) (service.WorkbenchUsage, error) {
	var u service.WorkbenchUsage
	err := q.QueryRowContext(ctx, `SELECT COALESCE((SELECT SUM(size) FROM image_workbench_assets WHERE user_id=$1),0),COALESCE((SELECT SUM(reserved_bytes) FROM image_workbench_jobs WHERE user_id=$1),0)`, uid).Scan(&u.UsedBytes, &u.ReservedBytes)
	return u, err
}
func (r *imageWorkbenchRepository) Usage(ctx context.Context, uid int64) (service.WorkbenchUsage, error) {
	return workbenchUsage(ctx, r.db, uid)
}

func (r *imageWorkbenchRepository) List(ctx context.Context, uid int64, page int) ([]service.WorkbenchJob, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+workbenchJobColumns+` FROM image_workbench_jobs WHERE user_id=$1 ORDER BY created_at DESC,id LIMIT 30 OFFSET $2`, uid, (page-1)*30)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := []service.WorkbenchJob{}
	for rows.Next() {
		j, err := scanWorkbenchJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
func (r *imageWorkbenchRepository) Get(ctx context.Context, uid int64, id string) (service.WorkbenchJob, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return service.WorkbenchJob{}, err
	}
	defer func() { _ = tx.Rollback() }()
	j, err := scanWorkbenchJob(tx.QueryRowContext(ctx, `SELECT `+workbenchJobColumns+` FROM image_workbench_jobs WHERE id=$1 AND user_id=$2`, id, uid))
	if err != nil {
		return j, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT position,status,asset_id,error,duration_ms FROM image_workbench_items WHERE job_id=$1 ORDER BY position`, id)
	if err != nil {
		return j, err
	}
	for rows.Next() {
		var item service.WorkbenchItem
		if err = rows.Scan(&item.Position, &item.Status, &item.AssetID, &item.Error, &item.DurationMS); err != nil {
			_ = rows.Close()
			return j, err
		}
		j.Items = append(j.Items, item)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return j, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT `+workbenchAssetColumns+` FROM image_workbench_assets WHERE user_id=$1 AND id IN (SELECT asset_id FROM image_workbench_items WHERE job_id=$2 UNION SELECT asset_id FROM image_workbench_references WHERE job_id=$2) ORDER BY created_at,id`, uid, id)
	if err != nil {
		return j, err
	}
	for rows.Next() {
		a, err := scanWorkbenchAsset(rows)
		if err != nil {
			_ = rows.Close()
			return j, err
		}
		j.Assets = append(j.Assets, a)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (r *imageWorkbenchRepository) Drafts(ctx context.Context, uid int64) ([]service.WorkbenchAsset, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+workbenchAssetColumns+` FROM image_workbench_assets WHERE user_id=$1 AND draft ORDER BY created_at,id`, uid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	assets := []service.WorkbenchAsset{}
	for rows.Next() {
		a, err := scanWorkbenchAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, a)
	}
	return assets, rows.Err()
}
func (r *imageWorkbenchRepository) Asset(ctx context.Context, uid int64, id string) (service.WorkbenchAsset, error) {
	return scanWorkbenchAsset(r.db.QueryRowContext(ctx, `SELECT `+workbenchAssetColumns+` FROM image_workbench_assets WHERE id=$1 AND user_id=$2`, id, uid))
}

func insertWorkbenchAsset(ctx context.Context, tx *sql.Tx, uid int64, a service.WorkbenchAsset) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO image_workbench_assets(id,user_id,filename,mime_type,size,width,height,draft) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, a.ID, uid, a.Filename, a.MimeType, a.Size, a.Width, a.Height, a.Draft)
	return err
}
func (r *imageWorkbenchRepository) AddAsset(ctx context.Context, uid int64, a service.WorkbenchAsset, write func() error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = workbenchUserLock(ctx, tx, uid); err != nil {
		return err
	}
	u, err := workbenchUsage(ctx, tx, uid)
	if err != nil {
		return err
	}
	if u.UsedBytes+u.ReservedBytes+a.Size > service.WorkbenchStorageLimit {
		return service.ErrWorkbenchQuota
	}
	if err = insertWorkbenchAsset(ctx, tx, uid, a); err != nil {
		return err
	}
	if err = write(); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *imageWorkbenchRepository) DeleteAsset(ctx context.Context, uid int64, id string, remove func(service.WorkbenchAsset) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = workbenchUserLock(ctx, tx, uid); err != nil {
		return err
	}
	a, err := scanWorkbenchAsset(tx.QueryRowContext(ctx, `SELECT `+workbenchAssetColumns+` FROM image_workbench_assets WHERE id=$1 AND user_id=$2`, id, uid))
	if err != nil {
		return err
	}
	if !a.Draft {
		return service.ErrWorkbenchBusy
	}
	if err = remove(a); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_workbench_assets WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageWorkbenchRepository) Create(ctx context.Context, uid int64, in service.WorkbenchInput) (service.WorkbenchJob, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.WorkbenchJob{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = workbenchUserLock(ctx, tx, uid); err != nil {
		return service.WorkbenchJob{}, false, err
	}
	previous, err := scanWorkbenchJob(tx.QueryRowContext(ctx, `SELECT `+workbenchJobColumns+` FROM image_workbench_jobs WHERE user_id=$1 AND request_id=$2`, uid, in.RequestID))
	if err == nil {
		a, marshalErr := json.Marshal(previous.Input)
		if marshalErr != nil {
			return previous, false, marshalErr
		}
		b, marshalErr := json.Marshal(in)
		if marshalErr != nil {
			return previous, false, marshalErr
		}
		if string(a) != string(b) {
			return previous, false, service.ErrWorkbenchConflict
		}
		return previous, true, tx.Commit()
	}
	if !errors.Is(err, service.ErrWorkbenchNotFound) {
		return previous, false, err
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM image_workbench_jobs WHERE user_id=$1 AND status='running')`, uid).Scan(&busy); err != nil {
		return previous, false, err
	}
	if busy {
		return previous, false, service.ErrWorkbenchBusy
	}
	var owned bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_keys WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL)`, in.APIKeyID, uid).Scan(&owned); err != nil {
		return previous, false, err
	}
	if !owned {
		return previous, false, service.ErrWorkbenchNotFound
	}
	u, err := workbenchUsage(ctx, tx, uid)
	if err != nil {
		return previous, false, err
	}
	reserve := int64(in.Count) * service.WorkbenchFileLimit
	if u.UsedBytes+u.ReservedBytes+reserve > service.WorkbenchStorageLimit {
		return previous, false, service.ErrWorkbenchQuota
	}
	id := uuid.NewString()
	raw, err := json.Marshal(in)
	if err != nil {
		return previous, false, err
	}
	j, err := scanWorkbenchJob(tx.QueryRowContext(ctx, `INSERT INTO image_workbench_jobs(id,user_id,request_id,api_key_id,input,status,reserved_bytes) VALUES($1,$2,$3,$4,$5,'running',$6) RETURNING `+workbenchJobColumns, id, uid, in.RequestID, in.APIKeyID, raw, reserve))
	if err != nil {
		return j, false, err
	}
	for i := 0; i < in.Count; i++ {
		if _, err = tx.ExecContext(ctx, `INSERT INTO image_workbench_items(job_id,position,status) VALUES($1,$2,'pending')`, id, i); err != nil {
			return j, false, err
		}
		j.Items = append(j.Items, service.WorkbenchItem{Position: i, Status: "pending"})
	}
	for i, aid := range in.ReferenceIDs {
		a, err := scanWorkbenchAsset(tx.QueryRowContext(ctx, `SELECT `+workbenchAssetColumns+` FROM image_workbench_assets WHERE id=$1 AND user_id=$2`, aid, uid))
		if err != nil {
			return j, false, err
		}
		var deleting bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM image_workbench_jobs j WHERE j.deleting AND j.id IN (SELECT job_id FROM image_workbench_items WHERE asset_id=$1 UNION SELECT job_id FROM image_workbench_references WHERE asset_id=$1))`, aid).Scan(&deleting); err != nil {
			return j, false, err
		}
		if deleting {
			return j, false, service.ErrWorkbenchBusy
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO image_workbench_references(job_id,asset_id,position) VALUES($1,$2,$3)`, id, aid, i); err != nil {
			return j, false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE image_workbench_assets SET draft=FALSE WHERE id=$1`, aid); err != nil {
			return j, false, err
		}
		a.Draft = false
		j.Assets = append(j.Assets, a)
	}
	return j, false, tx.Commit()
}

func (r *imageWorkbenchRepository) SaveItem(ctx context.Context, uid int64, id string, item service.WorkbenchItem, asset *service.WorkbenchAsset, write func() error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = workbenchUserLock(ctx, tx, uid); err != nil {
		return err
	}
	var status string
	var reserved int64
	if err = tx.QueryRowContext(ctx, `SELECT status,reserved_bytes FROM image_workbench_jobs WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, uid).Scan(&status, &reserved); err != nil {
		return err
	}
	if status != "running" || reserved < service.WorkbenchFileLimit {
		return service.ErrWorkbenchBusy
	}
	var pending string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM image_workbench_items WHERE job_id=$1 AND position=$2`, id, item.Position).Scan(&pending); err != nil {
		return err
	}
	if pending != "pending" {
		return service.ErrWorkbenchConflict
	}
	if asset != nil {
		if err = insertWorkbenchAsset(ctx, tx, uid, *asset); err != nil {
			return err
		}
		if err = write(); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_workbench_items SET status=$3,asset_id=$4,error=$5,duration_ms=$6 WHERE job_id=$1 AND position=$2`, id, item.Position, item.Status, item.AssetID, item.Error, item.DurationMS); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_workbench_jobs SET reserved_bytes=reserved_bytes-$2,updated_at=NOW() WHERE id=$1`, id, service.WorkbenchFileLimit); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageWorkbenchRepository) Finish(ctx context.Context, uid int64, id, status string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = workbenchUserLock(ctx, tx, uid); err != nil {
		return err
	}
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM image_workbench_jobs WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, uid).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return service.ErrWorkbenchNotFound
	} else if err != nil {
		return err
	}
	if current != "running" {
		return nil
	}
	pendingStatus := "failed"
	if status == "cancelled" || status == "interrupted" {
		pendingStatus = status
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_workbench_items SET status=$2,error=CASE WHEN error='' THEN '生成未完成，请查看任务状态；不会自动重复调用模型' ELSE error END WHERE job_id=$1 AND status='pending'`, id, pendingStatus); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_workbench_jobs SET status=$3,reserved_bytes=0,updated_at=NOW() WHERE id=$1 AND user_id=$2 AND status='running'`, id, uid, status); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageWorkbenchRepository) Interrupt(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE image_workbench_items SET status='interrupted',error='服务重启中断了生成；未自动重试，上游可能已经计费' WHERE status='pending' AND job_id IN (SELECT id FROM image_workbench_jobs WHERE status='running')`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_workbench_jobs SET status='interrupted',reserved_bytes=0,updated_at=NOW() WHERE status='running'`); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageWorkbenchRepository) Delete(ctx context.Context, uid int64, id string, remove func(service.WorkbenchAsset) error) error {
	// 先持久化删除标记；文件清理失败时保留记录及配额，允许明确重试。
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = workbenchUserLock(ctx, tx, uid); err != nil {
		_ = tx.Rollback()
		return err
	}
	j, err := scanWorkbenchJob(tx.QueryRowContext(ctx, `SELECT `+workbenchJobColumns+` FROM image_workbench_jobs WHERE id=$1 AND user_id=$2`, id, uid))
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if j.Status == "running" {
		_ = tx.Rollback()
		return service.ErrWorkbenchBusy
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_workbench_jobs SET deleting=TRUE WHERE id=$1`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	tx, err = r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = workbenchUserLock(ctx, tx, uid); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+workbenchAssetColumns+` FROM image_workbench_assets a WHERE a.user_id=$1 AND NOT a.draft AND a.id IN (SELECT asset_id FROM image_workbench_items WHERE job_id=$2 UNION SELECT asset_id FROM image_workbench_references WHERE job_id=$2) AND NOT EXISTS(SELECT 1 FROM image_workbench_references r WHERE r.asset_id=a.id AND r.job_id<>$2) AND NOT EXISTS(SELECT 1 FROM image_workbench_items i WHERE i.asset_id=a.id AND i.job_id<>$2)`, uid, id)
	if err != nil {
		return err
	}
	assets := []service.WorkbenchAsset{}
	for rows.Next() {
		a, err := scanWorkbenchAsset(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		assets = append(assets, a)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, a := range assets {
		if err = remove(a); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_workbench_jobs WHERE id=$1 AND user_id=$2`, id, uid); err != nil {
		return err
	}
	for _, a := range assets {
		if _, err = tx.ExecContext(ctx, `DELETE FROM image_workbench_assets WHERE id=$1`, a.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
