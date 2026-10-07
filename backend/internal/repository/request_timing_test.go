package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
)

func TestRequestTimingSQLErrors(t *testing.T) {
	want := errors.New("diagnostic database failure")
	for _, operation := range []string{"write", "read", "cleanup", "rows affected"} {
		t.Run(operation, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			repo := newUsageLogRepositoryWithSQL(nil, db)
			switch operation {
			case "write":
				mock.ExpectExec(".*").WillReturnError(want)
				err = repo.writeTiming(context.Background(), timingWrite{})
			case "read":
				mock.ExpectQuery(".*").WillReturnError(want)
				_, err = repo.RequestTimings(context.Background(), 9)
			case "cleanup":
				mock.ExpectExec(".*").WillReturnError(want)
				err = repo.cleanupRequestTimings(context.Background())
			case "rows affected":
				mock.ExpectExec(".*").WillReturnResult(sqlmock.NewErrorResult(want))
				err = repo.cleanupRequestTimings(context.Background())
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRequestTimingCleanupStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&usageLogRepository{}).cleanupRequestTimings(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context cancellation", err)
	}
}
func TestTimingBindingDoesNotWriteUntilRequestFinished(t *testing.T) {
	// A manually supplied queue exercises lifecycle without leaking a worker.
	repo := &usageLogRepository{timingQueue: make(chan timingWrite, 1)}
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	repo.db = db
	repo.timingOnce.Do(func() {})
	c := requesttiming.New(time.Now(), 0)
	ctx := requesttiming.With(context.Background(), c)
	repo.RecordRequestTiming(ctx, "client:r", 3)
	select {
	case <-repo.timingQueue:
		t.Fatal("premature snapshot")
	default:
	}
	c.Finish(200, false)
	select {
	case job := <-repo.timingQueue:
		raw, err := json.Marshal(job.data)
		if err != nil || len(raw) == 0 || job.data.Status != 200 {
			t.Fatal("invalid final snapshot")
		}
	default:
		t.Fatal("missing final snapshot")
	}
}
