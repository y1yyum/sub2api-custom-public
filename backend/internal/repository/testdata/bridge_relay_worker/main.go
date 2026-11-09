package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	_ "github.com/lib/pq"
)

type config struct {
	SourceDSN string
	SinkDSN   string
	Relay     custombridge.RelayConfig
	Stage     string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "relay fixture failed")
		os.Exit(1)
	}
}

func run() error {
	var cfg config
	if err := json.NewDecoder(os.Stdin).Decode(&cfg); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sourceDB, err := sql.Open("postgres", cfg.SourceDSN)
	if err != nil {
		return err
	}
	defer func() { _ = sourceDB.Close() }()
	sinkDB, err := sql.Open("postgres", cfg.SinkDSN)
	if err != nil {
		return err
	}
	defer func() { _ = sinkDB.Close() }()
	sourceDB.SetMaxOpenConns(1)
	sinkDB.SetMaxOpenConns(1)
	source, err := custombridge.NewRelayRepository(sourceDB, cfg.Relay)
	if err != nil {
		return err
	}
	sink, err := custombridge.NewInboxRepository(ctx, sinkDB)
	if err != nil {
		return err
	}
	if sink.Identity() != cfg.Relay.Binding.SinkID {
		return custombridge.ErrConsumerBinding
	}
	if cfg.Stage == "recover" {
		result, err := (&custombridge.Relay{Source: source, Sink: sink}).DeliverOnce(ctx)
		if err != nil || !result.Delivered {
			return errors.New("recovery did not deliver")
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"stage": "recovered", "pid": os.Getpid(), "result": result})
	}
	delivery, err := source.Claim(ctx)
	if err != nil || delivery == nil {
		return errors.New("fixture could not claim")
	}
	if cfg.Stage == "sink_committed" {
		if _, err := sink.Consume(ctx, delivery.Envelope); err != nil {
			return err
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"stage": cfg.Stage, "pid": os.Getpid(), "delivery": delivery,
	}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}
