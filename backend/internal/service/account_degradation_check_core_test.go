//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// A failed progress sink must stop before spending more upstream quota. The
// core returns a record but leaves history ownership to its caller.
func TestDegradationCheckCoreStopsWhenStartCannotBeDelivered(t *testing.T) {
	for _, checkType := range []AccountDegradationCheckType{AccountDegradationCheckModelTrace, AccountDegradationCheckSVGAnimation} {
		t.Run(string(checkType), func(t *testing.T) {
			upstream := &queuedHTTPUpstream{}
			svc, history := newDegradationCheckTestService(newOpenAIDegradationTestAccount(501), upstream)
			sinkErr := errors.New("progress store unavailable")
			record, err := svc.RunDegradationCheck(context.Background(), 501, checkType, "gpt-5.4", func(TestEvent) error { return sinkErr })
			require.ErrorIs(t, err, sinkErr)
			require.NotNil(t, record)
			require.Equal(t, AccountDegradationCheckStatusError, record.Status)
			require.Empty(t, upstream.requests)
			require.Empty(t, history.results)
		})
	}
}
