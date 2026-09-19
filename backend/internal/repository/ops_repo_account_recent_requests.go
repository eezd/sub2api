package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *opsRepository) ListRecentRequestsByAccounts(
	ctx context.Context,
	accountIDs []int64,
	startTime, endTime time.Time,
	limitPerAccount int,
) ([]*service.OpsRequestDetail, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if len(accountIDs) == 0 || limitPerAccount <= 0 {
		return []*service.OpsRequestDetail{}, nil
	}

	const query = `
WITH combined AS (
  SELECT
    'success'::TEXT AS kind,
    ul.created_at,
    ul.id AS source_id,
    ul.request_id,
    ul.model,
    ul.duration_ms,
    ul.first_token_ms,
    NULL::INT AS status_code,
    NULL::BIGINT AS error_id,
    NULL::TEXT AS phase,
    NULL::TEXT AS severity,
    NULL::TEXT AS message,
    ul.user_id,
    ul.api_key_id,
    ul.account_id,
    ul.group_id,
    ul.stream,
    ul.input_tokens,
    ul.output_tokens,
    ul.actual_cost,
    COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1) AS account_cost,
    COALESCE(ul.account_rate_multiplier, 1) AS account_rate_multiplier
  FROM usage_logs ul
  WHERE ul.account_id = ANY($1)
    AND ul.created_at >= $2
    AND ul.created_at < $3

  UNION ALL

  SELECT
    'error'::TEXT AS kind,
    o.created_at,
    o.id AS source_id,
    COALESCE(NULLIF(o.request_id, ''), NULLIF(o.client_request_id, ''), '') AS request_id,
    o.model,
    COALESCE(o.duration_ms, o.response_latency_ms) AS duration_ms,
    o.time_to_first_token_ms AS first_token_ms,
    o.status_code,
    o.id AS error_id,
    o.error_phase AS phase,
    o.severity,
    o.error_message AS message,
    o.user_id,
    o.api_key_id,
    o.account_id,
    o.group_id,
    o.stream,
    NULL::BIGINT AS input_tokens,
    NULL::BIGINT AS output_tokens,
    NULL::NUMERIC AS actual_cost,
    NULL::NUMERIC AS account_cost,
    NULL::NUMERIC AS account_rate_multiplier
  FROM ops_error_logs o
  WHERE o.account_id = ANY($1)
    AND o.created_at >= $2
    AND o.created_at < $3
    AND COALESCE(o.status_code, 0) >= 400
), ranked AS (
  SELECT
    combined.*,
    ROW_NUMBER() OVER (
      PARTITION BY account_id
      ORDER BY created_at DESC, kind ASC, source_id DESC
    ) AS row_num
  FROM combined
)
SELECT
  kind,
  created_at,
  request_id,
  model,
  duration_ms,
  first_token_ms,
  status_code,
  error_id,
  phase,
  severity,
  message,
  user_id,
  api_key_id,
  account_id,
  group_id,
  stream,
  input_tokens,
  output_tokens,
  actual_cost,
  account_cost,
  account_rate_multiplier
FROM ranked
WHERE row_num <= $4
ORDER BY account_id ASC, created_at DESC, kind ASC, source_id DESC`

	rows, err := r.db.QueryContext(
		ctx,
		query,
		pq.Array(accountIDs),
		startTime.UTC(),
		endTime.UTC(),
		limitPerAccount,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	toIntPtr := func(value sql.NullInt64) *int {
		if !value.Valid {
			return nil
		}
		converted := int(value.Int64)
		return &converted
	}
	toInt64Ptr := func(value sql.NullInt64) *int64 {
		if !value.Valid {
			return nil
		}
		converted := value.Int64
		return &converted
	}
	toFloat64Ptr := func(value sql.NullFloat64) *float64 {
		if !value.Valid {
			return nil
		}
		converted := value.Float64
		return &converted
	}

	out := make([]*service.OpsRequestDetail, 0, len(accountIDs)*limitPerAccount)
	for rows.Next() {
		var (
			kind      string
			createdAt time.Time
			requestID sql.NullString
			model     sql.NullString

			durationMs   sql.NullInt64
			firstTokenMs sql.NullInt64
			statusCode   sql.NullInt64
			errorID      sql.NullInt64
			phase        sql.NullString
			severity     sql.NullString
			message      sql.NullString
			userID       sql.NullInt64
			apiKeyID     sql.NullInt64
			accountID    sql.NullInt64
			groupID      sql.NullInt64
			stream       bool

			inputTokens           sql.NullInt64
			outputTokens          sql.NullInt64
			actualCost            sql.NullFloat64
			accountCost           sql.NullFloat64
			accountRateMultiplier sql.NullFloat64
		)

		if err := rows.Scan(
			&kind,
			&createdAt,
			&requestID,
			&model,
			&durationMs,
			&firstTokenMs,
			&statusCode,
			&errorID,
			&phase,
			&severity,
			&message,
			&userID,
			&apiKeyID,
			&accountID,
			&groupID,
			&stream,
			&inputTokens,
			&outputTokens,
			&actualCost,
			&accountCost,
			&accountRateMultiplier,
		); err != nil {
			return nil, err
		}

		out = append(out, &service.OpsRequestDetail{
			Kind:                  service.OpsRequestKind(kind),
			CreatedAt:             createdAt,
			RequestID:             strings.TrimSpace(requestID.String),
			Model:                 strings.TrimSpace(model.String),
			DurationMs:            toIntPtr(durationMs),
			FirstTokenMs:          toIntPtr(firstTokenMs),
			StatusCode:            toIntPtr(statusCode),
			InputTokens:           toIntPtr(inputTokens),
			OutputTokens:          toIntPtr(outputTokens),
			ActualCost:            toFloat64Ptr(actualCost),
			AccountCost:           toFloat64Ptr(accountCost),
			AccountRateMultiplier: toFloat64Ptr(accountRateMultiplier),
			ErrorID:               toInt64Ptr(errorID),
			Phase:                 phase.String,
			Severity:              severity.String,
			Message:               message.String,
			UserID:                toInt64Ptr(userID),
			APIKeyID:              toInt64Ptr(apiKeyID),
			AccountID:             toInt64Ptr(accountID),
			GroupID:               toInt64Ptr(groupID),
			Stream:                stream,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}
