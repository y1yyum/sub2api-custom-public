package relaymonitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const stationColumns = `id,name,url,type,enabled,cny_per_usd::text,balance_mode,balance_token_id,low_balance::text,no_renewal,include_in_cost,subject_id,channel_aliases,version`

type scanner interface{ Scan(...any) error }

func scanStation(row scanner) (Station, error) {
	var s Station
	var aliases []byte
	err := row.Scan(&s.ID, &s.Name, &s.URL, &s.Type, &s.Enabled, &s.CNYPerUSD, &s.BalanceMode, &s.BalanceTokenID, &s.LowBalance, &s.NoRenewal, &s.IncludeInCost, &s.SubjectID, &aliases, &s.Version)
	if err == nil {
		err = json.Unmarshal(aliases, &s.ChannelAliases)
	}
	return s, err
}

func (m *Module) station(ctx context.Context, id int64) (Station, error) {
	return scanStation(m.db.QueryRowContext(ctx, `SELECT `+stationColumns+` FROM relay_monitor_stations WHERE id=$1`, id))
}

func (m *Module) stations(ctx context.Context) ([]Station, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT `+stationColumns+` FROM relay_monitor_stations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []Station{}
	for rows.Next() {
		s, e := scanStation(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (m *Module) tokens(ctx context.Context, stationID int64) ([]Token, error) {
	s, err := m.station(ctx, stationID)
	if err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx, `SELECT id,station_id,name,account_id,enabled,cny_per_usd_override::text,credential_encrypted,version FROM relay_monitor_tokens WHERE station_id=$1 ORDER BY id`, stationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []Token{}
	for rows.Next() {
		var t Token
		if e := rows.Scan(&t.ID, &t.StationID, &t.Name, &t.AccountID, &t.Enabled, &t.CNYPerUSDOverride, &t.EncryptedCredential, &t.Version); e != nil {
			return nil, e
		}
		t.HasCredential = t.EncryptedCredential != "" || t.AccountID != nil
		applyRate(s, &t)
		result = append(result, t)
	}
	return result, rows.Err()
}

func (m *Module) readDoc(ctx context.Context, key string, out any) error {
	var raw []byte
	if err := m.db.QueryRowContext(ctx, `SELECT value FROM relay_monitor_meta WHERE key=$1`, key).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (m *Module) writeDoc(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO relay_monitor_meta(key,value) VALUES($1,$2::jsonb) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, key, string(raw))
	return err
}

func (m *Module) settings(ctx context.Context) (Settings, error) {
	tz := m.cfg.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	s := Settings{RefreshSeconds: 60, Timezone: tz, RetentionDays: 90, DailyReportTime: "09:00", LowBalance: "5", ErrorThreshold: 3, ETAHours: 72, CooldownMinutes: 1440}
	err := m.readDoc(ctx, "settings", &s)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	s.EncryptionKeyConfigured = m.cfg.Totp.EncryptionKeyConfigured
	return s, err
}

const snapshotColumns = `id,station_id,token_id,checked_at,ok,error,latency_ms,cny_per_usd::text,remaining_cny::text,today_used_cny::text,result`

func scanSnapshot(row scanner) (Snapshot, error) {
	var s Snapshot
	var raw []byte
	err := row.Scan(&s.ID, &s.StationID, &s.TokenID, &s.CheckedAt, &s.OK, &s.Error, &s.LatencyMS, &s.CNYPerUSD, &s.RemainingCNY, &s.TodayUsedCNY, &raw)
	if err == nil {
		err = json.Unmarshal(raw, &s.QueryResult)
	}
	return s, err
}

func (m *Module) history(ctx context.Context, id int64, start, end time.Time) ([]Snapshot, error) {
	// 按令牌数分配采样预算，覆盖完整窗口而非截断窗口尾部。
	bucket := int64(end.Sub(start).Seconds() / 500)
	if bucket < 60 {
		bucket = 60
	}
	rows, err := m.db.QueryContext(ctx, `SELECT `+snapshotColumns+` FROM (SELECT *,row_number() OVER(PARTITION BY token_id,floor(extract(epoch FROM checked_at)/GREATEST($4::double precision,$5::double precision*(SELECT GREATEST(count(*),1) FROM relay_monitor_tokens WHERE station_id=$1)/3500.0)) ORDER BY checked_at DESC) AS n FROM relay_monitor_snapshots WHERE station_id=$1 AND checked_at >= $2 AND checked_at < $3) AS samples WHERE n=1 ORDER BY checked_at LIMIT 4000`, id, start, end, bucket, end.Sub(start).Seconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Snapshot{}
	for rows.Next() {
		s, e := scanSnapshot(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (m *Module) latest(ctx context.Context, id int64) ([]Snapshot, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT DISTINCT ON(token_id) `+snapshotColumns+` FROM relay_monitor_snapshots WHERE station_id=$1 ORDER BY token_id,checked_at DESC`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Snapshot{}
	for rows.Next() {
		s, e := scanSnapshot(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
