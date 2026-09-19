package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpsServiceListRecentRequestsByAccountsGroupsOneBatchInInputOrder(t *testing.T) {
	var (
		gotIDs   []int64
		gotStart time.Time
		gotEnd   time.Time
		gotLimit int
		calls    int
	)
	accountOne := int64(11)
	accountThree := int64(33)
	repo := &opsRepoMock{
		ListRecentRequestsByAccountsFn: func(_ context.Context, accountIDs []int64, startTime, endTime time.Time, limit int) ([]*OpsRequestDetail, error) {
			calls++
			gotIDs = append([]int64(nil), accountIDs...)
			gotStart = startTime
			gotEnd = endTime
			gotLimit = limit
			return []*OpsRequestDetail{
				{Kind: OpsRequestKindSuccess, AccountID: &accountOne, RequestID: "one"},
				nil,
				{Kind: OpsRequestKindError, AccountID: &accountThree, RequestID: "three"},
				{Kind: OpsRequestKindSuccess, AccountID: nil, RequestID: "missing-account"},
			}, nil
		},
	}
	service := &OpsService{opsRepo: repo}

	result, err := service.ListRecentRequestsByAccounts(context.Background(), []int64{11, 22, 11, 33})

	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, []int64{11, 22, 33}, gotIDs)
	require.Equal(t, 15*time.Minute, gotEnd.Sub(gotStart))
	require.Equal(t, 5, gotLimit)
	require.Equal(t, gotStart, result.StartTime)
	require.Equal(t, gotEnd, result.EndTime)
	require.Equal(t, 5, result.LimitPerAccount)
	require.Len(t, result.Items, 3)
	require.Equal(t, int64(11), result.Items[0].AccountID)
	require.Equal(t, "one", result.Items[0].Requests[0].RequestID)
	require.Equal(t, int64(22), result.Items[1].AccountID)
	require.NotNil(t, result.Items[1].Requests)
	require.Empty(t, result.Items[1].Requests)
	require.Equal(t, int64(33), result.Items[2].AccountID)
	require.Equal(t, "three", result.Items[2].Requests[0].RequestID)
}

func TestOpsServiceListRecentRequestsByAccountsReturnsRepositoryError(t *testing.T) {
	expected := errors.New("query failed")
	repo := &opsRepoMock{
		ListRecentRequestsByAccountsFn: func(context.Context, []int64, time.Time, time.Time, int) ([]*OpsRequestDetail, error) {
			return nil, expected
		},
	}
	service := &OpsService{opsRepo: repo}

	result, err := service.ListRecentRequestsByAccounts(context.Background(), []int64{1})

	require.Nil(t, result)
	require.ErrorIs(t, err, expected)
}

func TestOpsServiceListRecentRequestsByAccountsHandlesNilRepository(t *testing.T) {
	service := &OpsService{}

	result, err := service.ListRecentRequestsByAccounts(context.Background(), []int64{7, 8})

	require.NoError(t, err)
	require.Len(t, result.Items, 2)
	require.Empty(t, result.Items[0].Requests)
	require.Empty(t, result.Items[1].Requests)
	require.Equal(t, 15*time.Minute, result.EndTime.Sub(result.StartTime))
}

func TestOpsServiceListRecentRequestsByAccountsSkipsRepositoryForEmptyInput(t *testing.T) {
	calls := 0
	repo := &opsRepoMock{
		ListRecentRequestsByAccountsFn: func(context.Context, []int64, time.Time, time.Time, int) ([]*OpsRequestDetail, error) {
			calls++
			return nil, nil
		},
	}
	service := &OpsService{opsRepo: repo}

	result, err := service.ListRecentRequestsByAccounts(context.Background(), nil)

	require.NoError(t, err)
	require.Zero(t, calls)
	require.NotNil(t, result.Items)
	require.Empty(t, result.Items)
}
