package service

import "context"

// GetUnifiedModelChannelStatusEnabled reports whether the unified model and
// channel status catalog is switched on. It fails closed: any read error or a
// missing settings repository means disabled.
func (s *SettingService) GetUnifiedModelChannelStatusEnabled(ctx context.Context) bool {
	if s == nil || s.settingRepo == nil {
		return false
	}
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyUnifiedModelChannelStatusEnabled})
	if err != nil {
		return false
	}
	return vals[SettingKeyUnifiedModelChannelStatusEnabled] == "true"
}
