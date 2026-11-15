//go:build integration

package upstreamsites

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeSiteRetentionPreservesCosts(t *testing.T) {
	p := newNativeTestManager(t)
	ctx := context.Background()
	save := func(config Config, id, version int64) Site {
		method, path := "POST", "/sites"
		if id > 0 {
			method, path = "PUT", fmt.Sprintf("/sites/%d", id)
		}
		raw, err := json.Marshal(SaveRequest{Config: config, Version: version})
		require.NoError(t, err)
		w := httptest.NewRecorder()
		p.Handler().ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(raw)))
		require.Equal(t, 200, w.Code, w.Body.String())
		var response struct {
			Data Site `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		return response.Data
	}
	apply := func(id int64, at time.Time, amount string) {
		var stored StoredSite
		version, err := p.read(ctx, "sites", strconv.FormatInt(id, 10), &stored)
		require.NoError(t, err)
		o := statSample(at, amount)
		o.RunID = at.Format(time.RFC3339Nano)
		o.ConfigVersion = stored.Site.ConfigVersion
		o.WalletEpoch = stored.Site.WalletEpoch
		o.StatisticsEpoch = stored.Site.StatisticsEpoch
		_, err = p.complete(ctx, stored.Site, o, version)
		require.NoError(t, err)
		_, err = p.complete(ctx, stored.Site, o, version)
		require.ErrorIs(t, err, ErrConflict, "同一已完成区间不得重复累计")
	}
	config := DefaultConfig()
	config.BaseURL = "https://example.com"
	config.RawRetentionDays = 7
	config.SummaryRetentionDays = 7
	now := time.Now().UTC()
	day := now.AddDate(0, 0, -6).Truncate(24 * time.Hour)
	site := save(config, 0, 0)
	apply(site.ID, day.Add(10*time.Hour), "100")
	apply(site.ID, day.Add(11*time.Hour), "90")
	var expires time.Time
	key := fmtSource(site.ID, day.Format(time.DateOnly))
	require.NoError(t, p.db.QueryRow(`SELECT expires_at FROM upstream_site_records WHERE collection='daily' AND key=$1`, key).Scan(&expires))
	require.True(t, expires.Equal(day.AddDate(0, 0, 7)))
	apply(site.ID, day.Add(12*time.Hour), "80")
	var after time.Time
	require.NoError(t, p.db.QueryRow(`SELECT expires_at FROM upstream_site_records WHERE collection='daily' AND key=$1`, key).Scan(&after))
	require.True(t, after.Equal(expires), "新查询不延长已有统计日的保留期")
	days, err := p.daily(ctx, site)
	require.NoError(t, err)
	require.Len(t, days, 1)
	require.Equal(t, "20", *days[0].UpstreamCost)

	// 模拟时钟推进后的真实持久状态：日汇总完整，而同一天的前两条原始记录已经过期。
	config.SummaryRetentionDays = 365
	site = save(config, 0, 0)
	day = now.AddDate(0, 0, -7).Truncate(24 * time.Hour)
	samples := []Observation{}
	for i, value := range []string{"100", "90", "80", "70"} {
		o := statSample(day.Add(time.Duration(2+i)*time.Hour), value)
		samples = append(samples, o)
		expiry := now.Add(time.Hour)
		if i < 2 {
			expiry = now.Add(-time.Hour)
		}
		_, err = p.kv.Put(ctx, "history", fmtSource(site.ID, strconv.Itoa(i)), o, ExpiresAt(expiry))
		require.NoError(t, err)
	}
	key = fmtSource(site.ID, day.Format(time.DateOnly))
	_, err = p.kv.Put(ctx, "daily", key, DailyStats(samples), ExpiresAt(day.AddDate(0, 0, 365)))
	require.NoError(t, err)
	var stored StoredSite
	version, err := p.read(ctx, "sites", strconv.FormatInt(site.ID, 10), &stored)
	require.NoError(t, err)
	stored.Site.Latest = &samples[len(samples)-1]
	_, err = p.kv.Put(ctx, "sites", strconv.FormatInt(site.ID, 10), stored, IfVersion(version))
	require.NoError(t, err)
	config.RawRetentionDays = 90
	site = save(config, site.ID, site.ConfigVersion)
	apply(site.ID, now, "70")
	days, err = p.daily(ctx, site)
	require.NoError(t, err)
	site.summarizeCosts(days, now)
	require.Equal(t, "30", *site.Costs[0].Amount)
	apply(site.ID, now.Add(time.Minute), "65")
	days, err = p.daily(ctx, site)
	require.NoError(t, err)
	site.summarizeCosts(days, now)
	require.Equal(t, "35", *site.Costs[0].Amount)

	config.RawRetentionDays, config.SummaryRetentionDays = 7, 7
	site = save(config, site.ID, site.ConfigVersion)
	days, err = p.daily(ctx, site)
	require.NoError(t, err)
	require.Len(t, days, 1)
	require.Equal(t, "5", *days[0].UpstreamCost)
	var count int
	require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM upstream_site_records WHERE collection='daily' AND key=$1`, key).Scan(&count))
	require.Zero(t, count, "缩短保留期同步清理过期日汇总")
}

func TestNativeProbeConfigurationLifecycle(t *testing.T) {
	p := newNativeTestManager(t)
	ctx := context.Background()
	config := DefaultConfig()
	config.BaseURL = "https://example.com"
	probe := Probe{ID: "health", URL: "https://example.com/health", IntervalMinutes: 5, TimeoutSeconds: 10, JSONPath: "success", Expected: "true"}
	config.Probes = []Probe{probe}
	var site Site
	save := func(keys map[string]string, want int, retained map[string]string) {
		method, path := "POST", "/sites"
		if site.ID > 0 {
			method, path = "PUT", fmt.Sprintf("/sites/%d", site.ID)
		}
		raw, err := json.Marshal(SaveRequest{Config: config, Version: site.ConfigVersion, Secrets: &Secrets{ProbeKeys: keys}})
		require.NoError(t, err)
		w := httptest.NewRecorder()
		p.Handler().ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(raw)))
		require.Equal(t, want, w.Code, w.Body.String())
		if want == 200 {
			var response struct {
				Data Site `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			site = response.Data
		}
		var stored StoredSite
		_, err = p.read(ctx, "sites", strconv.FormatInt(site.ID, 10), &stored)
		require.NoError(t, err)
		plain, err := p.cipher.Decrypt(stored.Ciphertext)
		require.NoError(t, err)
		var secrets Secrets
		require.NoError(t, json.Unmarshal([]byte(plain), &secrets))
		require.Equal(t, retained, secrets.ProbeKeys)
	}
	key := map[string]string{"health": "fixture-key"}
	save(key, 200, key)
	save(nil, 200, key)
	config.Probes = []Probe{}
	save(nil, 200, map[string]string{})
	config.Probes = []Probe{probe}
	save(nil, 200, map[string]string{})
	save(key, 200, key)
	config.Probes[0].URL = "https://example.com/ready"
	save(nil, 200, map[string]string{})
	save(map[string]string{"unused": "fixture-value"}, 400, map[string]string{})
}
