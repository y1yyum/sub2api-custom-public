package service

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// The deadline has elapsed while cancellation delivery is still pending.
type pendingRefreshDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c pendingRefreshDeadlineContext) Deadline() (time.Time, bool) {
	return c.deadline, true
}

func TestRefreshContextErr(t *testing.T) {
	canceled, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("refresh cycle stopped")
	cancel(cause)
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer stop()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "no deadline", ctx: context.Background()},
		{name: "future deadline", ctx: pendingRefreshDeadlineContext{context.Background(), time.Now().Add(time.Hour)}},
		{name: "past deadline pending delivery", ctx: pendingRefreshDeadlineContext{context.Background(), time.Now().Add(-time.Hour)}, want: context.DeadlineExceeded},
		{name: "zero deadline", ctx: pendingRefreshDeadlineContext{context.Background(), time.Time{}}, want: context.DeadlineExceeded},
		{name: "existing deadline error", ctx: expired, want: context.DeadlineExceeded},
		{name: "cancellation precedes clock expiry", ctx: pendingRefreshDeadlineContext{canceled, time.Now().Add(-time.Hour)}, want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := refreshContextErr(tc.ctx); got != tc.want {
				t.Fatalf("refreshContextErr() = %v, want %v (Err = %v)", got, tc.want, tc.ctx.Err())
			}
		})
	}
	if context.Cause(canceled) != cause {
		t.Fatal("the caller's cancellation cause changed")
	}
}

func TestRefreshContextErrAtExactDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := pendingRefreshDeadlineContext{context.Background(), time.Now().Add(10 * time.Millisecond)}
		if err := refreshContextErr(ctx); err != nil {
			t.Fatalf("before deadline: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatal("fixture must leave cancellation delivery pending")
		}
		if err := refreshContextErr(ctx); err != context.DeadlineExceeded {
			t.Fatalf("at deadline: %v, want DeadlineExceeded", err)
		}
	})
}
