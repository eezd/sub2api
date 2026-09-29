package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type accountDegradationCheckRepository struct {
	db *sql.DB
}

func NewAccountDegradationCheckRepository(db *sql.DB) service.AccountDegradationCheckRepository {
	return &accountDegradationCheckRepository{db: db}
}

func (r *accountDegradationCheckRepository) Create(ctx context.Context, result *service.AccountDegradationCheckResult) (*service.AccountDegradationCheckResult, error) {
	resultJSON := result.Result
	if len(resultJSON) == 0 {
		resultJSON = json.RawMessage(`{}`)
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO account_degradation_check_results (
			account_id, check_type, requested_model, tested_model, status,
			result, output_text, error_message, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, NOW())
		RETURNING id, account_id, check_type, requested_model, tested_model, status,
			result, output_text, error_message, created_at
	`, result.AccountID, result.CheckType, result.RequestedModel, result.TestedModel,
		result.Status, string(resultJSON), result.OutputText, result.ErrorMessage)
	return scanAccountDegradationCheckResult(row)
}

func (r *accountDegradationCheckRepository) ListByAccountID(
	ctx context.Context,
	accountID int64,
	checkType service.AccountDegradationCheckType,
	limit int,
) ([]*service.AccountDegradationCheckResult, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, check_type, requested_model, tested_model, status,
			result, output_text, error_message, created_at
		FROM account_degradation_check_results
		WHERE account_id = $1 AND check_type = $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, accountID, checkType, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	results := make([]*service.AccountDegradationCheckResult, 0, limit)
	for rows.Next() {
		result, scanErr := scanAccountDegradationCheckResult(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func scanAccountDegradationCheckResult(row scannable) (*service.AccountDegradationCheckResult, error) {
	result := &service.AccountDegradationCheckResult{}
	var resultJSON []byte
	if err := row.Scan(
		&result.ID,
		&result.AccountID,
		&result.CheckType,
		&result.RequestedModel,
		&result.TestedModel,
		&result.Status,
		&resultJSON,
		&result.OutputText,
		&result.ErrorMessage,
		&result.CreatedAt,
	); err != nil {
		return nil, err
	}
	result.Result = append(json.RawMessage(nil), resultJSON...)
	return result, nil
}
