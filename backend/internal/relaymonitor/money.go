package relaymonitor

import (
	"errors"
	"regexp"

	"github.com/shopspring/decimal"
)

var ratePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,15})(\.[0-9]{1,8})?$`)

func validateRate(value string) (string, error) {
	if !ratePattern.MatchString(value) {
		return "", errors.New("汇率必须是正数，整数最多16位、小数最多8位")
	}
	d, err := decimal.NewFromString(value)
	if err != nil || !d.IsPositive() {
		return "", errors.New("汇率必须大于零")
	}
	return d.String(), nil
}

func applyRate(station Station, token *Token) {
	token.EffectiveCNYPerUSD = station.CNYPerUSD
	token.RateSource = "station"
	if token.CNYPerUSDOverride != nil {
		token.EffectiveCNYPerUSD = *token.CNYPerUSDOverride
		token.RateSource = "token"
	}
}

func convertCNY(amount *string, currency, rate string) (*string, error) {
	if amount == nil {
		return nil, nil
	}
	d, err := decimal.NewFromString(*amount)
	if err != nil {
		return nil, errors.New("上游金额格式错误")
	}
	if currency == "USD" {
		r, e := decimal.NewFromString(rate)
		if e != nil || !r.IsPositive() {
			return nil, errors.New("汇率无效")
		}
		d = d.Mul(r)
	} else if currency != "CNY" {
		return nil, errors.New("上游币种尚未标准化")
	}
	result := d.String()
	return &result, nil
}
