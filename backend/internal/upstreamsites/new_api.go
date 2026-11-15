package upstreamsites

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

// New API wallet discovery follows the host adapter, without parsing key limits.
// Only the token route's explicitly named user_balance fields are account funds.
type newAPISettings struct {
	unit         string
	factor, rate decimal.Decimal
}

func defaultNewAPISettings() newAPISettings {
	return newAPISettings{"USD", decimal.NewFromInt(500000), decimal.NewFromInt(1)}
}

func newAPIDisplaySettings(ctx context.Context, client *http.Client, base string) newAPISettings {
	s := defaultNewAPISettings()
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	body, status, err := request(probe, client, endpoint(base, "/api/status"), "", "")
	if err != nil || status < 200 || status >= 300 || !json.Valid(body) {
		return s
	}
	r := gjson.ParseBytes(body)
	if r.Get("success").Type != gjson.True || r.Get("data.quota_display_type").Type != gjson.String {
		return s
	}
	u := strings.ToUpper(strings.TrimSpace(r.Get("data.quota_display_type").String()))
	if validUnit(u) {
		s.unit = u
	}
	for path, dest := range map[string]*decimal.Decimal{"data.quota_per_unit": &s.factor, "data.usd_exchange_rate": &s.rate} {
		if v, e := newAPINumber(r.Get(path)); e == nil && v.IsPositive() {
			*dest = v
		}
	}
	return s
}

func newAPIUserID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return "", ErrInvalid
	}
	return strconv.FormatInt(v, 10), nil
}

func newAPIUnit(raw, fallback string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "USD", "$", "US$":
		return "USD", true
	case "CNY", "RMB", "¥", "￥":
		return "CNY", true
	case "TOKENS", "TOKEN":
		return "TOKENS", true
	}
	return fallback, false
}

func newAPINumber(v gjson.Result) (decimal.Decimal, error) {
	if v.Type == gjson.String {
		t := strings.TrimSpace(v.String())
		t = strings.ReplaceAll(t, ",", "")
		t = strings.TrimPrefix(strings.TrimPrefix(t, "$"), "¥")
		v.Str = t
	}
	n, err := walletNumber(v)
	if err != nil {
		return decimal.Zero, err
	}
	return decimal.NewFromString(n)
}

func newAPIFirst(r gjson.Result, fields ...string) gjson.Result {
	for _, field := range fields {
		v := r.Get(field)
		if v.Exists() && v.Type != gjson.Null {
			return v
		}
	}
	return gjson.Result{}
}

func newAPIEnvelope(body []byte) (gjson.Result, error) {
	if !json.Valid(body) {
		return gjson.Result{}, errWallet
	}
	r := gjson.ParseBytes(body)
	if !r.IsObject() {
		return r, errWallet
	}
	for _, field := range []string{"success", "code"} {
		v := r.Get(field)
		if v.Type == gjson.False {
			return r, errAuth
		}
		if v.Exists() && v.Type != gjson.Null && v.Type != gjson.True {
			return r, errWallet
		}
	}
	return r, nil
}

func (s newAPISettings) amount(v gjson.Result, display bool) (string, error) {
	n, err := newAPINumber(v)
	if err != nil {
		return "", err
	}
	if !display && s.unit != "TOKENS" {
		if s.unit == "CNY" {
			n = n.Mul(s.rate)
		}
		n = n.DivRound(s.factor, 10)
	}
	return walletNumber(gjson.Result{Type: gjson.String, Str: n.String()})
}

func newAPIEmbedded(body []byte, s newAPISettings) ([]Balance, bool, error) {
	r, err := newAPIEnvelope(body)
	if err != nil {
		return nil, newAPIFirst(r.Get("data"), "user_balance_display", "user_balance").Exists(), err
	}
	d := r.Get("data")
	v := newAPIFirst(d, "user_balance_display", "user_balance")
	if !v.Exists() {
		return nil, false, nil
	}
	display := newAPIFirst(d, "user_balance_display").Exists()
	unit := s.unit
	if display {
		unit, _ = newAPIUnit(d.Get("currency").String(), s.unit)
	}
	n, err := s.amount(v, display)
	if err != nil {
		return nil, true, err
	}
	return []Balance{{Unit: unit, Available: n}}, true, nil
}

func newAPISelf(body []byte, s newAPISettings, expectedID string) ([]Balance, error) {
	r, err := newAPIEnvelope(body)
	if err != nil {
		return nil, err
	}
	d := r.Get("data")
	if !d.IsObject() {
		return nil, errWallet
	}
	if expectedID != "" {
		v, e := newAPINumber(d.Get("id"))
		if e != nil || !v.IsInteger() || !v.IsPositive() || v.GreaterThan(decimal.NewFromInt(9223372036854775807)) || v.String() != expectedID {
			return nil, errAuth
		}
	}
	unit, display := newAPIUnit(d.Get("currency").String(), s.unit)
	n, err := s.amount(d.Get("quota"), display)
	if err != nil {
		return nil, err
	}
	// used_quota is lifetime usage, not an account wallet capacity component.
	return []Balance{{Unit: unit, Available: n}}, nil
}

func newAPICompatible(body []byte, s newAPISettings) ([]Balance, error) {
	r, err := newAPIEnvelope(body)
	if err != nil {
		return nil, err
	}
	d := r.Get("data")
	if d.Exists() && d.Type != gjson.Null && !d.IsObject() {
		return nil, errWallet
	}
	infos := r.Get("balance_infos")
	if v := d.Get("balance_infos"); v.IsArray() && len(v.Array()) > 0 {
		infos = v
	}
	selected, nested := r, false
	if infos.Exists() && infos.Type != gjson.Null && !infos.IsArray() {
		return nil, errWallet
	}
	if list := infos.Array(); len(list) > 0 {
		selected = list[0]
		for _, item := range list {
			if unit, _ := newAPIUnit(item.Get("currency").String(), s.unit); unit == s.unit {
				selected = item
				break
			}
		}
	} else if d.IsObject() {
		selected, nested = d, true
	}
	unit, display := newAPIUnit(selected.Get("currency").String(), s.unit)
	if strings.TrimSpace(selected.Get("currency").String()) == "" && (!nested || !newAPIFirst(selected, "quota").Exists()) {
		display = true
	}
	fields := []string{"total_balance", "balance", "remaining"}
	if nested {
		fields = append(fields, "quota")
	}
	n, err := s.amount(newAPIFirst(selected, fields...), display)
	if err != nil {
		return nil, err
	}
	w := Balance{Unit: unit, Available: n}
	used := newAPIFirst(selected, "used_balance", "used")
	if nested {
		used = newAPIFirst(selected, "used", "used_quota")
	}
	for _, item := range []struct {
		raw  gjson.Result
		dest **string
	}{{used, &w.Used}, {selected.Get("total"), &w.Total}} {
		if !item.raw.Exists() || item.raw.Type == gjson.Null {
			continue
		}
		v, e := s.amount(item.raw, display)
		if e != nil {
			return nil, e
		}
		num, _ := decimal.NewFromString(v)
		if num.IsNegative() {
			return nil, errWallet
		}
		*item.dest = &v
	}
	return []Balance{w}, nil
}

// Authentication, rate limits, and deadlines are terminal. Missing routes and
// unusable shapes may fall back, but malformed embedded account amounts may not.
func queryNewAPI(ctx context.Context, client *http.Client, base string, secret Secrets) ([]Balance, int, error) {
	user, err := newAPIUserID(secret.WalletUserID)
	if err != nil {
		return nil, 0, err
	}
	settings := newAPIDisplaySettings(ctx, client, base)
	key, token := strings.TrimSpace(secret.Key), strings.TrimSpace(secret.WalletToken)
	if key != "" {
		body, status, e := request(ctx, client, endpoint(base, "/api/usage/token/"), key, "")
		if e != nil || status == 401 || status == 403 || status == 429 || status >= 500 {
			return nil, status, e
		}
		if status >= 200 && status < 300 {
			wallet, present, e := newAPIEmbedded(body, settings)
			if present || errors.Is(e, errAuth) {
				return wallet, status, e
			}
		}
	}
	var previousErr error
	var previousStatus int
	if token != "" {
		body, status, e := request(ctx, client, endpoint(base, "/api/user/self"), token, user)
		if ctx.Err() != nil {
			return nil, status, ctx.Err()
		}
		if status == 401 || status == 403 || status == 429 || errors.Is(e, context.DeadlineExceeded) {
			return nil, status, e
		}
		if e == nil && status >= 200 && status < 300 {
			wallet, parseErr := newAPISelf(body, settings, user)
			if parseErr == nil || errors.Is(parseErr, errAuth) {
				return wallet, status, parseErr
			}
		}
		previousErr, previousStatus = e, status
	}
	if key == "" {
		if previousErr != nil || previousStatus >= 500 {
			return nil, previousStatus, previousErr
		}
		return nil, previousStatus, errWallet
	}
	body, status, err := request(ctx, client, endpoint(base, "/user/balance"), key, "")
	if err != nil {
		if previousErr != nil && ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
			err = previousErr
		}
		return nil, status, err
	}
	if status < 200 || status >= 300 {
		return nil, status, nil
	}
	wallet, err := newAPICompatible(body, settings)
	return wallet, status, err
}
