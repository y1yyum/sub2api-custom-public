package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBridgeRelayCommandDisabled(t *testing.T) {
	t.Setenv("BRIDGE_RELAY_SOURCE_DSN", "invalid-dsn-secret")
	t.Setenv("BRIDGE_RELAY_SINK_DSN", "invalid-dsn-secret")
	var output bytes.Buffer
	if err := run(context.Background(), nil, &output); err != nil || !strings.Contains(output.String(), "disabled") {
		t.Fatalf("disabled command: output=%q error=%v", output.String(), err)
	}
	if strings.Contains(output.String(), "secret") {
		t.Fatal("disabled command exposed DSN")
	}
}

func TestBridgeRelayCommandRedactsErrors(t *testing.T) {
	if value := safeError(errors.New("postgres://credential-secret@invalid")); strings.Contains(value, "secret") {
		t.Fatal("error exposed DSN")
	}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"--unexpected=credential-secret"}, &output); err == nil ||
		strings.Contains(err.Error()+output.String(), "credential-secret") {
		t.Fatal("argument parser exposed input or accepted unknown flag")
	}
}

func TestBridgeRelayCommandDSNFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsn")
	if err := os.WriteFile(path, []byte("  fixture-dsn\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRIDGE_RELAY_SOURCE_DSN", "")
	t.Setenv("BRIDGE_RELAY_SOURCE_DSN_FILE", path)
	if dsn, err := loadRelayDSN("SOURCE"); err != nil || dsn != "fixture-dsn" {
		t.Fatalf("load DSN file: %q, %v", dsn, err)
	}
	t.Setenv("BRIDGE_RELAY_SOURCE_DSN", "ambiguous")
	if _, err := loadRelayDSN("SOURCE"); err == nil {
		t.Fatal("ambiguous DSN accepted")
	}
	t.Setenv("BRIDGE_RELAY_SOURCE_DSN", "")
	link := filepath.Join(t.TempDir(), "outside-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRIDGE_RELAY_SOURCE_DSN_FILE", link)
	if _, err := loadRelayDSN("SOURCE"); err == nil {
		t.Fatal("DSN symlink escaped selected directory")
	}
}
