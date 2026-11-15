package upstreamsites

import (
	"context"
	"encoding/json"
	"fmt"
)

func deliveryFor(email string) string {
	if email == "" {
		return "not_configured"
	}
	return "pending"
}

// 先提交 sending 状态再发邮件。结果未知的邮件不自动重发，避免重启后重复通知。
func (p *Manager) deliver(ctx context.Context) error {
	rows, err := p.kv.db.QueryContext(ctx, `SELECT key,value FROM upstream_site_records WHERE collection='events' AND value->>'delivery'='pending' AND (expires_at IS NULL OR expires_at>NOW()) ORDER BY key LIMIT 100`)
	if err != nil {
		return err
	}
	records := []*Record{}
	for rows.Next() {
		r := &Record{}
		if err = rows.Scan(&r.Key, &r.ValueJson); err != nil {
			_ = rows.Close()
			return err
		}
		records = append(records, r)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, r := range records {
		var event struct {
			SiteName  string `json:"site_name"`
			Email     string `json:"email"`
			Kind      string `json:"kind"`
			Value     string `json:"value"`
			Unit      string `json:"unit"`
			Threshold string `json:"threshold"`
		}
		if err = json.Unmarshal(r.ValueJson, &event); err != nil {
			return err
		}
		result, err := p.db.ExecContext(ctx, `UPDATE upstream_site_records SET value=jsonb_set(value,'{delivery}','"sending"'),version=version+1 WHERE collection='events' AND key=$1 AND value->>'delivery'='pending'`, r.Key)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		status := "delivered"
		if err = p.notify(ctx, event.Email, "上游站点告警："+event.SiteName, fmt.Sprintf("站点：%s\n事件：%s\n数值：%s %s\n阈值：%s", event.SiteName, event.Kind, event.Value, event.Unit, event.Threshold)); err != nil {
			status = "failed_or_unknown"
		}
		if _, err = p.db.ExecContext(ctx, `UPDATE upstream_site_records SET value=jsonb_set(value,'{delivery}',to_jsonb($2::text)),version=version+1 WHERE collection='events' AND key=$1`, r.Key, status); err != nil {
			return err
		}
	}
	return nil
}
