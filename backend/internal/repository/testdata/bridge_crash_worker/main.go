package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/repository/testdata/bridge_crash_worker/protocol"
	_ "github.com/lib/pq"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var config protocol.Config
	if err := json.NewDecoder(os.Stdin).Decode(&config); err != nil {
		return fmt.Errorf("decode worker fixture: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", config.DSN)
	if err != nil {
		return errors.New("open temporary database failed")
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	// 进程被杀后，屏障中的 PostgreSQL 会主动探测断开的客户端。
	if _, err := db.ExecContext(ctx, "SET client_connection_check_interval = '50ms'; SET statement_timeout = '20s'"); err != nil {
		return fmt.Errorf("configure temporary connection: %w", err)
	}
	var backendPID int
	if err := db.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&backendPID); err != nil {
		return fmt.Errorf("read temporary backend pid: %w", err)
	}
	if err := emit(protocol.Event{Stage: "ready", BackendPID: backendPID}); err != nil {
		return err
	}
	if config.Mode == "recover" {
		return recoverAttempt(ctx, db, config)
	}
	intent := protocol.Intent{
		State: "send_intent", SenderPID: os.Getpid(), CoreRequestID: config.Command.RequestID,
		InternalKeyID: config.Command.APIKeyID, Request: *config.Command.BridgeRequest,
	}
	if err := persistJSON(config.IntentPath, intent); err != nil {
		return fmt.Errorf("persist synthetic send intent: %w", err)
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.ProviderURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("call fake provider: %w", err)
	}
	usage, readErr := io.ReadAll(io.LimitReader(response.Body, 65537))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || len(usage) > 65536 || !json.Valid(usage) {
		return errors.New("invalid fake provider response")
	}
	config.Command.BridgeUsage = usage
	if err := emit(protocol.Event{Stage: "provider_returned", Intent: &intent}); err != nil {
		return err
	}
	if config.Mode == "before_apply" {
		if err := waitForRelease(ctx, config.ContinuePath); err != nil {
			return err
		}
	}
	if err := emit(protocol.Event{Stage: "billing_started"}); err != nil {
		return err
	}
	result, err := repository.NewUsageBillingRepository(nil, db).Apply(ctx, &config.Command)
	if err != nil {
		return fmt.Errorf("apply real usage billing: %w", err)
	}
	if err := emit(protocol.Event{Stage: "committed_before_ack", Applied: result.Applied, Receipt: result.BridgeReceipt}); err != nil {
		return err
	}
	if config.Mode == "after_commit" {
		if err := waitForRelease(ctx, config.ContinuePath); err != nil {
			return err
		}
	}
	return persistJSON(config.Acknowledgement, result.BridgeReceipt)
}

func recoverAttempt(ctx context.Context, db *sql.DB, config protocol.Config) error {
	encoded, err := os.ReadFile(config.IntentPath)
	if err != nil {
		return fmt.Errorf("load synthetic send intent: %w", err)
	}
	var intent protocol.Intent
	if err := json.Unmarshal(encoded, &intent); err != nil {
		return err
	}
	if intent.CoreRequestID != config.Command.RequestID || intent.InternalKeyID != config.Command.APIKeyID || intent.Request != *config.Command.BridgeRequest {
		return errors.New("recovery fixture does not match persisted intent")
	}
	receipt, err := custombridge.NewReceiptRepository(db).Get(ctx, intent.CoreRequestID, intent.InternalKeyID)
	state := "settled"
	if errors.Is(err, sql.ErrNoRows) {
		state = "pending_review"
	} else if err != nil {
		return fmt.Errorf("read receipt in fresh process: %w", err)
	}
	if err := emit(protocol.Event{Stage: "recovered", State: state, Receipt: receipt, Intent: &intent}); err != nil {
		return err
	}
	if !config.Reconcile {
		return nil
	}
	// 显式对账只重试已知的计费命令；恢复分支不会调用 Provider。
	result, err := repository.NewUsageBillingRepository(nil, db).Apply(ctx, &config.Command)
	if err != nil {
		return fmt.Errorf("reconcile real usage billing: %w", err)
	}
	return emit(protocol.Event{Stage: "reconciled", Applied: result.Applied, Receipt: result.BridgeReceipt})
}

func emit(event protocol.Event) error {
	event.PID = os.Getpid()
	return json.NewEncoder(os.Stdout).Encode(event)
}

func waitForRelease(ctx context.Context, path string) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func persistJSON(path string, value any) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".bridge-crash-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := json.NewEncoder(file).Encode(value); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
