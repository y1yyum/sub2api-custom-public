package relaymonitor

func mergeSecretFields(existing, patch map[string]string) map[string]string {
	merged := make(map[string]string, len(existing)+len(patch))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range patch {
		if value == "" {
			delete(merged, key)
		} else {
			merged[key] = value
		}
	}
	return merged
}
