package service

import (
	"context"
	"time"
)

const (
	opsAccountRecentRequestWindow = 15 * time.Minute
	opsAccountRecentRequestLimit  = 5
)

type OpsAccountRecentRequestGroup struct {
	AccountID int64               `json:"account_id"`
	Requests  []*OpsRequestDetail `json:"requests"`
}

type OpsAccountRecentRequests struct {
	StartTime       time.Time                       `json:"start_time"`
	EndTime         time.Time                       `json:"end_time"`
	LimitPerAccount int                             `json:"limit_per_account"`
	Items           []*OpsAccountRecentRequestGroup `json:"items"`
}

func (s *OpsService) ListRecentRequestsByAccounts(ctx context.Context, accountIDs []int64) (*OpsAccountRecentRequests, error) {
	endTime := time.Now().UTC()
	startTime := endTime.Add(-opsAccountRecentRequestWindow)

	uniqueIDs := make([]int64, 0, len(accountIDs))
	groupsByID := make(map[int64]*OpsAccountRecentRequestGroup, len(accountIDs))
	items := make([]*OpsAccountRecentRequestGroup, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		if _, exists := groupsByID[accountID]; exists {
			continue
		}
		group := &OpsAccountRecentRequestGroup{
			AccountID: accountID,
			Requests:  []*OpsRequestDetail{},
		}
		uniqueIDs = append(uniqueIDs, accountID)
		groupsByID[accountID] = group
		items = append(items, group)
	}

	result := &OpsAccountRecentRequests{
		StartTime:       startTime,
		EndTime:         endTime,
		LimitPerAccount: opsAccountRecentRequestLimit,
		Items:           items,
	}
	if len(uniqueIDs) == 0 || s == nil || s.opsRepo == nil {
		return result, nil
	}

	requests, err := s.opsRepo.ListRecentRequestsByAccounts(ctx, uniqueIDs, startTime, endTime, opsAccountRecentRequestLimit)
	if err != nil {
		return nil, err
	}
	for _, request := range requests {
		if request == nil || request.AccountID == nil {
			continue
		}
		group, requested := groupsByID[*request.AccountID]
		if !requested {
			continue
		}
		group.Requests = append(group.Requests, request)
	}

	return result, nil
}
