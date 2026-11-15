package upstreamsites

import (
	"encoding/json"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
	"strings"
)

// wallet_parser.go retains the validated account-only parsers from the native implementation.
// Individual API key quotas are never accepted as account funds.

// parseWallet extracts authoritative account fields without reading key allowances.
// Required wallet fields and account validity flags are checked independently;
// decimal conversion rejects invalid, oversized, or excessively precise amounts.
func parseWallet(adapter string, body []byte) ([]Balance, error) {
	if !json.Valid(body) {
		return nil, errWallet
	}
	r := gjson.ParseBytes(body)
	for _, key := range []string{"isValid", "is_available", "success"} {
		if adapter == "deepseek_balance" && key == "is_available" {
			continue
		}
		v := r.Get(key)
		if v.Exists() && v.Type == gjson.False {
			return nil, errAuth
		}
	}
	var wallets []Balance
	switch adapter {
	case "sub2api":
		if r.Get("isValid").Type != gjson.True {
			return nil, errWallet
		}
		v, err := walletNumber(r.Get("balance"))
		if err != nil {
			return nil, err
		}
		unit := r.Get("unit").String()
		if unit != "USD" {
			return nil, errWallet
		}
		wallets = append(wallets, Balance{Unit: unit, Available: v})
	case "zivv":
		if r.Get("is_available").Type != gjson.True {
			return nil, errWallet
		}
		v, err := walletNumber(r.Get("balance"))
		if err != nil {
			return nil, err
		}
		w := Balance{Unit: strings.ToUpper(r.Get("currency").String()), Available: v}
		if raw := r.Get("total_used"); raw.Exists() {
			used, err := walletNumber(raw)
			if err != nil {
				return nil, err
			}
			w.Used = &used
		}
		wallets = append(wallets, w)
	case "derouter":
		a, err := walletNumber(r.Get("available"))
		if err != nil {
			return nil, err
		}
		b, err := walletNumber(r.Get("balance"))
		if err != nil {
			return nil, err
		}
		l, err := walletNumber(r.Get("locked_balance"))
		if err != nil {
			return nil, err
		}
		available, _ := decimal.NewFromString(a)
		total, _ := decimal.NewFromString(b)
		locked, _ := decimal.NewFromString(l)
		if locked.IsNegative() || !total.Sub(locked).Equal(available) {
			return nil, errWallet
		}
		wallets = append(wallets, Balance{Unit: "USD", Available: a, Total: &b})
	case "kimi_balance":
		v, err := walletNumber(r.Get("data.available_balance"))
		if err != nil {
			return nil, err
		}
		wallets = append(wallets, Balance{Unit: "CNY", Available: v})
	case "deepseek_balance":
		if !r.Get("balance_infos").IsArray() {
			return nil, errWallet
		}
		for _, item := range r.Get("balance_infos").Array() {
			v, err := walletNumber(item.Get("total_balance"))
			if err != nil {
				return nil, err
			}
			unit := strings.ToUpper(item.Get("currency").String())
			if unit == "" {
				unit = "CNY"
			}
			wallets = append(wallets, Balance{Unit: unit, Available: v})
		}
	}
	seen := map[string]bool{}
	for _, w := range wallets {
		if (w.Unit != "USD" && w.Unit != "CNY" && w.Unit != "TOKENS") || seen[w.Unit] {
			return nil, errWallet
		}
		seen[w.Unit] = true
	}
	if len(wallets) == 0 {
		return nil, errWallet
	}
	return wallets, nil
}

// walletNumber validates a raw JSON monetary value without float conversion.
func walletNumber(raw gjson.Result) (string, error) {
	if !raw.Exists() || (raw.Type != gjson.Number && raw.Type != gjson.String) {
		return "", errWallet
	}
	text := raw.String()
	if len(text) > 64 {
		return "", errWallet
	}
	v, err := decimal.NewFromString(text)
	if err != nil || v.Exponent() < -10 || v.Exponent() > 20 || v.Abs().GreaterThanOrEqual(decimal.New(1, 20)) {
		return "", errWallet
	}
	return v.String(), nil
}
