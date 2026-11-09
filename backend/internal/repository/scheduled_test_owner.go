package repository

import "github.com/Wei-Shaw/sub2api/internal/custom/scheduledowner"

func (r *scheduledTestPlanRepository) ScheduledOwnerStore() *scheduledowner.Store {
	return scheduledowner.NewStore(r.db)
}

func (r *scheduledTestPlanRepository) ScheduledOwnerMaxWorkers() int {
	maxOpen := r.db.Stats().MaxOpenConnections
	if maxOpen == 0 {
		return 10
	}
	return min(10, maxOpen-1)
}
