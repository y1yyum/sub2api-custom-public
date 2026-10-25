package relaymonitor

import (
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var errLinkedOriginMismatch = errors.New("关联账号的 base_url 与站点地址不同源，拒绝发送其凭证")

// linkedAccountOriginMatches keeps a linked account's credential bound to the
// upstream it was issued for: it may only be sent to the station whose origin
// equals the account's base_url origin.
func linkedAccountOriginMatches(stationURL string, account *service.Account) error {
	if account == nil {
		return errLinkedOriginMismatch
	}
	accountOrigin, ok := originOf(account.GetCredential("base_url"))
	if !ok {
		return errLinkedOriginMismatch
	}
	stationOrigin, ok := originOf(stationURL)
	if !ok || stationOrigin != accountOrigin {
		return errLinkedOriginMismatch
	}
	return nil
}

func originOf(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	switch {
	case scheme == "https" && (port == "" || port == "443"):
		port = "443"
	case scheme == "http" && (port == "" || port == "80"):
		port = "80"
	case scheme != "https" && scheme != "http":
		return "", false
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), true
}
