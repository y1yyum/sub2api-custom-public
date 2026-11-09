package dispatch

import "time"

// 协议事实只计数和报告：它们出现时 outcome 必为 Unknown／ClientCanceled，或仍是
// 原有可信 Success，因此不会成为新的失败率、超时或熔断输入。
var protocolFactPhases = map[string]map[string]bool{
	"semantic_failure":  {"terminal": true},
	"incomplete":        {"terminal": true},
	"non_text_terminal": {"terminal": true},
	"stream_break":      {"headers": true, "content": true},
	"local_timeout":     {"connect": true, "headers": true, "content": true},
	"bridge_deadline":   {"": true, "connect": true, "headers": true, "content": true, "terminal": true},
}

func validProtocolFacts(o Observation) bool {
	switch o.Phase {
	case "", "connect", "headers", "content", "terminal":
	default:
		return false
	}
	if o.UsageMissing && (o.Phase != "terminal" || o.ProtocolFact != "" && o.ProtocolFact != "non_text_terminal" ||
		o.Outcome != Success && o.Outcome != Unknown) {
		return false
	}
	if o.ProtocolFact == "" {
		return true
	}
	if !protocolFactPhases[o.ProtocolFact][o.Phase] {
		return false
	}
	switch o.ProtocolFact {
	case "bridge_deadline":
		return o.Outcome == ClientCanceled
	case "non_text_terminal":
		return (o.Outcome == Success || o.Outcome == Unknown) && o.TTFTMillis == 0
	default:
		return o.Outcome == Unknown
	}
}

// protocolFactKeys 返回固定低基数的报告键；中断与超时附带阶段以区分连接、首内容前和流中途。
func protocolFactKeys(o Observation) []string {
	keys := make([]string, 0, 2)
	switch o.ProtocolFact {
	case "":
	case "stream_break", "local_timeout", "bridge_deadline":
		if o.Phase == "" {
			keys = append(keys, o.ProtocolFact)
		} else {
			keys = append(keys, o.ProtocolFact+"."+o.Phase)
		}
	default:
		keys = append(keys, o.ProtocolFact)
	}
	if o.UsageMissing {
		keys = append(keys, "usage_missing")
	}
	return keys
}

func (h *Health) recordProtocolFacts(o Observation, now time.Time) {
	keys := protocolFactKeys(o)
	if len(keys) == 0 {
		return
	}
	bucket := h.currentHealthBucket(now)
	if bucket.Facts == nil {
		bucket.Facts = make(map[string]int, len(keys))
	}
	for _, key := range keys {
		bucket.Facts[key]++
	}
}
