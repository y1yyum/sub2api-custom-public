package dispatch

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDispatchScheduledConfigBindsDatabaseAndEpoch(t *testing.T) {
	base := Config{Version: 1, CoreInstance: "core", ConfigRevision: 1, SnapshotFile: "snapshot.json"}
	cfg, err := normalizeConfig(base)
	require.NoError(t, err)
	require.Nil(t, cfg.Scheduled)
	base.Scheduled = &ScheduledConfig{DatabaseID: uuid.NewString(), Bindings: []ScheduledBinding{{PlanID: 2, Epoch: 4}, {PlanID: 1, Epoch: 3}}}
	cfg, err = normalizeConfig(base)
	require.NoError(t, err)
	require.Equal(t, []ScheduledBinding{{PlanID: 1, Epoch: 3}, {PlanID: 2, Epoch: 4}}, cfg.Scheduled.Bindings)
	require.Equal(t, int64(2), base.Scheduled.Bindings[0].PlanID)
	for _, change := range []func(*ScheduledConfig){
		func(c *ScheduledConfig) { c.DatabaseID = "wrong-database" },
		func(c *ScheduledConfig) { c.DatabaseID = uuid.Nil.String() },
		func(c *ScheduledConfig) { c.Bindings = nil },
		func(c *ScheduledConfig) { c.Bindings = []ScheduledBinding{{PlanID: 0, Epoch: 1}} },
		func(c *ScheduledConfig) { c.Bindings = []ScheduledBinding{{PlanID: 1, Epoch: 0}} },
		func(c *ScheduledConfig) {
			c.Bindings = []ScheduledBinding{{PlanID: 1, Epoch: 1}, {PlanID: 1, Epoch: 2}}
		},
		func(c *ScheduledConfig) { c.Bindings = make([]ScheduledBinding, 1001) },
	} {
		scheduled := *base.Scheduled
		change(&scheduled)
		bad := base
		bad.Scheduled = &scheduled
		_, err := normalizeConfig(bad)
		require.Error(t, err)
	}
}
