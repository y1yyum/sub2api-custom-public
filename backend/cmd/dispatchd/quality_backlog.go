package main

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
)

// qualityReports reads the source only when quality.deferral asks the report
// to show the unscored backlog; otherwise it stays a dispatch-only read.
func qualityReports(ctx context.Context, store *dispatch.Store, cfg dispatch.Config) ([]dispatch.QualityReport, error) {
	if cfg.Quality == nil || cfg.Quality.Deferral == nil {
		return store.QualityReports(ctx, cfg)
	}
	db, err := openDB(ctx, "DISPATCH_QUALITY", "")
	if err != nil {
		return nil, errors.New("dispatch quality source unavailable")
	}
	defer func() { _ = db.Close() }()
	source, err := dispatch.NewQualitySource(ctx, db)
	if err != nil {
		return nil, err
	}
	return store.QualityReportsWithBacklog(ctx, cfg, source)
}
