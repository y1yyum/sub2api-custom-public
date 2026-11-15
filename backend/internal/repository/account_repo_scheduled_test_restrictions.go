package repository

import "github.com/Wei-Shaw/sub2api/internal/service"

// 运行时限制仅由所属模块写入，表单、复制和批量编辑不能注入或覆盖。
func stripScheduledTestRestrictionsFromExtra(extra map[string]any) map[string]any {
	_, quiz := extra[service.AccountExtraScheduledTestRestrictions]
	_, site := extra[service.AccountExtraUpstreamSiteRestriction]
	if !quiz && !site {
		return extra
	}
	stripped := make(map[string]any, len(extra))
	for key, value := range extra {
		if key != service.AccountExtraScheduledTestRestrictions && key != service.AccountExtraUpstreamSiteRestriction {
			stripped[key] = value
		}
	}
	return stripped
}
