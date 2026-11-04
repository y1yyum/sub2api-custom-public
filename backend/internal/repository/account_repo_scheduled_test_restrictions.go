package repository

import "github.com/Wei-Shaw/sub2api/internal/service"

// 智力测试限制由计划运行时维护，普通写入和复制不能携带旧快照。
func stripScheduledTestRestrictionsFromExtra(extra map[string]any) map[string]any {
	if _, exists := extra[service.AccountExtraScheduledTestRestrictions]; !exists {
		return extra
	}
	stripped := make(map[string]any, len(extra)-1)
	for key, value := range extra {
		if key != service.AccountExtraScheduledTestRestrictions {
			stripped[key] = value
		}
	}
	return stripped
}
