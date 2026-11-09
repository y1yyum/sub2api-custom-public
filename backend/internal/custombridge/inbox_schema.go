package custombridge

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed migrations/003_shadow_inbox.sql
var inboxSchema string

// ApplyInboxSchema 只在独立目标库创建影子事实，不修改 Core 客户余额。
func ApplyInboxSchema(ctx context.Context, db *sql.DB) error {
	if err := checkShadowDatabase(ctx, db); err != nil {
		return err
	}
	return applyRelayMigration(ctx, db, "bridge_shadow", "inbox_schema_migrations", "003", inboxSchema)
}
