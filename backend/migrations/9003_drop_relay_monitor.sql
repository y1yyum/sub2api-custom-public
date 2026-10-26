-- Removes the relay-monitor module schema created by 9001 and 9002.
-- Data in these tables is permanently deleted. Indexes, constraints and
-- bigserial sequences are owned by their tables and drop with them.
DROP TABLE IF EXISTS relay_monitor_outbox CASCADE;
DROP TABLE IF EXISTS relay_monitor_alert_state CASCADE;
DROP TABLE IF EXISTS relay_monitor_events CASCADE;
DROP TABLE IF EXISTS relay_monitor_notification_channels CASCADE;
DROP TABLE IF EXISTS relay_monitor_subjects CASCADE;
DROP TABLE IF EXISTS relay_monitor_purchases CASCADE;
DROP TABLE IF EXISTS relay_monitor_meta CASCADE;
DROP TABLE IF EXISTS relay_monitor_rate_changes CASCADE;
DROP TABLE IF EXISTS relay_monitor_snapshots CASCADE;
DROP TABLE IF EXISTS relay_monitor_tokens CASCADE;
DROP TABLE IF EXISTS relay_monitor_stations CASCADE;
