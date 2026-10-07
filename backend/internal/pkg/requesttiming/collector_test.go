package requesttiming

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFinishAndBindingEitherOrder(t *testing.T) {
	for _, finishFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "bind_first", true: "finish_first"}[finishFirst], func(t *testing.T) {
			c := New(time.Now(), 3)
			ctx := With(context.Background(), c)
			body := c.WrapBody(io.NopCloser(strings.NewReader("abc")))
			if _, err := io.ReadAll(body); err != nil {
				t.Fatal(err)
			}
			done := Observe(ctx, "check")
			done()
			done()
			var got Snapshot
			calls := 0
			if finishFirst {
				c.Finish(200, false)
			}
			c.WhenFinished(func(s Snapshot) { got = s; calls++ })
			c.Finish(200, false)
			c.Finish(500, true)
			if calls != 1 || got.Status != 200 || got.BodyBytes != 3 || !got.BodyComplete || len(got.Spans) != 1 {
				t.Fatalf("unexpected snapshot: %+v, calls %d", got, calls)
			}
			Mark(ctx, "too_late")
			if _, ok := got.Events["too_late"]; ok {
				t.Fatal("snapshot mutated")
			}
		})
	}
}
func TestConcurrentCallbacksAndBounds(t *testing.T) {
	c := New(time.Now(), -1)
	ctx := With(context.Background(), c)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				Observe(ctx, "span")()
				Mark(ctx, "one")
				Output(ctx, true, false, "")
			}
		}()
	}
	wg.Wait()
	c.Finish(200, false)
	c.WhenFinished(func(s Snapshot) {
		if len(s.Spans) != 256 || !s.Truncated {
			t.Fatal("missing bounds")
		}
	})
}
func TestHTTPTraceKeepsOuterHooksAndReuse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	c := New(time.Now(), 0)
	client := server.Client()
	for range 2 {
		req, _ := http.NewRequestWithContext(With(context.Background(), c), "POST", server.URL, strings.NewReader("hello"))
		req, tr := StartAttempt(req, 7, 0)
		resp, err := client.Do(req)
		tr.Response(resp, err)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	c.Finish(200, false)
	c.WhenFinished(func(s Snapshot) {
		if len(s.Attempts) != 2 {
			t.Fatal("lost attempts")
		}
		for _, a := range s.Attempts {
			if !a.BodyEOF || a.ResponseBytes != 2 || a.Events["request_written"] == 0 || a.Events["first_byte"] == 0 {
				t.Fatalf("incomplete trace %+v", a)
			}
		}
		if s.Attempts[1].Reused == nil || !*s.Attempts[1].Reused {
			t.Fatal("reuse missing")
		}
	})
}

func TestHeartbeatFlushIsNotOutput(t *testing.T) {
	c := New(time.Now(), 0)
	ctx := With(context.Background(), c)
	c.Written(time.Now(), 4, nil, true)
	OutputFlushed(ctx)
	Output(ctx, true, false, "")
	c.Written(time.Now(), 4, nil, true) // even after parsing, a heartbeat is not output
	c.mu.Lock()
	_, wrong := c.data.Events["first_output_flush"]
	c.mu.Unlock()
	if wrong {
		t.Fatal("heartbeat counted as output")
	}
	OutputFlushed(ctx)
	c.Finish(200, false)
	c.WhenFinished(func(s Snapshot) {
		if _, ok := s.Events["first_output_flush"]; !ok {
			t.Fatal("output flush missing")
		}
	})
}

type cancelOnCloseFixture struct {
	started chan struct{}
	closed  chan struct{}
}

func (b *cancelOnCloseFixture) Read([]byte) (int, error) {
	close(b.started)
	<-b.closed
	return 0, context.Canceled
}
func (b *cancelOnCloseFixture) Close() error { close(b.closed); return nil }
func TestCompletedStreamCleanupDoesNotBecomeCancellation(t *testing.T) {
	for _, terminal := range []string{"completed", "failed", ""} {
		t.Run(terminal, func(t *testing.T) {
			c := New(time.Now(), 0)
			ctx := With(context.Background(), c)
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid", nil)
			req, trace := StartAttempt(req, 1, 0)
			fixture := &cancelOnCloseFixture{started: make(chan struct{}), closed: make(chan struct{})}
			resp := &http.Response{StatusCode: 200, Body: fixture, Request: req}
			trace.Response(resp, nil)
			Output(ResponseContext(ctx, resp), true, true, terminal)
			done := make(chan struct{})
			go func() { _, _ = resp.Body.Read(make([]byte, 1)); close(done) }()
			<-fixture.started
			_ = resp.Body.Close()
			<-done
			c.Finish(200, false)
			c.WhenFinished(func(s Snapshot) {
				a := s.Attempts[0]
				if terminal == "completed" {
					if a.Error != "" || !a.CleanupCanceled {
						t.Fatalf("cleanup misclassified: %+v", a)
					}
				} else if a.Error != "canceled" || a.CleanupCanceled {
					t.Fatalf("real cancellation hidden: %+v", a)
				}
			})
		})
	}
}

func TestCompletedStreamPipeCleanupDoesNotBecomeTransportError(t *testing.T) {
	for _, terminal := range []string{"completed", "failed", ""} {
		t.Run(terminal, func(t *testing.T) {
			c := New(time.Now(), 0)
			ctx := With(context.Background(), c)
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid", nil)
			req, trace := StartAttempt(req, 1, 0)
			reader, _ := io.Pipe()
			resp := &http.Response{StatusCode: 200, Body: reader, Request: req}
			trace.Response(resp, nil)
			Output(ResponseContext(ctx, resp), true, true, terminal)
			done := make(chan struct{})
			go func() { _, _ = resp.Body.Read(make([]byte, 1)); close(done) }()
			_ = resp.Body.Close()
			<-done
			c.Finish(200, false)
			c.WhenFinished(func(s Snapshot) {
				a := s.Attempts[0]
				if terminal == "completed" {
					if a.Error != "" || !a.CleanupCanceled {
						t.Fatalf("pipe cleanup misclassified: %+v", a)
					}
				} else if a.Error != "transport_error" || a.CleanupCanceled {
					t.Fatalf("pipe error hidden: %+v", a)
				}
			})
		})
	}
}

type immediateErrorBody struct{ err error }

func (b immediateErrorBody) Read([]byte) (int, error) { return 0, b.err }
func (immediateErrorBody) Close() error               { return nil }

func TestStreamCleanupErrorBeforeCloseRemainsRecorded(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "canceled", err: context.Canceled, want: "canceled"},
		{name: "transport", err: io.ErrUnexpectedEOF, want: "transport_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(time.Now(), 0)
			ctx := With(context.Background(), c)
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid", nil)
			req, trace := StartAttempt(req, 1, 0)
			resp := &http.Response{StatusCode: 200, Body: immediateErrorBody{err: tc.err}, Request: req}
			trace.Response(resp, nil)
			Output(ResponseContext(ctx, resp), true, true, "completed")
			_, _ = resp.Body.Read(make([]byte, 1))
			_ = resp.Body.Close()
			c.Finish(200, false)
			c.WhenFinished(func(s Snapshot) {
				a := s.Attempts[0]
				if a.Error != tc.want || a.CleanupCanceled {
					t.Fatalf("pre-close error misclassified: %+v", a)
				}
			})
		})
	}
}

func waitForTraceUpdate() bool {
	deadline := time.Now().Add(time.Second)
	stack := make([]byte, 64<<10)
	for time.Now().Before(deadline) {
		n := runtime.Stack(stack, true)
		if strings.Contains(string(stack[:n]), "requesttiming.(*Trace).update") {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func TestReadErrorClassificationUsesCloseStateAtReadReturn(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "canceled", err: context.Canceled, want: "canceled"},
		{name: "closed_pipe", err: io.ErrClosedPipe, want: "transport_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(time.Now(), 0)
			ctx := With(context.Background(), c)
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid", nil)
			req, trace := StartAttempt(req, 1, 0)
			resp := &http.Response{StatusCode: 200, Body: immediateErrorBody{err: tc.err}, Request: req}
			trace.Response(resp, nil)
			Output(ResponseContext(ctx, resp), true, true, "completed")
			body := resp.Body.(*responseBody)

			c.mu.Lock()
			readDone := make(chan struct{})
			go func() {
				_, _ = body.Read(make([]byte, 1))
				close(readDone)
			}()
			if !waitForTraceUpdate() {
				c.mu.Unlock()
				<-readDone
				t.Fatal("Read did not reach the collector lock")
			}

			closeDone := make(chan struct{})
			go func() {
				_ = body.Close()
				close(closeDone)
			}()
			deadline := time.Now().Add(time.Second)
			for !body.closing.Load() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			closeStarted := body.closing.Load()
			c.mu.Unlock()
			<-readDone
			<-closeDone
			if !closeStarted {
				t.Fatal("Close did not start while Read was waiting for the collector lock")
			}

			c.Finish(200, false)
			c.WhenFinished(func(s Snapshot) {
				a := s.Attempts[0]
				if a.Error != tc.want || a.CleanupCanceled {
					t.Fatalf("pre-close error misclassified after concurrent Close: %+v", a)
				}
			})
		})
	}
}
