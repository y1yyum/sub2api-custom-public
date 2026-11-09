package custombridge

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/001_receipt_outbox.sql
var receiptSchema string

// ApplyReceiptSchema 是显式实验迁移入口，不由主程序启动或 Core 迁移调用。
func ApplyReceiptSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, receiptSchema); err != nil {
		return fmt.Errorf("apply bridge receipt schema: %w", err)
	}
	return tx.Commit()
}
