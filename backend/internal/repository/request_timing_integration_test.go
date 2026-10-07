//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func requestTimingIntegrationRepository(t *testing.T) *usageLogRepository {
	t.Helper()
	tx := testTx(t)
	// Use the migrated timing schema and index, isolated from other fixtures.
	_, err := tx.ExecContext(context.Background(), `
		CREATE TEMP TABLE request_timing_details (LIKE public.request_timing_details INCLUDING ALL);
		CREATE TEMP TABLE usage_logs (id bigint PRIMARY KEY, request_id text, api_key_id bigint);
		INSERT INTO usage_logs VALUES (1, 'client:repeat', 8), (2, 'other', 9);
	`)
	require.NoError(t, err)
	return newUsageLogRepositoryWithSQL(nil, tx)
}

func TestRequestTimingRetentionClearsMultipleBatches(t *testing.T) {
	repo := requestTimingIntegrationRepository(t)
	ctx := context.Background()
	_, err := repo.sql.ExecContext(ctx, `
		INSERT INTO request_timing_details (usage_log_id, trace_id, created_at, detail)
		SELECT 1, md5(n::text)::uuid, NOW() - INTERVAL '31 days', '{}'::jsonb
		FROM generate_series(1, 25005) AS n;
		INSERT INTO request_timing_details VALUES
			(999, '00000000-0000-0000-0000-000000000001', NOW(), '{"kind":"orphan"}'),
			(1, '00000000-0000-0000-0000-000000000002', NOW(), '{"kind":"fresh"}'),
			(1, '00000000-0000-0000-0000-000000000003', NOW() - INTERVAL '30 days', '{"kind":"boundary"}');
	`)
	require.NoError(t, err)
	cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, repo.cleanupRequestTimings(cleanupCtx))
	rows, err := repo.sql.QueryContext(ctx, `SELECT detail->>'kind' FROM request_timing_details ORDER BY created_at`)
	require.NoError(t, err)
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var kind string
		require.NoError(t, rows.Scan(&kind))
		kinds = append(kinds, kind)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"boundary", "fresh"}, kinds, "all 25005 expired rows and the fresh orphan must be removed")
}

func TestRequestTimingRetentionBatchIsBounded(t *testing.T) {
	repo := requestTimingIntegrationRepository(t)
	ctx := context.Background()
	_, err := repo.sql.ExecContext(ctx, `
		INSERT INTO request_timing_details (usage_log_id, trace_id, created_at, detail)
		SELECT 1, md5(n::text)::uuid, NOW() - INTERVAL '31 days', '{}'::jsonb
		FROM generate_series(1, 10001) AS n
	`)
	require.NoError(t, err)
	n, err := repo.cleanupRequestTimingBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(10000), n)
	n, err = repo.cleanupRequestTimingBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

func TestRequestTimingsEnforcesRetentionBeforeCleanup(t *testing.T) {
	repo := requestTimingIntegrationRepository(t)
	ctx := context.Background()
	// NOW() is fixed in this transaction, so this tests the inclusive cutoff
	// exactly rather than approximating it with an application clock.
	_, err := repo.sql.ExecContext(ctx, `
		INSERT INTO request_timing_details VALUES
			(1, '00000000-0000-0000-0000-000000000001', NOW() - INTERVAL '30 days 1 microsecond', '{"kind":"expired"}'),
			(1, '00000000-0000-0000-0000-000000000002', NOW() - INTERVAL '30 days', '{"kind":"boundary"}'),
			(1, '00000000-0000-0000-0000-000000000003', NOW(), '{"kind":"fresh"}');
		INSERT INTO request_timing_details (usage_log_id, trace_id, created_at, detail)
		SELECT 2, ('00000000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid,
			NOW() - (n / 2) * INTERVAL '1 minute', jsonb_build_object('rank', n)
		FROM generate_series(1, 25) AS n;
	`)
	require.NoError(t, err)
	got, err := repo.RequestTimings(ctx, 1)
	require.NoError(t, err)
	var kinds []string
	for _, raw := range got {
		var detail struct {
			Kind string `json:"kind"`
		}
		require.NoError(t, json.Unmarshal(raw, &detail))
		kinds = append(kinds, detail.Kind)
	}
	require.Equal(t, []string{"fresh", "boundary"}, kinds)
	got, err = repo.RequestTimings(ctx, 2)
	require.NoError(t, err)
	var ranks []int
	for _, raw := range got {
		var detail struct {
			Rank int `json:"rank"`
		}
		require.NoError(t, json.Unmarshal(raw, &detail))
		ranks = append(ranks, detail.Rank)
	}
	require.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, ranks)
}
