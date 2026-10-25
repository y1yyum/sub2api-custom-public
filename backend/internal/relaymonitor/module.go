package relaymonitor

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"reflect"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
)

var errStale = errors.New("配置已变更，请重新加载")
var errBusy = errors.New("凭证正在查询，请稍后重试")

func New(db *sql.DB, cfg *config.Config, accounts service.AccountRepository, encryptor service.SecretEncryptor) *Module {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Module{db: db, cfg: cfg, accounts: accounts, encryptor: encryptor, ctx: ctx, cancel: cancel, sem: make(chan struct{}, 4), client: newOutboundClient(cfg)}
	m.wg.Add(1)
	go m.run()
	return m
}

func (m *Module) Stop() { m.cancel(); m.wg.Wait(); m.client.CloseIdleConnections() }

func (m *Module) run() {
	defer m.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var nextRefresh, nextRetry, nextCleanup time.Time
	refreshDone := make(chan struct{}, 1)
	refreshing := false
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-refreshDone:
			refreshing = false
		case now := <-tick.C:
			ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
			settings, err := m.settings(ctx)
			if err == nil && settings.Enabled {
				regular := !now.Before(nextRefresh)
				if !refreshing && (regular || !now.Before(nextRetry)) {
					refreshing = true
					if regular {
						nextRefresh = now.Add(time.Duration(settings.RefreshSeconds) * time.Second)
					}
					nextRetry = now.Add(30 * time.Second)
					m.wg.Add(1)
					go func() {
						defer m.wg.Done()
						defer func() { refreshDone <- struct{}{} }()
						ctx, cancel := context.WithTimeout(m.ctx, 5*time.Minute)
						defer cancel()
						var err error
						if regular {
							err = m.refreshAll(ctx)
						} else {
							err = m.retryFailures(ctx, settings.ErrorThreshold)
						}
						if err != nil {
							slog.Warn("relay monitor refresh failed")
						}
					}()
				}
				if err = m.scheduleReports(ctx); err != nil {
					slog.Warn("relay monitor report scheduling failed")
				}
				if !now.Before(nextCleanup) {
					nextCleanup = now.Add(time.Hour)
					_, err = m.db.ExecContext(ctx, `DELETE FROM relay_monitor_snapshots WHERE checked_at < $1`, now.AddDate(0, 0, -settings.RetentionDays))
					if err != nil {
						slog.Warn("relay monitor retention failed")
					}
				}
			}
			if err == nil {
				deliveryCtx, deliveryCancel := context.WithTimeout(m.ctx, 2*time.Minute)
				if err = m.processNotifications(deliveryCtx); err != nil {
					slog.Warn("relay monitor delivery processing failed")
				}
				deliveryCancel()
			}
			cancel()
		}
	}
}

func (m *Module) retryFailures(ctx context.Context, threshold int) error {
	rows, err := m.db.QueryContext(ctx, `SELECT DISTINCT a.station_id FROM relay_monitor_alert_state a JOIN relay_monitor_stations s ON s.id=a.station_id JOIN relay_monitor_tokens t ON t.id=a.token_id WHERE s.enabled AND t.enabled AND (a.state->>'fail_count')::int > 0 AND (a.state->>'fail_count')::int < $1 AND NOT EXISTS(SELECT 1 FROM relay_monitor_snapshots snap WHERE snap.token_id=a.token_id AND snap.checked_at>now()-interval '30 seconds')`, threshold)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		s, err := m.station(ctx, id)
		if err != nil {
			return err
		}
		if err = m.refreshStation(ctx, s); err != nil && !errors.Is(err, errBusy) {
			return err
		}
	}
	return nil
}

func credentialLockKey(t Token) int64 {
	key := "relay-monitor:token:" + strconv.FormatInt(t.ID, 10)
	if t.AccountID != nil {
		key = "relay-monitor:account:" + strconv.FormatInt(*t.AccountID, 10)
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64())
}

func (m *Module) useCredential(ctx context.Context, s Station, t Token, fn func(map[string]string) (map[string]string, error)) error {
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var acquired bool
	lockKey := credentialLockKey(t)
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, lockKey).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return errBusy
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, lockKey); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	// 锁后再校验配置；所有凭据操作共用同一 PostgreSQL 会话锁。
	var currentTokenVersion, currentStationVersion int64
	var currentCipher string
	err = conn.QueryRowContext(ctx, `SELECT t.version,s.version,t.credential_encrypted FROM relay_monitor_tokens t JOIN relay_monitor_stations s ON s.id=t.station_id WHERE t.id=$1 AND s.id=$2`, t.ID, s.ID).Scan(&currentTokenVersion, &currentStationVersion, &currentCipher)
	if err != nil {
		return err
	}
	if currentTokenVersion != t.Version || currentStationVersion != s.Version {
		return errStale
	}
	credentials := map[string]string{}
	if t.AccountID != nil {
		account, e := m.accounts.GetByID(ctx, *t.AccountID)
		if e != nil {
			return errors.New("关联账号不可用")
		}
		if e = linkedAccountOriginMatches(s.URL, account); e != nil {
			return e
		}
		for key, value := range account.Credentials {
			if text, ok := value.(string); ok {
				credentials[key] = text
			} else if value != nil {
				credentials[key] = fmt.Sprint(value)
			}
		}
	} else if currentCipher != "" {
		plain, e := m.encryptor.Decrypt(currentCipher)
		if e != nil {
			return errors.New("凭证解密失败，请检查固定加密密钥")
		}
		if err = json.Unmarshal([]byte(plain), &credentials); err != nil {
			return errors.New("凭证格式错误")
		}
	}
	original := make(map[string]string, len(credentials))
	for k, v := range credentials {
		original[k] = v
	}
	rotated, callErr := fn(credentials)
	if rotated != nil {
		credentials = rotated
	}
	// 无论上游查询是否成功，都持久化已成功轮换的凭证；关联账号由原账号服务管理。
	if t.AccountID == nil && !reflect.DeepEqual(original, credentials) {
		if !m.cfg.Totp.EncryptionKeyConfigured {
			return errors.New("保存凭证需要固定 TOTP_ENCRYPTION_KEY")
		}
		raw, e := json.Marshal(credentials)
		if e != nil {
			return e
		}
		cipher, e := m.encryptor.Encrypt(string(raw))
		if e != nil {
			return errors.New("凭证加密失败")
		}
		persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer persistCancel()
		result, e := conn.ExecContext(persistCtx, `UPDATE relay_monitor_tokens SET credential_encrypted=$1,updated_at=NOW() WHERE id=$2 AND account_id IS NULL AND credential_encrypted=$3 AND EXISTS(SELECT 1 FROM relay_monitor_stations WHERE id=$4 AND url=$5 AND type=$6)`, cipher, t.ID, currentCipher, s.ID, s.URL, s.Type)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errStale
		}
	}
	return callErr
}

func (m *Module) refreshAll(ctx context.Context) error {
	stations, err := m.stations(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	// 每批最多四个网络任务；锁同时防止手动和定时刷新重复写入。
	for start := 0; start < len(stations); start += 4 {
		end := start + 4
		if end > len(stations) {
			end = len(stations)
		}
		results := make(chan error, end-start)
		for _, s := range stations[start:end] {
			go func(s Station) {
				if !s.Enabled {
					results <- nil
					return
				}
				results <- m.refreshStation(ctx, s)
			}(s)
		}
		for range stations[start:end] {
			if e := <-results; e != nil && firstErr == nil {
				firstErr = e
			}
		}
	}
	return firstErr
}

func (m *Module) refreshStation(ctx context.Context, s Station) error {
	cfg, err := m.settings(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return errors.New("监控模块尚未启用")
	}
	tokens, err := m.tokens(ctx, s.ID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, t := range tokens {
		if !t.Enabled {
			continue
		}
		err = m.useCredential(ctx, s, t, func(credential map[string]string) (map[string]string, error) {
			started := time.Now()
			result, queryErr := m.query(ctx, s, t, credential)
			snap := Snapshot{StationID: s.ID, TokenID: t.ID, CheckedAt: time.Now().UTC(), OK: queryErr == nil, LatencyMS: time.Since(started).Milliseconds(), CNYPerUSD: t.EffectiveCNYPerUSD, QueryResult: result}
			if snap.Currency == "" {
				snap.Currency = "USD"
			}
			if snap.BalanceKind == "" {
				snap.BalanceKind = s.BalanceMode
			}
			if s.BalanceMode == "shared" {
				snap.CNYPerUSD = s.CNYPerUSD
			}
			if queryErr != nil {
				snap.Error = "上游查询失败，请检查凭证、站点协议或网络策略"
				snap.QueryResult = QueryResult{Currency: "USD", BalanceKind: s.BalanceMode}
			} else {
				snap.RemainingCNY, err = convertCNY(result.Remaining, result.Currency, snap.CNYPerUSD)
				if err != nil {
					return result.Credential, err
				}
				snap.TodayUsedCNY, err = convertCNY(result.TodayUsed, result.Currency, t.EffectiveCNYPerUSD)
				if err != nil {
					return result.Credential, err
				}
			}
			if err := m.saveSnapshot(ctx, s, t, &snap, cfg); err != nil {
				return result.Credential, err
			}
			return result.Credential, nil
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Module) saveSnapshot(ctx context.Context, s Station, t Token, snap *Snapshot, cfg Settings) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var sv, tv int64
	err = tx.QueryRowContext(ctx, `SELECT s.version,t.version FROM relay_monitor_stations s JOIN relay_monitor_tokens t ON t.station_id=s.id WHERE s.id=$1 AND t.id=$2 FOR UPDATE OF s,t`, s.ID, t.ID).Scan(&sv, &tv)
	if err != nil {
		return err
	}
	if sv != s.Version || tv != t.Version {
		return errStale
	}
	if snap.OK {
		previous, e := scanSnapshot(tx.QueryRowContext(ctx, `SELECT `+snapshotColumns+` FROM relay_monitor_snapshots WHERE token_id=$1 AND ok=TRUE ORDER BY checked_at DESC LIMIT 1`, t.ID))
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			applyHistoricalConsumption(previous, snap, cfg.Timezone, t.EffectiveCNYPerUSD)
		}
	}
	safeResult := snap.QueryResult
	safeResult.Credential = nil
	safeResult.Account = ""
	raw, err := json.Marshal(safeResult)
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO relay_monitor_snapshots(station_id,token_id,checked_at,ok,error,latency_ms,cny_per_usd,remaining_cny,today_used_cny,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb) RETURNING id`, s.ID, t.ID, snap.CheckedAt, snap.OK, snap.Error, snap.LatencyMS, snap.CNYPerUSD, snap.RemainingCNY, snap.TodayUsedCNY, string(raw)).Scan(&snap.ID)
	if err != nil {
		return err
	}
	if err = m.evaluateAlerts(ctx, tx, s, *snap, cfg); err != nil {
		return err
	}
	return tx.Commit()
}

func applyHistoricalConsumption(previous Snapshot, current *Snapshot, tz, rate string) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	if previous.CheckedAt.In(loc).Format("2006-01-02") != current.CheckedAt.In(loc).Format("2006-01-02") {
		return
	}
	var delta decimal.Decimal
	valid := false
	if current.TodayUsed != nil && previous.TodayUsed != nil {
		a, e := decimal.NewFromString(*current.TodayUsed)
		b, f := decimal.NewFromString(*previous.TodayUsed)
		if e == nil && f == nil && !a.LessThan(b) {
			delta = a.Sub(b)
			valid = true
		}
	} else if current.Remaining != nil && previous.Remaining != nil {
		a, e := decimal.NewFromString(*previous.Remaining)
		b, f := decimal.NewFromString(*current.Remaining)
		if e == nil && f == nil {
			delta = decimal.Max(decimal.Zero, a.Sub(b))
			valid = true
			current.Estimated = true
		}
	}
	if valid {
		value := delta.String()
		converted, e := convertCNY(&value, current.Currency, rate)
		if e == nil {
			base := decimal.Zero
			var e error
			if previous.TodayUsedCNY != nil {
				base, e = decimal.NewFromString(*previous.TodayUsedCNY)
			} else {
				current.Estimated = true
			}
			if e == nil {
				out := base.Add(decimal.RequireFromString(*converted)).String()
				current.TodayUsedCNY = &out
			}
		}
	} else if previous.CNYPerUSD != rate && current.TodayUsedCNY != nil {
		current.Estimated = true
	}
}
