//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestOpsRepositoryListRecentRequestsByAccounts(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("recent-requests-%s@example.com", uuid.NewString())})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-recent-" + uuid.NewString(), Name: "recent"})
	accountOne := mustCreateAccount(t, client, &service.Account{Name: "recent-one-" + uuid.NewString()})
	accountTwo := mustCreateAccount(t, client, &service.Account{Name: "recent-two-" + uuid.NewString()})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = ANY($1)", pq.Array([]int64{accountOne.ID, accountTwo.ID}))
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
	})

	usageRepo := newUsageLogRepositoryWithSQL(client, integrationDB)
	opsRepo := NewOpsRepository(integrationDB).(*opsRepository)
	now := time.Now().UTC().Truncate(time.Microsecond)
	startTime := now.Add(-15 * time.Minute)
	endTime := now.Add(time.Minute)
	duration := 1250
	firstToken := 240
	accountRate := 0.5
	accountStatsCost := 3.0

	insertUsage := func(accountID int64, requestID string, createdAt time.Time, actualCost float64) {
		t.Helper()
		_, err := usageRepo.Create(ctx, &service.UsageLog{
			UserID: user.ID, APIKeyID: apiKey.ID, AccountID: accountID,
			RequestID: requestID, Model: "gpt-recent", InputTokens: 12, OutputTokens: 7,
			TotalCost: 2, ActualCost: actualCost, AccountStatsCost: &accountStatsCost,
			AccountRateMultiplier: &accountRate, DurationMs: &duration, FirstTokenMs: &firstToken,
			CreatedAt: createdAt,
		})
		require.NoError(t, err)
	}
	insertError := func(accountID int64, requestID string, status int, createdAt time.Time) {
		t.Helper()
		duration64 := int64(900)
		firstToken64 := int64(110)
		_, err := opsRepo.InsertErrorLog(ctx, &service.OpsInsertErrorLogInput{
			RequestID: requestID, UserID: &user.ID, APIKeyID: &apiKey.ID, AccountID: &accountID,
			Model: "gpt-error", ErrorPhase: "upstream", ErrorType: "upstream_error", Severity: "error",
			StatusCode: status, ErrorMessage: fmt.Sprintf("status %d", status), ResponseLatencyMs: &duration64,
			TimeToFirstTokenMs: &firstToken64, CreatedAt: createdAt,
		})
		require.NoError(t, err)
	}

	insertUsage(accountOne.ID, "expired", startTime.Add(-time.Microsecond), 1)
	insertUsage(accountOne.ID, "oldest-retained-candidate", now.Add(-5*time.Minute), 1)
	insertUsage(accountOne.ID, "zero-cost", now.Add(-4*time.Minute), 0)
	insertError(accountOne.ID, "error-401", 401, now.Add(-3*time.Minute))
	insertUsage(accountOne.ID, "tie-success", now.Add(-2*time.Minute), 1)
	insertError(accountOne.ID, "tie-error", 429, now.Add(-2*time.Minute))
	insertUsage(accountOne.ID, "newest", now.Add(-time.Minute), 1)
	insertUsage(accountTwo.ID, "other-account", now.Add(-30*time.Second), 1)

	items, err := opsRepo.ListRecentRequestsByAccounts(
		ctx,
		[]int64{accountOne.ID, accountTwo.ID},
		startTime,
		endTime,
		5,
	)
	require.NoError(t, err)
	require.Len(t, items, 6)

	var accountOneItems, accountTwoItems []*service.OpsRequestDetail
	for _, item := range items {
		require.NotNil(t, item.AccountID)
		switch *item.AccountID {
		case accountOne.ID:
			accountOneItems = append(accountOneItems, item)
		case accountTwo.ID:
			accountTwoItems = append(accountTwoItems, item)
		}
	}
	require.Len(t, accountOneItems, 5)
	require.Len(t, accountTwoItems, 1)
	require.Equal(t, []string{"newest", "tie-error", "tie-success", "error-401", "zero-cost"}, []string{
		accountOneItems[0].RequestID,
		accountOneItems[1].RequestID,
		accountOneItems[2].RequestID,
		accountOneItems[3].RequestID,
		accountOneItems[4].RequestID,
	})
	require.Equal(t, "other-account", accountTwoItems[0].RequestID)

	zeroCost := accountOneItems[4]
	require.NotNil(t, zeroCost.ActualCost)
	require.Zero(t, *zeroCost.ActualCost)
	require.NotNil(t, zeroCost.AccountCost)
	require.InDelta(t, 1.5, *zeroCost.AccountCost, 1e-9)
	require.NotNil(t, zeroCost.AccountRateMultiplier)
	require.InDelta(t, 0.5, *zeroCost.AccountRateMultiplier, 1e-9)
	require.Equal(t, 12, *zeroCost.InputTokens)
	require.Equal(t, 7, *zeroCost.OutputTokens)

	errorItem := accountOneItems[1]
	require.Equal(t, service.OpsRequestKindError, errorItem.Kind)
	require.Equal(t, 429, *errorItem.StatusCode)
	require.Equal(t, 900, *errorItem.DurationMs)
	require.Equal(t, "status 429", errorItem.Message)
	require.Nil(t, errorItem.InputTokens)
	require.Nil(t, errorItem.OutputTokens)
	require.Nil(t, errorItem.ActualCost)
	require.Nil(t, errorItem.AccountCost)
	require.Nil(t, errorItem.AccountRateMultiplier)
}
