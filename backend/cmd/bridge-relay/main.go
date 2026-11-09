package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/lib/pq"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "bridge-relay:", safeError(err))
		os.Exit(1)
	}
}

type relayStageError struct {
	stage string
	err   error
}

func (e *relayStageError) Error() string { return e.stage + " failed" }
func (e *relayStageError) Unwrap() error { return e.err }

func safeError(err error) string {
	stage := "operation"
	var staged *relayStageError
	if errors.As(err, &staged) {
		stage = staged.stage
	}
	code := "unavailable"
	var pg *pq.Error
	switch {
	case errors.Is(err, custombridge.ErrConsumerBinding):
		code = "identity_mismatch"
	case errors.Is(err, custombridge.ErrInboxConflict):
		code = "payload_conflict"
	case errors.Is(err, custombridge.ErrReceiptEnvelope):
		code = "invalid_receipt"
	case errors.Is(err, custombridge.ErrDeliveryLease):
		code = "lease_lost"
	case errors.Is(err, context.DeadlineExceeded):
		code = "timeout"
	case errors.As(err, &pg):
		code = "sqlstate_" + string(pg.Code)
	}
	return stage + ": " + code
}

func run(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("bridge-relay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	continuous := flags.Bool("run", false, "持续排空已提交回执")
	once := flags.Bool("once", false, "处理至多 batch 条当前可领取回执")
	applySource := flags.Bool("apply-source-schema", false, "显式应用源库迁移 002，要求已有 001")
	applySink := flags.Bool("apply-sink-schema", false, "显式应用独立目标库迁移 003")
	replay := flags.String("replay", "", "显式重放死信事件 UUID")
	reason := flags.String("reason", "", "死信重放审计原因")
	sourceID := flags.String("source", "", "稳定 Core 来源身份")
	consumerID := flags.String("consumer", "", "固定目标身份的消费者名称")
	batch := flags.Int("batch", 32, "单轮处理上限")
	poll := flags.Duration("poll", time.Second, "持续模式轮询间隔")
	lease := flags.Duration("lease", 30*time.Second, "单条事件租约")
	timeout := flags.Duration("delivery-timeout", 10*time.Second, "单条投递总超时，必须短于租约")
	maxAttempts := flags.Int("max-attempts", 8, "每次人工重放前的尝试上限")
	parseErr := flags.Parse(args)
	if errors.Is(parseErr, flag.ErrHelp) {
		flags.SetOutput(output)
		flags.PrintDefaults()
		return nil
	}
	if parseErr != nil || flags.NArg() != 0 {
		return errors.New("invalid command arguments")
	}
	if !*continuous && !*once && !*applySource && !*applySink && *replay == "" {
		_, err := fmt.Fprintln(output, "bridge-relay disabled; explicitly select --run, --once or a schema action")
		return err
	}
	if (*continuous && *once) || *batch < 1 || *batch > 1000 || *poll < time.Millisecond ||
		*timeout < time.Millisecond || *timeout >= *lease || (*replay != "" && (*continuous || *once)) {
		return errors.New("invalid relay action or limits")
	}
	work := *continuous || *once || *replay != ""
	var sourceDB, sinkDB *sql.DB
	var err error
	if *applySource || work {
		sourceDB, err = openRelayDB(ctx, "SOURCE")
		if err != nil {
			return &relayStageError{"source connection", err}
		}
		defer func() { _ = sourceDB.Close() }()
	}
	if *applySink || work {
		sinkDB, err = openRelayDB(ctx, "SINK")
		if err != nil {
			return &relayStageError{"sink connection", err}
		}
		defer func() { _ = sinkDB.Close() }()
	}
	if *applySource {
		if err := custombridge.ApplyRelaySchema(ctx, sourceDB); err != nil {
			return &relayStageError{"source migration", err}
		}
	}
	if *applySink {
		if err := custombridge.ApplyInboxSchema(ctx, sinkDB); err != nil {
			return &relayStageError{"sink migration", err}
		}
	}
	if !work {
		return nil
	}
	sink, err := custombridge.NewInboxRepository(ctx, sinkDB)
	if err != nil {
		return &relayStageError{"sink identity", err}
	}
	config := custombridge.DefaultRelayConfig(custombridge.ConsumerBinding{
		SourceID: *sourceID, ConsumerID: *consumerID, SinkID: sink.Identity(),
	})
	config.LeaseDuration, config.MaxAttempts = *lease, *maxAttempts
	source, err := custombridge.NewRelayRepository(sourceDB, config)
	if err != nil {
		return &relayStageError{"configuration", err}
	}
	if err := source.RegisterConsumer(ctx); err != nil {
		return &relayStageError{"consumer registration", err}
	}
	if *replay != "" {
		return source.Replay(ctx, *replay, *reason)
	}
	relay := &custombridge.Relay{Source: source, Sink: sink}
	for {
		var roundErr error
		for n := 0; n < *batch; n++ {
			deliveryCtx, cancel := context.WithTimeout(ctx, *timeout)
			result, deliveryErr := relay.DeliverOnce(deliveryCtx)
			cancel()
			if deliveryErr != nil {
				roundErr = deliveryErr
				_, _ = fmt.Fprintln(output, safeError(&relayStageError{"delivery", deliveryErr}))
				if errors.Is(deliveryErr, custombridge.ErrConsumerBinding) {
					return deliveryErr
				}
				if result.EventID == "" {
					break
				}
			}
			if result.EventID == "" {
				break
			}
		}
		if !*continuous {
			return roundErr
		}
		timer := time.NewTimer(*poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func openRelayDB(ctx context.Context, side string) (*sql.DB, error) {
	dsn, err := loadRelayDSN(side)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, errors.New("invalid DSN")
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, &relayStageError{"database ping", err}
	}
	return db, nil
}

func loadRelayDSN(side string) (string, error) {
	name := "BRIDGE_RELAY_" + side + "_DSN"
	dsn, file := os.Getenv(name), os.Getenv(name+"_FILE")
	if (dsn == "") == (file == "") {
		return "", errors.New("set exactly one DSN environment variable or file")
	}
	if file != "" {
		root, err := os.OpenRoot(filepath.Dir(file))
		if err != nil {
			return "", errors.New("open DSN directory failed")
		}
		defer func() { _ = root.Close() }()
		content, err := root.ReadFile(filepath.Base(file))
		if err != nil {
			return "", errors.New("read DSN file failed")
		}
		dsn = strings.TrimSpace(string(content))
	}
	if dsn == "" {
		return "", errors.New("empty DSN")
	}
	return dsn, nil
}
