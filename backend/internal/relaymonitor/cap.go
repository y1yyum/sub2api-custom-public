package relaymonitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func capSeededHex(seed string, length int) string {
	state := uint32(2166136261)
	for _, c := range []byte(seed) {
		state ^= uint32(c)
		state *= 16777619
	}
	var out strings.Builder
	for out.Len() < length {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		fmt.Fprintf(&out, "%08x", state)
	}
	return out.String()[:length]
}

type capPow struct{ salt, target string }

func solveCapChallenge(ctx context.Context, b providerBody) ([]any, error) {
	if providerString(b["token"]) == "" {
		return nil, errors.New("cap challenge 缺少 token")
	}
	var work []capPow
	format2 := providerCount(b["format"]) == 2
	if format2 {
		for _, v := range providerRows(b["challenges"]) {
			if v["protocol"] != "sha256-pow" {
				return nil, errors.New("cap challenge 协议不受支持")
			}
			p := providerObject(v["payload"])
			work = append(work, capPow{providerString(p["salt"]), providerString(p["target"])})
		}
	} else if rows, ok := b["challenge"].([]any); ok {
		for _, row := range rows {
			a, ok := row.([]any)
			if !ok || len(a) != 2 {
				return nil, errors.New("cap challenge 格式无效")
			}
			work = append(work, capPow{providerString(a[0]), providerString(a[1])})
		}
	} else {
		p := providerObject(b["challenge"])
		count, saltLength, difficulty := providerCount(p["c"]), providerCount(p["s"]), providerCount(p["d"])
		if count < 1 || count > 256 || saltLength < 1 || saltLength > 256 || difficulty < 1 || difficulty > 16 {
			return nil, errors.New("cap challenge 参数无效")
		}
		for n := int64(1); n <= count; n++ {
			seed := providerString(b["token"]) + strconv.FormatInt(n, 10)
			work = append(work, capPow{capSeededHex(seed, int(saltLength)), capSeededHex(seed+"d", int(difficulty))})
		}
	}
	if len(work) < 1 || len(work) > 256 {
		return nil, errors.New("cap challenge 数量无效")
	}
	estimate := float64(0)
	for _, p := range work {
		if len(p.salt) < 1 || len(p.salt) > 256 || len(p.target) < 1 || len(p.target) > 64 {
			return nil, errors.New("cap challenge 格式无效")
		}
		for _, c := range p.target {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return nil, errors.New("cap challenge 目标无效")
			}
		}
		estimate += math.Pow(16, float64(len(p.target)))
	}
	if estimate > 50000000 {
		return nil, errors.New("cap challenge 工作量超出单次登录限制")
	}
	solutions := make([]any, 0, len(work))
	budget := int64(50000000)
	for _, p := range work {
		solved := false
		for nonce := int64(0); budget > 0; nonce++ {
			budget--
			if nonce%1024 == 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
			}
			digest := sha256.Sum256([]byte(p.salt + strconv.FormatInt(nonce, 10)))
			encoded := hex.EncodeToString(digest[:])
			if strings.HasPrefix(encoded, strings.ToLower(p.target)) {
				if format2 {
					solutions = append(solutions, providerBody{"nonce": nonce})
				} else {
					solutions = append(solutions, nonce)
				}
				solved = true
				break
			}
		}
		if !solved {
			return nil, errors.New("cap challenge 超出计算预算")
		}
	}
	return solutions, nil
}
func (m *Module) solveCapEndpoint(ctx context.Context, endpoint string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if e := m.validateURL(endpoint); e != nil {
		return "", e
	}
	b, _, e := m.providerRequest(ctx, strings.TrimRight(endpoint, "/"), "/challenge", "POST", nil, nil)
	if e != nil {
		return "", e
	}
	solutions, e := solveCapChallenge(ctx, b)
	if e != nil {
		return "", e
	}
	redeemed, _, e := m.providerRequest(ctx, strings.TrimRight(endpoint, "/"), "/redeem", "POST", nil, providerBody{"token": b["token"], "solutions": solutions})
	if e != nil {
		return "", e
	}
	if redeemed["success"] != true || providerString(redeemed["token"]) == "" {
		return "", errors.New("cap challenge 验证失败")
	}
	return providerString(redeemed["token"]), nil
}
func (m *Module) providerCaptcha(ctx context.Context, s Station) (string, error) {
	base := providerBase(s)
	path := "/api/v1/settings/public"
	if s.Type == "monoize" {
		path = "/api/dashboard/settings/public"
	}
	b, _, e := m.providerRequest(ctx, base, path, "GET", nil, nil)
	if e != nil {
		return "", nil
	}
	d := providerData(b)
	origin, e := url.Parse(base)
	if e != nil {
		return "", e
	}
	origin.Path = "/"
	origin.RawQuery = ""
	origin.Fragment = ""
	if s.Type == "monoize" {
		if d["captcha_enabled"] != true {
			return "", nil
		}
		raw := providerString(d["cap_api_endpoint"])
		if raw == "" {
			return "", errors.New("站点开启验证码但缺少 Cap 端点")
		}
		endpoint, e := origin.Parse(raw)
		if e != nil {
			return "", errors.New("cap 端点无效")
		}
		return m.solveCapEndpoint(ctx, endpoint.String())
	}
	if d["captcha_provider"] != "cap" || d["turnstile_enabled"] == false {
		return "", nil
	}
	raw, key := providerString(d["cap_api_endpoint"]), strings.Trim(providerString(d["cap_site_key"]), "/")
	if raw == "" || key == "" {
		return "", errors.New("cap 验证码配置不完整")
	}
	endpoint, e := origin.Parse(raw)
	if e != nil {
		return "", errors.New("cap 端点无效")
	}
	origin.Path = strings.TrimRight(endpoint.Path, "/") + "/" + key + "/"
	origin.RawPath = strings.TrimRight(endpoint.EscapedPath(), "/") + "/" + url.PathEscape(key) + "/"
	return m.solveCapEndpoint(ctx, origin.String())
}
