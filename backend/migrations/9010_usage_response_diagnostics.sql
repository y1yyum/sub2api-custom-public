ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS response_diagnostics JSONB;
