package service

import (
	"context"
	"time"
)

func refreshContextErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Deadline delivery can lag behind the clock while its timer is scheduled.
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}
