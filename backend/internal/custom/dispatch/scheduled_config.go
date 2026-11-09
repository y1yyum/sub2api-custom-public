package dispatch

import (
	"errors"
	"sort"

	"github.com/google/uuid"
)

type ScheduledBinding struct {
	PlanID int64 `json:"plan_id"`
	Epoch  int64 `json:"owner_epoch"`
}

type ScheduledConfig struct {
	DatabaseID string             `json:"database_id"`
	Bindings   []ScheduledBinding `json:"bindings"`
}

func normalizeScheduledConfig(cfg ScheduledConfig) (ScheduledConfig, error) {
	id, err := uuid.Parse(cfg.DatabaseID)
	if err != nil || id == uuid.Nil || id.String() != cfg.DatabaseID || len(cfg.Bindings) < 1 || len(cfg.Bindings) > 1000 {
		return ScheduledConfig{}, errors.New("invalid dispatch scheduled source configuration")
	}
	bindings := append([]ScheduledBinding(nil), cfg.Bindings...)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].PlanID < bindings[j].PlanID })
	for i, binding := range bindings {
		if binding.PlanID < 1 || binding.Epoch < 1 || (i > 0 && bindings[i-1].PlanID == binding.PlanID) {
			return ScheduledConfig{}, errors.New("invalid dispatch scheduled owner binding")
		}
	}
	cfg.Bindings = bindings
	return cfg, nil
}
