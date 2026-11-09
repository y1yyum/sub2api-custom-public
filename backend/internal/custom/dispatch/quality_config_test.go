package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDispatchQualityConfigDefaultsAndBoundaries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"schema_version":1,"revision":"uncalibrated","templates":[]}`), 0600))
	target := qualityTestTarget()
	base := QualityConfig{BaselineFile: path, Bindings: []QualityBinding{{PlanID: 1, TargetRef: target.Ref, Kind: "quiz"}, {PlanID: 2, TargetRef: target.Ref, Kind: "quiz"}}}
	cfg, err := normalizeQualityConfig(base, []Target{target})
	require.NoError(t, err)
	require.Equal(t, 1000, cfg.MaxResults)
	require.False(t, cfg.ApplyWeight)
	require.NotNil(t, cfg.baseline)
	require.Len(t, cfg.baselineDigest, 64)
	for name, change := range map[string]func(*QualityConfig){
		"missing_baseline": func(c *QualityConfig) { c.BaselineFile = path + "-missing" },
		"directory":        func(c *QualityConfig) { c.BaselineFile = dir },
		"empty_bindings":   func(c *QualityConfig) { c.Bindings = nil },
		"duplicate_plan":   func(c *QualityConfig) { c.Bindings[1].PlanID = 1 },
		"unknown_target":   func(c *QualityConfig) { c.Bindings[0].TargetRef = "missing" },
		"unsupported_kind": func(c *QualityConfig) { c.Bindings[0].Kind = "probe" },
		"limit":            func(c *QualityConfig) { c.MaxResults = 10001 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			changed.Bindings = append([]QualityBinding(nil), base.Bindings...)
			change(&changed)
			_, err := normalizeQualityConfig(changed, []Target{target})
			require.Error(t, err)
		})
	}
	before, err := json.Marshal(cfg.version())
	require.NoError(t, err)
	copyPath := filepath.Join(dir, "identical.json")
	require.NoError(t, os.WriteFile(copyPath, cfg.baselineBody, 0600))
	base.BaselineFile = copyPath
	copyCfg, err := normalizeQualityConfig(base, []Target{target})
	require.NoError(t, err)
	after, err := json.Marshal(copyCfg.version())
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "source path aliases do not change immutable content")
	require.NoError(t, os.WriteFile(copyPath, []byte(strings.Repeat(" ", MaxSnapshotBytes+1)), 0600))
	_, err = normalizeQualityConfig(base, []Target{target})
	require.Error(t, err)
}
