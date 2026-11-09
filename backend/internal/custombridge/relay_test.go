package custombridge

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBridgeRelayReceiptEnvelope(t *testing.T) {
	id := uuid.NewString()
	left, err := NewReceiptEnvelope("core", id, json.RawMessage(`{"schema_version":1,"receipt_id":"`+id+`","raw_usage":{"tokens":9007199254740993}}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewReceiptEnvelope("core", id, json.RawMessage(`{ "raw_usage":{"tokens":9007199254740993}, "receipt_id":"`+id+`", "schema_version":1 }`))
	if err != nil {
		t.Fatal(err)
	}
	if left.PayloadDigest != right.PayloadDigest {
		t.Fatal("equivalent JSON has different digest")
	}
	if _, err := validateReceiptEnvelope(left); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ReceiptEnvelope){
		func(value *ReceiptEnvelope) { value.PayloadDigest = "wrong" },
		func(value *ReceiptEnvelope) { value.EventID = uuid.NewString() },
		func(value *ReceiptEnvelope) { value.Source = "" },
		func(value *ReceiptEnvelope) { value.Receipt = json.RawMessage(`null`) },
	} {
		changed := left
		mutate(&changed)
		if _, err := validateReceiptEnvelope(changed); !errors.Is(err, ErrReceiptEnvelope) {
			t.Fatalf("invalid envelope accepted: %v", err)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"receipt_id":"x"} {}`, `invalid`} {
		if _, err := NewReceiptEnvelope("core", id, json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid receipt accepted: %s", raw)
		}
	}
}

func TestBridgeRelayBackoffBound(t *testing.T) {
	repo := &RelayRepository{config: RelayConfig{RetryMin: time.Second, RetryMax: time.Minute}}
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 6: 32 * time.Second, 7: time.Minute, 1000: time.Minute} {
		if got := repo.backoff(attempt); got != want {
			t.Fatalf("attempt=%d got=%s want=%s", attempt, got, want)
		}
	}
}
