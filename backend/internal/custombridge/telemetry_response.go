package custombridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxTelemetryResponseBytes = 64 << 10
	maxTelemetryStreamBytes   = 8 << 20
)

// The existing read wrapper observes only bytes returned to native processing.
// Buffer limits affect evidence, never forwarding, billing, or reader results.
type telemetryBody struct {
	io.ReadCloser
	attempt                                     *TelemetryAttempt
	mediaType                                   string
	failure                                     bool
	pending                                     []byte
	total                                       int
	bad, eof, closed                            bool
	eventType                                   string
	hasEvent, hasData, frameComplete, completed bool
	frameOutput, firstOutput, frameUsage        bool
	frameTerminal                               string
}

func newTelemetryBody(body io.ReadCloser, attempt *TelemetryAttempt, contentType string, observe bool) *telemetryBody {
	b := &telemetryBody{ReadCloser: body, attempt: attempt, failure: attempt.facts.status == 403 || attempt.facts.status == 429}
	if observe {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err == nil && (mediaType == "application/json" || !b.failure && mediaType == "text/event-stream") {
			b.mediaType = mediaType
		}
	}
	attempt.facts.streamed = b.mediaType == "text/event-stream"
	return b
}

func (b *telemetryBody) Read(raw []byte) (int, error) {
	n, err := b.ReadCloser.Read(raw)
	observedAt := time.Now()
	b.attempt.mu.Lock()
	defer b.attempt.mu.Unlock()
	if b.attempt.finished || b.closed {
		b.release()
		return n, err
	}
	if err != nil && !errors.Is(err, io.EOF) {
		// io.Reader may return n>0 together with an error; observe those bytes
		// first so the facts do not depend on how the transport chunked them.
		if b.mediaType != "" && !b.bad && n > 0 {
			b.observe(raw[:n], false, observedAt)
		}
		abandoned := b.bad
		b.attempt.facts.readFailed = true
		b.attempt.facts.responseCompleted = false
		b.bad = true
		if !abandoned && b.attempt.facts.status >= 200 && b.attempt.facts.status <= 299 && b.attempt.facts.terminal == "" {
			b.attempt.facts.streamBreak = true
			b.attempt.facts.timeout = telemetryTimeout(err)
		}
	}
	if b.bad {
		b.attempt.facts.ttft = 0
		b.attempt.facts.failureClass = ""
		b.release()
		return n, err
	}
	if b.mediaType != "" {
		b.observe(raw[:n], errors.Is(err, io.EOF), observedAt)
	}
	return n, err
}

func (b *telemetryBody) Close() error {
	b.attempt.mu.Lock()
	b.closed = true
	b.release()
	b.attempt.mu.Unlock()
	// Closing after native SSE completion may unblock a pending upstream read.
	// That intentional close is not a new transport failure of the response.
	err := b.ReadCloser.Close()
	if err != nil {
		b.attempt.mu.Lock()
		b.attempt.facts.readFailed = true
		b.attempt.facts.responseCompleted = false
		b.attempt.facts.ttft = 0
		b.attempt.facts.failureClass = ""
		b.attempt.mu.Unlock()
	}
	return err
}

func (b *telemetryBody) release() { clear(b.pending); b.pending = nil }

func (b *telemetryBody) append(raw []byte) bool {
	if len(raw) > maxTelemetryResponseBytes-len(b.pending) {
		b.bad = true
		b.release()
		return false
	}
	if len(raw) == 0 {
		return true
	}
	if b.pending == nil {
		b.pending = make([]byte, 0, maxTelemetryResponseBytes)
	}
	b.pending = append(b.pending, raw...)
	return true
}

func (b *telemetryBody) observe(raw []byte, eof bool, observedAt time.Time) {
	if b.eof {
		if len(raw) == 0 {
			return
		}
		b.bad = true
	}
	limit := maxTelemetryResponseBytes
	if b.mediaType == "text/event-stream" {
		limit = maxTelemetryStreamBytes
	}
	if len(raw) > limit-b.total {
		b.bad = true
	}
	if b.bad {
		b.attempt.facts.responseCompleted = false
		b.attempt.facts.ttft = 0
		b.attempt.facts.failureClass = ""
		b.release()
		return
	}
	b.total += len(raw)
	if b.mediaType == "application/json" {
		if !b.append(raw) {
			return
		}
		if eof {
			if b.failure {
				b.attempt.facts.failureClass = telemetryClassifyFailure(b.pending, b.attempt.facts.status, b.attempt.target.ErrorRules)
			} else {
				fields, valid := telemetryResponseObject(b.pending)
				b.completed = valid && fields.status == "completed" && fields.kind == "" && fields.response == nil
				clear(fields.response)
				if b.completed {
					b.attempt.facts.terminal, b.attempt.facts.usagePresent = "completed", fields.hasUsage
				} else if kind, status, ok := telemetryTerminalObject(b.pending); ok && kind == "" && (status == "failed" || status == "incomplete") {
					b.attempt.facts.terminal = status
				}
			}
		}
	} else {
		for len(raw) > 0 && !b.bad {
			end := bytes.IndexByte(raw, '\n')
			if end < 0 {
				b.append(raw)
				break
			}
			if !b.append(raw[:end]) {
				break
			}
			b.line(bytes.TrimSuffix(b.pending, []byte{'\r'}), observedAt)
			clear(b.pending)
			b.pending = b.pending[:0]
			raw = raw[end+1:]
		}
		invalid := b.bad
		// A partial physical line/frame is not a completed Responses event.
		if eof && (len(b.pending) != 0 || b.hasData || b.hasEvent) {
			b.bad = true
		}
		// A valid stream ending without a terminal frame is a break, not a terminal.
		if eof && !invalid && b.attempt.facts.terminal == "" {
			b.attempt.facts.streamBreak = true
		}
		// Native SSE processing may close immediately after a complete frame.
		// A later observed partial/conflicting frame revokes that provisional fact.
		b.attempt.facts.responseCompleted = b.completed && !b.bad && len(b.pending) == 0 && !b.hasData && !b.hasEvent
	}
	if b.bad || eof {
		b.release()
	}
	if b.bad {
		b.attempt.facts.ttft = 0
		b.attempt.facts.failureClass = ""
	}
	if eof {
		b.eof = true
		if b.mediaType == "application/json" {
			b.attempt.facts.responseCompleted = b.completed && !b.bad
		}
	}
}

type telemetryFailureFields struct {
	code, kind                 string
	hasCode, hasType, hasError bool
	errorObject                json.RawMessage
}

func telemetryClassifyFailure(raw []byte, status int, rules []ErrorRule) string {
	fields, valid := telemetryFailureObject(raw)
	defer clear(fields.errorObject)
	if !valid {
		return ""
	}
	if fields.hasError {
		if fields.hasCode || fields.hasType {
			return ""
		}
		fields, valid = telemetryFailureObject(fields.errorObject)
		defer clear(fields.errorObject)
	}
	if !valid || !fields.hasCode || fields.hasError {
		return ""
	}
	for _, rule := range rules {
		if rule.Status == status && rule.Code == fields.code && (rule.Type == "" || rule.Type == fields.kind) {
			return rule.FailureClass
		}
	}
	return ""
}

// Only literal, unique proof keys are accepted. Neither message text nor raw
// error objects escape the bounded observer scratch space.
func telemetryFailureObject(raw []byte) (fields telemetryFailureFields, valid bool) {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return fields, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return fields, false
	}
	seen := map[string]bool{}
	for decoder.More() {
		start := decoder.InputOffset()
		token, err := decoder.Token()
		if err != nil {
			return fields, false
		}
		key, ok := token.(string)
		if !ok || seen[strings.ToLower(key)] {
			return fields, false
		}
		seen[strings.ToLower(key)] = true
		canonical := ""
		for _, name := range []string{"code", "type", "error"} {
			if strings.EqualFold(key, name) {
				canonical = name
				break
			}
		}
		if canonical != "" {
			literal := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(raw[start:decoder.InputOffset()]), []byte{','}))
			if key != canonical || !bytes.Equal(literal, []byte(`"`+canonical+`"`)) {
				return fields, false
			}
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return fields, false
		}
		switch canonical {
		case "code", "type":
			var text string
			if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &text) != nil || !telemetryErrorToken(text) {
				clear(value)
				return fields, false
			}
			if canonical == "code" {
				fields.code, fields.hasCode = text, true
			} else {
				fields.kind, fields.hasType = text, true
			}
		case "error":
			fields.errorObject, fields.hasError = value, true
			continue
		}
		clear(value)
	}
	return fields, true
}

func (b *telemetryBody) line(line []byte, observedAt time.Time) {
	if len(line) == 0 {
		if b.hasEvent && !b.hasData {
			b.bad = true
		}
		b.completed = b.completed || b.frameComplete
		f := &b.attempt.facts
		if !b.bad && b.frameOutput && !b.firstOutput {
			b.firstOutput, f.firstContent = true, true
			duration := f.headerDuration + min(max(observedAt.Sub(f.recordedAt), 0), 900000*time.Millisecond)
			f.ttft = min(duration.Milliseconds(), 900000)
		}
		if !b.bad && b.frameComplete {
			f.terminal, f.usagePresent = "completed", b.frameUsage
		}
		// A failure terminal ends completion evidence for the rest of the stream.
		if !b.bad && b.frameTerminal != "" {
			f.terminal = map[string]string{"response.failed": "failed", "error": "failed", "response.incomplete": "incomplete"}[b.frameTerminal]
			b.bad = true
		}
		b.eventType, b.hasEvent, b.hasData, b.frameComplete = "", false, false, false
		b.frameOutput, b.frameUsage, b.frameTerminal = false, false, ""
		return
	}
	if line[0] == ':' {
		return
	}
	field, value, found := bytes.Cut(line, []byte{':'})
	if !found {
		b.bad = true
		return
	}
	value = bytes.Trim(value, " \t")
	switch string(field) {
	case "id", "retry":
		return
	case "event":
		if b.hasEvent || b.hasData || !telemetryEventType(string(value)) && !telemetryFailureTerminal(string(value)) {
			b.bad = true
			return
		}
		b.eventType, b.hasEvent = string(value), true
	case "data":
		if b.hasData {
			b.bad = true
			return
		}
		b.hasData = true
		if bytes.Equal(value, []byte("[DONE]")) {
			if !b.completed || b.hasEvent {
				b.bad = true
			}
			return
		}
		if b.completed {
			b.bad = true
			return
		}
		if kind, _, ok := telemetryTerminalObject(value); ok && telemetryFailureTerminal(kind) {
			if b.hasEvent && b.eventType != kind {
				b.bad = true
			}
			b.frameTerminal = kind
			return
		}
		fields, valid := telemetryResponseObject(value)
		defer clear(fields.response)
		if !valid || fields.kind == "" || fields.status != "" || (b.hasEvent && b.eventType != fields.kind) {
			b.bad = true
			return
		}
		var response telemetryResponseFields
		if fields.response != nil {
			response, valid = telemetryResponseObject(fields.response)
			defer clear(response.response)
			if !valid || response.response != nil {
				b.bad = true
				return
			}
		}
		if fields.kind == "response.completed" {
			b.frameComplete, b.frameUsage = response.status == "completed", response.hasUsage
			if !b.frameComplete {
				b.bad = true
			}
		} else if !telemetryEventType(fields.kind) || (response.status != "" && response.status != "in_progress" && response.status != "queued") {
			b.bad = true
		}
		if fields.kind == "response.output_text.delta" {
			if !fields.hasDelta {
				b.bad = true
			}
			b.frameOutput = fields.meaningfulDelta
		}
	default:
		b.bad = true
	}
}

func telemetryEventType(kind string) bool {
	switch kind {
	case "response.completed", "response.created", "response.queued", "response.in_progress",
		"response.output_item.added", "response.output_item.done", "response.content_part.added", "response.content_part.done",
		"response.output_text.delta", "response.output_text.done", "response.refusal.delta", "response.refusal.done",
		"response.reasoning_summary_part.added", "response.reasoning_summary_part.done",
		"response.reasoning_summary_text.delta", "response.reasoning_summary_text.done",
		"response.reasoning_text.delta", "response.reasoning_text.done":
		return true
	}
	return false
}

type telemetryResponseFields struct {
	status, kind              string
	response                  json.RawMessage
	hasDelta, meaningfulDelta bool
	hasUsage                  bool
	usageKeys                 int
}

// Decode only completion/timing facts, rejecting duplicate/case-folded proof fields. Raw
// response data lives in bounded scratch space, never in telemetry facts/events.
func telemetryResponseObject(raw []byte) (fields telemetryResponseFields, valid bool) {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return fields, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return fields, false
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fields, false
		}
		key, ok := token.(string)
		if !ok {
			return fields, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fields, false
		}
		// Usage presence is a report fact only and never changes completion validity.
		if key == "usage" {
			fields.usageKeys++
			fields.hasUsage = fields.usageKeys == 1 && len(value) > 0 && value[0] == '{'
		}
		canonical := ""
		for _, name := range []string{"status", "type", "response", "error", "incomplete_details", "delta"} {
			if strings.EqualFold(key, name) {
				canonical = name
				break
			}
		}
		if canonical == "" {
			clear(value)
			continue
		}
		if key != canonical || seen[canonical] {
			clear(value)
			return fields, false
		}
		seen[canonical] = true
		switch key {
		case "status":
			if json.Unmarshal(value, &fields.status) != nil {
				clear(value)
				return fields, false
			}
			if fields.status != "completed" && fields.status != "in_progress" && fields.status != "queued" {
				clear(value)
				return fields, false
			}
		case "type":
			if json.Unmarshal(value, &fields.kind) != nil || !telemetryEventType(fields.kind) {
				clear(value)
				return fields, false
			}
		case "response":
			fields.response = value
			continue
		case "delta":
			var delta string
			if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &delta) != nil {
				clear(value)
				return fields, false
			}
			fields.hasDelta, fields.meaningfulDelta = true, strings.TrimSpace(delta) != ""
		default:
			if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				clear(value)
				return fields, false
			}
		}
		clear(value)
	}
	return fields, true
}

func telemetryFailureTerminal(kind string) bool {
	return kind == "response.failed" || kind == "response.incomplete" || kind == "error"
}

// telemetryTerminalObject reads only literal, unique top-level type/status
// strings so failure terminals can be named without retaining error details.
func telemetryTerminalObject(raw []byte) (kind, status string, valid bool) {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return "", "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return "", "", false
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return "", "", false
		}
		key, _ := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "", "", false
		}
		canonical := ""
		if strings.EqualFold(key, "type") || strings.EqualFold(key, "status") {
			canonical = strings.ToLower(key)
		}
		if canonical == "" {
			clear(value)
			continue
		}
		var text string
		if key != canonical || seen[canonical] || json.Unmarshal(value, &text) != nil {
			clear(value)
			return "", "", false
		}
		seen[canonical] = true
		if canonical == "type" {
			kind = text
		} else {
			status = text
		}
		clear(value)
	}
	return kind, status, true
}
