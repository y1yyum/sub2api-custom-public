//go:build integration

package upstreamsites

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type fixtureCipher struct{ cipher.AEAD }

func (c fixtureCipher) Encrypt(s string) (string, error) {
	nonce := make([]byte, c.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(c.Seal(nonce, nonce, []byte(s), nil)), nil
}
func (c fixtureCipher) Decrypt(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	result, err := c.Open(nil, raw[:c.NonceSize()], raw[c.NonceSize():], nil)
	return string(result), err
}

func newNativeTestManager(t *testing.T) *Manager {
	t.Helper()
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image: "postgres:18.1-alpine3.23", Env: map[string]string{"POSTGRES_USER": "test", "POSTGRES_PASSWORD": "test", "POSTGRES_DB": "test"}, ExposedPorts: []string{"5432/tcp"}, WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(time.Minute),
	}, Started: true})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(ctx)) })
	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://test:test@%s:%s/test?sslmode=disable", host, port.Port()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,credentials JSONB DEFAULT '{}',extra JSONB DEFAULT '{}',status TEXT DEFAULT 'active',type TEXT DEFAULT 'apikey',schedulable BOOLEAN DEFAULT true,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 CREATE TABLE scheduler_outbox(event_type TEXT,account_id BIGINT,created_at TIMESTAMPTZ);
 INSERT INTO accounts(id,name) VALUES (1,'enabled'),(2,'manual-disabled'); UPDATE accounts SET schedulable=false WHERE id=2;`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/9012_upstream_sites.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	block, err := aes.NewCipher(bytes.Repeat([]byte{5}, 32))
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)
	p := New(db, fixtureCipher{aead}, nil)
	t.Cleanup(p.Stop)
	return p
}

func TestNativeSiteLifecycle(t *testing.T) {
	ctx := context.Background()
	p := newNativeTestManager(t)
	db := p.db
	var mails atomic.Int32
	p.notify = func(context.Context, string, string, string) error { mails.Add(1); return nil }
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		w := httptest.NewRecorder()
		p.Handler().ServeHTTP(w, r)
		return w
	}
	var status atomic.Int32
	status.Store(200)
	var amount atomic.Value
	amount.Store("0.5")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer wallet-secret", r.Header.Get("Authorization"))
		w.WriteHeader(int(status.Load()))
		fmt.Fprintf(w, `{"isValid":true,"balance":%q,"unit":"USD"}`, amount.Load())
	}))
	defer upstream.Close()
	p.client = upstream.Client()
	config := DefaultConfig()
	config.Name = "native"
	config.BaseURL = upstream.URL
	config.Timezone = "UTC"
	config.Policy.Mode = "balance"
	config.Policy.Thresholds = []Threshold{{Unit: "USD", Low: "1", Recover: "5"}}
	config.Policy.MinSuspendSeconds = 0
	config.Policy.RecoveryCount = 1
	config.Policy.AutoRecover = true
	config.Alerts = AlertPolicy{Control: true, Email: "admin@example.com", Unit: "USD", LowBalance: "1"}
	w := call("POST", "/sites", SaveRequest{Config: config, Secrets: &Secrets{Key: "wallet-secret"}})
	require.Equal(t, 200, w.Code, w.Body.String())
	var created struct {
		Data Site `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	id := created.Data.ID
	path := "/sites/" + strconv.FormatInt(id, 10)
	require.NotContains(t, w.Body.String(), "wallet-secret")
	var ciphertext string
	require.NoError(t, db.QueryRow(`SELECT value->>'ciphertext' FROM upstream_site_records WHERE collection='sites'`).Scan(&ciphertext))
	require.NotContains(t, ciphertext, "wallet-secret")
	w = call("PUT", "/bindings", map[string]any{"version": 0, "bindings": map[string]int64{"1": id, "2": id}})
	require.Equal(t, 200, w.Code, w.Body.String())
	w = call("POST", path+"/query", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	// 同配置的并发完成也必须核对存储版本，迟到响应不能覆盖另一副本的新结果。
	var snapshot StoredSite
	oldVersion, err := p.read(ctx, "sites", strconv.FormatInt(id, 10), &snapshot)
	require.NoError(t, err)
	_, err = p.query(ctx, id, "balance")
	require.NoError(t, err)
	_, err = p.complete(ctx, snapshot.Site, *snapshot.Site.LastObservation, oldVersion)
	require.ErrorIs(t, err, ErrConflict)
	var held int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM accounts WHERE extra ? 'upstream_site_restriction'`).Scan(&held))
	require.Equal(t, 2, held)
	require.NoError(t, p.deliver(ctx))
	require.EqualValues(t, 3, mails.Load())
	require.NoError(t, p.deliver(ctx))
	require.EqualValues(t, 3, mails.Load())
	// 重启读取同一状态，不因构造新管理器解除站点限制。
	restarted := New(db, p.cipher, p.notify)
	require.NoError(t, restarted.transaction(ctx, func(m *Manager, c context.Context) error { return m.reconcile(c) }))
	restarted.Stop()
	status.Store(401)
	w = call("POST", path+"/query", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), Authentication)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM accounts WHERE extra ? 'upstream_site_restriction'`).Scan(&held))
	require.Equal(t, 2, held)
	w = call("GET", "/sites", nil)
	require.Contains(t, w.Body.String(), "0.5")
	require.NotContains(t, w.Body.String(), "wallet-secret")
	// 并发人工关闭与自动恢复；恢复只移除站点限制。
	manual := func(enabled bool) {
		var current StoredSite
		_, e := p.read(ctx, "sites", strconv.FormatInt(id, 10), &current)
		require.NoError(t, e)
		response := call("PUT", path+"/manual", map[string]any{"enabled": enabled, "state_version": current.Site.State.Version})
		require.Equal(t, 200, response.Code, response.Body.String())
	}
	manual(false)
	manual(true)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM accounts WHERE extra ? 'upstream_site_restriction'`).Scan(&held))
	require.Equal(t, 2, held, "人工启用不能清除自动限制")
	manual(false)
	w = call("PUT", path+"/manual", map[string]any{"enabled": true, "state_version": 1})
	require.Equal(t, 409, w.Code)
	w = call("PUT", path+"/manual", map[string]any{"state_version": 1})
	require.Equal(t, 400, w.Code)
	_, err = db.Exec(`UPDATE accounts SET schedulable=false WHERE id=1`)
	require.NoError(t, err)
	status.Store(200)
	amount.Store("10")
	w = call("POST", path+"/query", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM accounts WHERE extra ? 'upstream_site_restriction'`).Scan(&held))
	require.Equal(t, 2, held, "自动恢复不能清除人工禁用")
	var recovered StoredSite
	_, err = p.read(ctx, "sites", strconv.FormatInt(id, 10), &recovered)
	require.NoError(t, err)
	require.Empty(t, recovered.Site.State.Reasons)
	require.True(t, recovered.Site.State.Manual)
	manual(true)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM accounts WHERE extra ? 'upstream_site_restriction'`).Scan(&held))
	require.Zero(t, held)
	var enabled int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM accounts WHERE schedulable`).Scan(&enabled))
	require.Zero(t, enabled)
	// 每次查询提交后即可读到成本；失败不记零，充值不冲减。
	for _, sample := range []struct {
		balance, cost string
		status        int
	}{
		{"7.75", "2.25", 200}, {"0", "2.25", 401}, {"6", "4", 200}, {"20", "4", 200},
	} {
		amount.Store(sample.balance)
		status.Store(int32(sample.status))
		w = call("POST", path+"/query", nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		w = call("GET", "/sites", nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		var listed struct {
			Data struct {
				Sites []Site `json:"sites"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
		require.Equal(t, sample.cost, *listed.Data.Sites[0].Costs[0].Amount)
		require.Equal(t, sample.cost, *listed.Data.Sites[0].Today[0].UpstreamCost)
	}
	// 更新版本及多副本写冲突。
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- call("PUT", path, SaveRequest{Config: config, Version: 1}).Code }()
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	require.Equal(t, 1, counts[200])
	require.Equal(t, 1, counts[409])
	// 错误账户身份在保存时拒绝，事务不改变原配置。
	invalidConfig := config
	invalidConfig.Adapter = "new_api"
	for _, badID := range []string{"0", "-1", "1.1", "9223372036854775808", "42\r\nInjected: yes"} {
		w = call("PUT", path, SaveRequest{Config: invalidConfig, Version: 2, Secrets: &Secrets{WalletUserID: badID}})
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	w = call("DELETE", path+"?config_version=2", nil)
	require.Equal(t, 409, w.Code)
	w = call("PUT", "/bindings", map[string]any{"version": 1, "bindings": map[string]int64{}})
	require.Equal(t, 200, w.Code, w.Body.String())
	w = call("DELETE", path+"?config_version=2", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	w = call("POST", "/sites", SaveRequest{Config: config})
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.Greater(t, created.Data.ID, id)
	w = call("PUT", "/sites/not-an-id", SaveRequest{Config: config})
	require.Equal(t, 400, w.Code)
	w = call("GET", "/sites", nil)
	require.NotContains(t, w.Body.String(), strings.Repeat("5", 64))
}
