package relaymonitor

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type Station struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	Type           string    `json:"type"`
	Enabled        bool      `json:"enabled"`
	CNYPerUSD      string    `json:"cny_per_usd"`
	BalanceMode    string    `json:"balance_mode"`
	BalanceTokenID *int64    `json:"balance_token_id"`
	LowBalance     string    `json:"low_balance"`
	NoRenewal      bool      `json:"no_renewal"`
	IncludeInCost  bool      `json:"include_in_cost"`
	SubjectID      *int64    `json:"subject_id"`
	ChannelAliases []string  `json:"channel_aliases"`
	Version        int64     `json:"version"`
	Latest         *Snapshot `json:"latest,omitempty"`
}

type Token struct {
	ID                  int64             `json:"id"`
	StationID           int64             `json:"station_id"`
	Name                string            `json:"name"`
	AccountID           *int64            `json:"account_id"`
	Enabled             bool              `json:"enabled"`
	CNYPerUSDOverride   *string           `json:"cny_per_usd_override"`
	EffectiveCNYPerUSD  string            `json:"effective_cny_per_usd"`
	RateSource          string            `json:"rate_source"`
	HasCredential       bool              `json:"has_credential"`
	Version             int64             `json:"version"`
	Credential          map[string]string `json:"-"`
	EncryptedCredential string            `json:"-"`
}

type QueryResult struct {
	Remaining   *string           `json:"remaining"`
	Used        *string           `json:"used"`
	TodayUsed   *string           `json:"today_used"`
	Currency    string            `json:"currency"`
	Requests    int64             `json:"requests"`
	Tokens      int64             `json:"tokens"`
	Account     string            `json:"account,omitempty"`
	BalanceKind string            `json:"balance_kind"`
	Credential  map[string]string `json:"-"`
	Estimated   bool              `json:"estimated"`
}

type Snapshot struct {
	ID           int64     `json:"id"`
	StationID    int64     `json:"station_id"`
	TokenID      int64     `json:"token_id"`
	CheckedAt    time.Time `json:"checked_at"`
	OK           bool      `json:"ok"`
	Error        string    `json:"error,omitempty"`
	LatencyMS    int64     `json:"latency_ms"`
	CNYPerUSD    string    `json:"cny_per_usd"`
	RemainingCNY *string   `json:"remaining_cny"`
	TodayUsedCNY *string   `json:"today_used_cny"`
	QueryResult
}

type Settings struct {
	Enabled                 bool   `json:"enabled"`
	RefreshSeconds          int    `json:"refresh_seconds"`
	Timezone                string `json:"timezone"`
	RetentionDays           int    `json:"retention_days"`
	DailyReportEnabled      bool   `json:"daily_report_enabled"`
	DailyReportTime         string `json:"daily_report_time"`
	LowBalance              string `json:"low_balance"`
	ErrorThreshold          int    `json:"error_threshold"`
	ETAHours                int    `json:"eta_hours"`
	CooldownMinutes         int    `json:"cooldown_minutes"`
	EncryptionKeyConfigured bool   `json:"encryption_key_configured"`
}

type UsageRow struct {
	Currency            string `json:"currency"`
	Model               string `json:"model"`
	Time                string `json:"time,omitempty"`
	Name                string `json:"name,omitempty"`
	StationID           int64  `json:"station_id,omitempty"`
	TokenID             int64  `json:"token_id,omitempty"`
	Requests            int64  `json:"requests"`
	InputTokens         *int64 `json:"input_tokens"`
	OutputTokens        *int64 `json:"output_tokens"`
	CacheReadTokens     *int64 `json:"cache_read_tokens"`
	CacheCreationTokens *int64 `json:"cache_creation_tokens"`
	Cost                string `json:"cost,omitempty"`
	CostCNY             string `json:"cost_cny,omitempty"`
	Estimated           bool   `json:"estimated"`
}

type Module struct {
	db        *sql.DB
	cfg       *config.Config
	accounts  service.AccountRepository
	encryptor service.SecretEncryptor
	client    *http.Client
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	sem       chan struct{}
}
