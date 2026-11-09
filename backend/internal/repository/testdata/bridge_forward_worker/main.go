package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	_ "github.com/lib/pq"
)

type fixture struct {
	DSN           string
	Mode          string
	ProviderURL   string
	CoreRequestID string
	Envelope      custombridge.ForwardEnvelope
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var config fixture
	if err := json.NewDecoder(os.Stdin).Decode(&config); err != nil {
		return errors.New("invalid synthetic forward fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", config.DSN)
	if err != nil {
		return errors.New("cannot open isolated database")
	}
	defer func() { _ = db.Close() }()
	repo := custombridge.NewForwardAttemptRepository(db)
	err = repo.Receive(ctx, config.Envelope, config.CoreRequestID)
	if config.Mode == "retry" {
		if !errors.Is(err, custombridge.ErrAttemptReplay) {
			return fmt.Errorf("retry must be refused: %w", err)
		}
		status, err := repo.Get(ctx, config.Envelope.InternalKeyID, config.Envelope.AttemptID)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"pid": os.Getpid(), "state": status.State, "core_request_id": status.CoreRequestID})
	}
	if err != nil {
		return err
	}
	if config.Mode != "after_receive" {
		if err := repo.ClaimSend(ctx, config.Envelope.InternalKeyID, config.Envelope.AttemptID); err != nil {
			return err
		}
	}
	if config.Mode == "after_provider" {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.ProviderURL, nil)
		if err != nil {
			return err
		}
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return errors.New("fake provider failed")
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"pid": os.Getpid(), "state": config.Mode}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}
