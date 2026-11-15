package upstreamsites

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Encryptor interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}
type Manager struct {
	db     *sql.DB
	kv     recordStore
	cipher Encryptor
	busy   *sync.Map
	client *http.Client
	notify func(context.Context, string, string, string) error
	cancel context.CancelFunc
	done   chan struct{}
}

func New(db *sql.DB, cipher Encryptor, notify func(context.Context, string, string, string) error) *Manager {
	return &Manager{db: db, kv: recordStore{db}, cipher: cipher, busy: &sync.Map{}, client: monitorClient(""), notify: notify}
}

func (p *Manager) transaction(ctx context.Context, work func(*Manager, context.Context) error) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(74321012)`); err != nil {
		return err
	}
	scoped := *p
	scoped.kv = recordStore{tx}
	if err = work(&scoped, ctx); err != nil {
		return err
	}
	return tx.Commit()
}

type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *bufferedResponse) Write(data []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	return b.body.Write(data)
}

func (p *Manager) httpTransactions(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || strings.HasSuffix(r.URL.Path, "/query") {
			handler.ServeHTTP(w, r)
			return
		}
		result := &bufferedResponse{header: make(http.Header)}
		err := p.transaction(r.Context(), func(scoped *Manager, ctx context.Context) error {
			// 路由复用，但事务内直接调用写处理器，防止递归开启事务。
			switch {
			case r.Method == "PUT" && r.URL.Path == "/bindings":
				scoped.bindHTTP(result, r.WithContext(ctx))
			case r.Method == "POST" && r.URL.Path == "/sites":
				scoped.saveHTTP(result, r.WithContext(ctx))
			case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/sites/") && strings.HasSuffix(r.URL.Path, "/manual"):
				r.SetPathValue("id", strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/sites/"), "/manual"))
				scoped.manualHTTP(result, r.WithContext(ctx))
			case strings.HasPrefix(r.URL.Path, "/sites/") && (r.Method == "PUT" || r.Method == "DELETE"):
				id := strings.TrimPrefix(r.URL.Path, "/sites/")
				if strings.Contains(id, "/") {
					return ErrInvalid
				}
				r.SetPathValue("id", id)
				if r.Method == "DELETE" {
					scoped.deleteHTTP(result, r)
				} else {
					scoped.saveHTTP(result, r)
				}
			default:
				return ErrNotFound
			}
			if result.status >= 400 {
				return errHTTPResponse
			}
			return nil
		})
		if err != nil && !errors.Is(err, errHTTPResponse) {
			reply(w, nil, err)
			return
		}
		for key, values := range result.header {
			w.Header()[key] = values
		}
		w.WriteHeader(result.status)
		_, _ = w.Write(result.body.Bytes())
	})
}

var errHTTPResponse = errors.New("HTTP operation rejected")

func (p *Manager) daily(ctx context.Context, site Site) ([]DailyStat, error) {
	rows, err := p.records(ctx, "daily", fmtSource(site.ID, ""))
	if err != nil {
		return nil, err
	}
	result := []DailyStat{}
	for _, r := range rows {
		var values []DailyStat
		if err = json.Unmarshal(r.ValueJson, &values); err != nil {
			return nil, err
		}
		for _, value := range values {
			expiry, e := summaryExpiresAt(value, site.Config.SummaryRetentionDays)
			if e != nil {
				return nil, e
			}
			if expiry.After(time.Now()) {
				result = append(result, value)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Date > result[j].Date })
	return result, nil
}

func (p *Manager) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				if err := p.cycle(ctx, at); err != nil && ctx.Err() == nil {
					slog.Error("上游站点监控失败", "error", err)
				}
			}
		}
	}()
}
func (p *Manager) Stop() {
	if p.cancel != nil {
		p.cancel()
		<-p.done
	}
	p.client.CloseIdleConnections()
}
func (p *Manager) cycle(ctx context.Context, at time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	lease, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = lease.Rollback() }()
	var locked bool
	if err = lease.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(74321013)`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	if _, err = p.db.ExecContext(ctx, `DELETE FROM upstream_site_records WHERE expires_at<=NOW()`); err != nil {
		return err
	}
	monitorErr := p.RunTask(ctx, "monitor", at)
	return errors.Join(monitorErr, p.deliver(ctx))
}
