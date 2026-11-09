//go:build integration

package repository

import (
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/custom/scheduledowner"
	"github.com/stretchr/testify/require"
)

func TestScheduledOwnerResultIDsRemainBigint(t *testing.T) {
	for _, resultID := range []int64{2147483647, 2147483648, 9223372036854775806} {
		t.Run(strconv.FormatInt(resultID, 10), func(t *testing.T) {
			db, store := ownerStoreDB(t)
			plan := ownerStorePlan(t, db, "probe")
			_, err := store.Import(t.Context(), []int64{plan})
			require.NoError(t, err)
			run, err := store.Acquire(t.Context(), plan, scheduledowner.Legacy)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, run.Close()) })
			require.NoError(t, run.Validate(t.Context()))
			ownerStoreExec(t, db, `INSERT INTO public.scheduled_test_results(id,plan_id,model_id,response_text,started_at,finished_at)
			 VALUES($1,$2,'owner-model','synthetic large result identity',clock_timestamp(),clock_timestamp())`, resultID, plan)
			ownerStoreExec(t, db, `UPDATE public.scheduled_test_plans SET last_run_at=clock_timestamp(),next_run_at=clock_timestamp()+interval '1 hour' WHERE id=$1`, plan)
			require.NoError(t, run.Finish(t.Context(), resultID, nil))
			var storedID int64
			var state string
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT result_id,state FROM scheduled_owner.runs WHERE plan_id=$1`, plan).Scan(&storedID, &state))
			require.Equal(t, resultID, storedID)
			require.Equal(t, "completed", state)
		})
	}
}
