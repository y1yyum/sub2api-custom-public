package service

import (
	"context"
	"database/sql"
	"github.com/Wei-Shaw/sub2api/internal/upstreamsites"
	"html"
	"strings"
)

const AccountExtraUpstreamSiteRestriction = upstreamsites.AccountRestrictionKey

func (a *Account) IsUpstreamSiteBlocked() bool {
	restriction, _ := a.Extra[AccountExtraUpstreamSiteRestriction].(map[string]any)
	return len(restriction) > 0
}

func ProvideUpstreamSites(db *sql.DB, cipher SecretEncryptor, email *EmailService) *upstreamsites.Manager {
	manager := upstreamsites.New(db, cipher, func(ctx context.Context, to, subject, body string) error {
		return email.SendEmail(ctx, to, subject, "<p>"+strings.ReplaceAll(html.EscapeString(body), "\n", "<br>")+"</p>")
	})
	manager.Start()
	return manager
}
