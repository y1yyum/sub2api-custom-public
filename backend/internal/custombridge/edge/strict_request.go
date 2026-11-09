package edge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	strictMaxOutputTokens = 4096
	strictMaxTextBytes    = 1 << 20
	strictMaxMessages     = 128
	strictMaxContentItems = 128
	strictMaxInstructions = 32768
)

func validateStrictResponsesBody(body []byte) (string, error) {
	invalid := errors.New("unsupported strict Responses request")
	if !utf8.Valid(body) || checkConfigJSON(json.NewDecoder(bytes.NewReader(body)), 0) != nil {
		return "", invalid
	}
	var request struct {
		Model           string          `json:"model"`
		Input           json.RawMessage `json:"input"`
		MaxOutputTokens int             `json:"max_output_tokens"`
		Stream          json.RawMessage `json:"stream"`
		Instructions    json.RawMessage `json:"instructions"`
	}
	if decodeStrictJSON(body, &request) != nil || !strictModel(request.Model) ||
		request.MaxOutputTokens < 1 || request.MaxOutputTokens > strictMaxOutputTokens || !strictTextInput(request.Input) {
		return "", invalid
	}
	if len(request.Stream) != 0 && !bytes.Equal(request.Stream, []byte("true")) && !bytes.Equal(request.Stream, []byte("false")) {
		return "", invalid
	}
	if len(request.Instructions) != 0 && !strictText(request.Instructions, strictMaxInstructions) {
		return "", invalid
	}
	return request.Model, nil
}

func strictModel(model string) bool {
	if model == "" || len(model) > 255 {
		return false
	}
	for _, char := range model {
		if char <= 0x20 || char >= 0x7f {
			return false
		}
	}
	return true
}

func decodeStrictJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing strict JSON")
	}
	return nil
}

func strictText(raw json.RawMessage, limit int) bool {
	_, ok := strictTextSize(raw, limit)
	return ok
}

func strictTextSize(raw json.RawMessage, limit int) (int, bool) {
	var text string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &text) != nil || len(text) > limit {
		return 0, false
	}
	return len(text), true
}

func strictTextInput(raw json.RawMessage) bool {
	if strictText(raw, strictMaxTextBytes) {
		return true
	}
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if decodeStrictJSON(raw, &messages) != nil || len(messages) == 0 || len(messages) > strictMaxMessages {
		return false
	}
	textBytes := 0
	for _, message := range messages {
		switch message.Role {
		case "system", "developer", "user", "assistant":
		default:
			return false
		}
		if size, ok := strictTextSize(message.Content, strictMaxTextBytes); ok {
			textBytes += size
		} else {
			var items []struct {
				Type string          `json:"type"`
				Text json.RawMessage `json:"text"`
			}
			if decodeStrictJSON(message.Content, &items) != nil || len(items) == 0 || len(items) > strictMaxContentItems {
				return false
			}
			for _, item := range items {
				size, ok := strictTextSize(item.Text, strictMaxTextBytes)
				if item.Type != "input_text" || !ok {
					return false
				}
				textBytes += size
			}
		}
		if textBytes > strictMaxTextBytes {
			return false
		}
	}
	return true
}
