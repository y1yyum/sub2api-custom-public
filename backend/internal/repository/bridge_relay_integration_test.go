//go:build integration

package repository

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type bridgeRelayFixture struct {
	sourceDB, sinkDB   *sql.DB
	sourceDSN, sinkDSN string
	source             *custombridge.RelayRepository
	sink               *custombridge.InboxRepository
	config             custombridge.RelayConfig
}

func bridgeRelayDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	name := "bridge_relay_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := integrationDB.ExecContext(context.Background(), "CREATE DATABASE "+pq.QuoteIdentifier(name))
	require.NoError(t, err)
	u, err := url.Parse(integrationDSN)
	require.NoError(t, err)
	require.Contains(t, []string{"postgres", "postgresql"}, u.Scheme)
	u.Path, u.RawPath = "/"+name, ""
	dsn := u.String()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	require.NoError(t, db.PingContext(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := integrationDB.ExecContext(context.Background(), "DROP DATABASE "+pq.QuoteIdentifier(name)+" WITH (FORCE)")
		require.NoError(t, err)
	})
	return db, dsn
}

func newBridgeRelayFixture(t *testing.T) *bridgeRelayFixture {
	t.Helper()
	f := &bridgeRelayFixture{}
	f.sourceDB, f.sourceDSN = bridgeRelayDatabase(t)
	f.sinkDB, f.sinkDSN = bridgeRelayDatabase(t)
	ctx := context.Background()
	require.NoError(t, custombridge.ApplyReceiptSchema(ctx, f.sourceDB))
	require.NoError(t, custombridge.ApplyRelaySchema(ctx, f.sourceDB))
	require.NoError(t, custombridge.ApplyInboxSchema(ctx, f.sinkDB))
	var err error
	f.sink, err = custombridge.NewInboxRepository(ctx, f.sinkDB)
	require.NoError(t, err)
	f.config = custombridge.DefaultRelayConfig(custombridge.ConsumerBinding{
		ConsumerID: "shadow-a", SourceID: "core-" + uuid.NewString(), SinkID: f.sink.Identity(),
	})
	f.config.RetryMin, f.config.RetryMax = 2*time.Millisecond, 10*time.Millisecond
	f.source, err = custombridge.NewRelayRepository(f.sourceDB, f.config)
	require.NoError(t, err)
	require.NoError(t, f.source.RegisterConsumer(ctx))
	return f
}

func bridgeRelayReceipt(t *testing.T, db *sql.DB, transaction *sql.Tx) *custombridge.Receipt {
	t.Helper()
	tx := transaction
	if tx == nil {
		var err error
		tx, err = db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
	}
	receipt := &custombridge.Receipt{
		ReservationID: uuid.NewString(), AttemptID: uuid.NewString(), CoreRequestID: uuid.NewString(),
		InternalKeyID: 1, AccountID: 2, UserID: 3, RequestFingerprint: strings.Repeat("a", 64),
		RawUsage: json.RawMessage(`{"input_tokens":7,"output_tokens":2,"extension":{"meter":"untouched"}}`),
		Billing:  custombridge.ReceiptBilling{Model: "fixture-model", BalanceCost: 1.25},
	}
	require.NoError(t, custombridge.InsertReceipt(context.Background(), tx, receipt))
	if transaction == nil {
		require.NoError(t, tx.Commit())
	}
	return receipt
}

func bridgeRelayState(t *testing.T, f *bridgeRelayFixture, eventID string) string {
	t.Helper()
	var state string
	require.NoError(t, f.sourceDB.QueryRow(`SELECT state FROM bridge.receipt_delivery WHERE consumer_id = $1 AND receipt_id = $2`,
		f.config.Binding.ConsumerID, eventID).Scan(&state))
	return state
}

func bridgeRelayCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	// table 只来自本测试的常量。
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
	return count
}

func TestBridgeRelay(t *testing.T) {
	ctx := context.Background()
	t.Run("explicit_schema_and_checksum", func(t *testing.T) {
		source, _ := bridgeRelayDatabase(t)
		sink, _ := bridgeRelayDatabase(t)
		require.Error(t, custombridge.ApplyRelaySchema(ctx, source))
		require.NoError(t, custombridge.ApplyReceiptSchema(ctx, source))
		require.ErrorIs(t, custombridge.ApplyInboxSchema(ctx, source), custombridge.ErrConsumerBinding)
		for range 2 {
			require.NoError(t, custombridge.ApplyRelaySchema(ctx, source))
			require.NoError(t, custombridge.ApplyInboxSchema(ctx, sink))
		}
		_, err := source.Exec(`UPDATE bridge.relay_schema_migrations SET checksum = 'changed' WHERE version = '002'`)
		require.NoError(t, err)
		require.ErrorContains(t, custombridge.ApplyRelaySchema(ctx, source), "checksum mismatch")
	})
	t.Run("two_consumers_and_target_binding", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		receipt := bridgeRelayReceipt(t, f.sourceDB, nil)
		otherDB, _ := bridgeRelayDatabase(t)
		require.NoError(t, custombridge.ApplyInboxSchema(ctx, otherDB))
		otherSink, err := custombridge.NewInboxRepository(ctx, otherDB)
		require.NoError(t, err)
		otherConfig := f.config
		otherConfig.Binding.ConsumerID, otherConfig.Binding.SinkID = "shadow-b", otherSink.Identity()
		otherSource, err := custombridge.NewRelayRepository(f.sourceDB, otherConfig)
		require.NoError(t, err)
		require.NoError(t, otherSource.RegisterConsumer(ctx))
		for _, relay := range []*custombridge.Relay{{Source: f.source, Sink: f.sink}, {Source: otherSource, Sink: otherSink}} {
			result, err := relay.DeliverOnce(ctx)
			require.NoError(t, err)
			require.True(t, result.Delivered)
			require.Equal(t, receipt.ID, result.EventID)
		}
		require.Equal(t, 1, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.shadow_receipt_ledger"))
		require.Equal(t, 1, bridgeRelayCount(t, otherDB, "bridge_shadow.shadow_receipt_ledger"))
		wrongConfig := f.config
		wrongConfig.Binding.SinkID = otherSink.Identity()
		wrong, err := custombridge.NewRelayRepository(f.sourceDB, wrongConfig)
		require.NoError(t, err)
		require.ErrorIs(t, wrong.RegisterConsumer(ctx), custombridge.ErrConsumerBinding)
		_, err = wrong.Claim(ctx)
		require.ErrorIs(t, err, custombridge.ErrConsumerBinding)
		_, err = (&custombridge.Relay{Source: f.source, Sink: otherSink}).DeliverOnce(ctx)
		require.ErrorIs(t, err, custombridge.ErrConsumerBinding)
		wrongConfig = f.config
		wrongConfig.Binding.SourceID = "different-core"
		wrong, err = custombridge.NewRelayRepository(f.sourceDB, wrongConfig)
		require.NoError(t, err)
		require.ErrorIs(t, wrong.RegisterConsumer(ctx), custombridge.ErrConsumerBinding)
	})
	t.Run("concurrent_workers_one_valid_lease", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		bridgeRelayReceipt(t, f.sourceDB, nil)
		var wg sync.WaitGroup
		start := make(chan struct{})
		deliveries := make(chan *custombridge.Delivery, 4)
		errors := make(chan error, 4)
		for range 4 {
			wg.Go(func() {
				<-start
				delivery, err := f.source.Claim(ctx)
				deliveries <- delivery
				errors <- err
			})
		}
		close(start)
		wg.Wait()
		close(deliveries)
		close(errors)
		count := 0
		for err := range errors {
			require.NoError(t, err)
		}
		for delivery := range deliveries {
			if delivery != nil {
				count++
			}
		}
		require.Equal(t, 1, count)
	})
	t.Run("locked_row_does_not_block_other_ready_event", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		first := bridgeRelayReceipt(t, f.sourceDB, nil)
		second := bridgeRelayReceipt(t, f.sourceDB, nil)
		_, err := f.sourceDB.Exec(`INSERT INTO bridge.receipt_delivery(consumer_id, receipt_id)
			SELECT $1, receipt_id FROM bridge.receipt_outbox`, f.config.Binding.ConsumerID)
		require.NoError(t, err)
		tx, err := f.sourceDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		_, err = tx.Exec(`SELECT receipt_id FROM bridge.receipt_delivery WHERE receipt_id = $1 FOR UPDATE`, first.ID)
		require.NoError(t, err)
		claimCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		delivery, err := f.source.Claim(claimCtx)
		require.NoError(t, err)
		require.NotNil(t, delivery)
		require.Equal(t, second.ID, delivery.Lease.EventID)
	})
	t.Run("late_commit_is_not_skipped", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		tx, err := f.sourceDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		early := bridgeRelayReceipt(t, f.sourceDB, tx)
		late := bridgeRelayReceipt(t, f.sourceDB, nil)
		relay := &custombridge.Relay{Source: f.source, Sink: f.sink}
		result, err := relay.DeliverOnce(ctx)
		require.NoError(t, err)
		require.Equal(t, late.ID, result.EventID)
		require.NoError(t, tx.Commit())
		result, err = relay.DeliverOnce(ctx)
		require.NoError(t, err)
		require.Equal(t, early.ID, result.EventID)
		require.Equal(t, 2, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.shadow_receipt_ledger"))
	})
	t.Run("sink_rollback_and_schema_failure_preserve_event", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		receipt := bridgeRelayReceipt(t, f.sourceDB, nil)
		_, err := f.sinkDB.Exec(`CREATE FUNCTION bridge_shadow.fixture_reject() RETURNS trigger LANGUAGE plpgsql AS
			$$ BEGIN RAISE EXCEPTION 'fixture rollback'; END $$;
			CREATE TRIGGER fixture_reject BEFORE INSERT ON bridge_shadow.shadow_receipt_ledger
			FOR EACH ROW EXECUTE FUNCTION bridge_shadow.fixture_reject()`)
		require.NoError(t, err)
		relay := &custombridge.Relay{Source: f.source, Sink: f.sink}
		result, err := relay.DeliverOnce(ctx)
		require.Error(t, err)
		require.True(t, result.Failed)
		require.Equal(t, "pending", bridgeRelayState(t, f, receipt.ID))
		require.Zero(t, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.inbox"))
		require.Zero(t, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.shadow_receipt_ledger"))
		_, err = f.sinkDB.Exec(`DROP TRIGGER fixture_reject ON bridge_shadow.shadow_receipt_ledger;
			ALTER TABLE bridge_shadow.shadow_receipt_ledger RENAME TO unavailable_ledger`)
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			result, err = relay.DeliverOnce(ctx)
			return err != nil && result.EventID != ""
		}, time.Second, 5*time.Millisecond)
		require.Zero(t, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.inbox"))
		require.Equal(t, "pending", bridgeRelayState(t, f, receipt.ID))
		require.Equal(t, 1, bridgeRelayCount(t, f.sourceDB, "bridge.receipt_outbox"))
		_, err = f.sinkDB.Exec(`ALTER TABLE bridge_shadow.unavailable_ledger RENAME TO shadow_receipt_ledger`)
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			result, err = relay.DeliverOnce(ctx)
			return err == nil && result.Delivered
		}, time.Second, 5*time.Millisecond)
		require.Equal(t, "acked", bridgeRelayState(t, f, receipt.ID))
	})
	t.Run("ack_loss_stale_lease_and_conflict", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		f.config.LeaseDuration = time.Second
		var err error
		f.source, err = custombridge.NewRelayRepository(f.sourceDB, f.config)
		require.NoError(t, err)
		receipt := bridgeRelayReceipt(t, f.sourceDB, nil)
		first, err := f.source.Claim(ctx)
		require.NoError(t, err)
		require.NotNil(t, first)
		result, err := f.sink.Consume(ctx, first.Envelope)
		require.NoError(t, err)
		require.False(t, result.Duplicate)
		var second *custombridge.Delivery
		require.Eventually(t, func() bool {
			second, err = f.source.Claim(ctx)
			return err == nil && second != nil
		}, 5*time.Second, 10*time.Millisecond)
		require.Greater(t, second.Lease.Generation, first.Lease.Generation)
		require.NotEqual(t, second.Lease.Token, first.Lease.Token)
		require.ErrorIs(t, f.source.Ack(ctx, first.Lease), custombridge.ErrDeliveryLease)
		require.ErrorIs(t, f.source.Fail(ctx, first.Lease, "late_failure", true), custombridge.ErrDeliveryLease)
		result, err = f.sink.Consume(ctx, second.Envelope)
		require.NoError(t, err)
		require.True(t, result.Duplicate)
		require.NoError(t, f.source.Ack(ctx, second.Lease))
		require.Equal(t, "acked", bridgeRelayState(t, f, receipt.ID))
		require.Equal(t, 1, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.shadow_receipt_ledger"))
		var original map[string]any
		require.NoError(t, json.Unmarshal(second.Envelope.Receipt, &original))
		original["account_id"] = 999
		changed, err := json.Marshal(original)
		require.NoError(t, err)
		conflict, err := custombridge.NewReceiptEnvelope(f.config.Binding.SourceID, receipt.ID, changed)
		require.NoError(t, err)
		_, err = f.sink.Consume(ctx, conflict)
		require.ErrorIs(t, err, custombridge.ErrInboxConflict)
		require.Equal(t, 1, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.inbox_conflicts"))
		var stored string
		require.NoError(t, f.sinkDB.QueryRow(`SELECT receipt->>'account_id' FROM bridge_shadow.shadow_receipt_ledger`).Scan(&stored))
		require.Equal(t, "2", stored)
	})
	t.Run("content_conflict_dead_letters_without_overwrite", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		receipt := bridgeRelayReceipt(t, f.sourceDB, nil)
		delivery, err := f.source.Claim(ctx)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(delivery.Envelope.Receipt, &fields))
		fields["account_id"] = json.RawMessage(`999`)
		raw, err := json.Marshal(fields)
		require.NoError(t, err)
		different, err := custombridge.NewReceiptEnvelope(f.config.Binding.SourceID, receipt.ID, raw)
		require.NoError(t, err)
		_, err = f.sink.Consume(ctx, different)
		require.NoError(t, err)
		require.NoError(t, f.source.Fail(ctx, delivery.Lease, "ack_lost", false))
		relay := &custombridge.Relay{Source: f.source, Sink: f.sink}
		require.Eventually(t, func() bool {
			_, deliveryErr := relay.DeliverOnce(ctx)
			return errors.Is(deliveryErr, custombridge.ErrInboxConflict)
		}, time.Second, 5*time.Millisecond)
		require.Equal(t, "dead_letter", bridgeRelayState(t, f, receipt.ID))
		require.Equal(t, 1, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.inbox_conflicts"))
		var stored string
		require.NoError(t, f.sinkDB.QueryRow(`SELECT receipt->>'account_id' FROM bridge_shadow.shadow_receipt_ledger`).Scan(&stored))
		require.Equal(t, "999", stored)
		require.Equal(t, 1, bridgeRelayCount(t, f.sourceDB, "bridge.receipt_outbox"))
	})
	t.Run("bounded_dead_letter_and_explicit_replay", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		f.config.MaxAttempts = 1
		var err error
		f.source, err = custombridge.NewRelayRepository(f.sourceDB, f.config)
		require.NoError(t, err)
		receipt := bridgeRelayReceipt(t, f.sourceDB, nil)
		delivery, err := f.source.Claim(ctx)
		require.NoError(t, err)
		require.NoError(t, f.source.Fail(ctx, delivery.Lease, "sink_unavailable", false))
		require.Equal(t, "dead_letter", bridgeRelayState(t, f, receipt.ID))
		next, err := f.source.Claim(ctx)
		require.NoError(t, err)
		require.Nil(t, next)
		require.Error(t, f.source.Replay(ctx, receipt.ID, ""))
		require.NoError(t, f.source.Replay(ctx, receipt.ID, "target repaired"))
		result, err := (&custombridge.Relay{Source: f.source, Sink: f.sink}).DeliverOnce(ctx)
		require.NoError(t, err)
		require.True(t, result.Delivered)
		var attempts, total, replays, audited int
		require.NoError(t, f.sourceDB.QueryRow(`SELECT attempts, total_attempts, replay_count FROM bridge.receipt_delivery`).Scan(&attempts, &total, &replays))
		require.Equal(t, 1, attempts)
		require.Equal(t, 2, total)
		require.Equal(t, 1, replays)
		require.NoError(t, f.sourceDB.QueryRow(`SELECT COUNT(*) FROM bridge.receipt_delivery_audit WHERE action = 'replay' AND reason = 'target repaired'`).Scan(&audited))
		require.Equal(t, 1, audited)
		require.ErrorIs(t, f.source.Replay(ctx, receipt.ID, "already done"), custombridge.ErrDeliveryState)
	})
	t.Run("exhausted_crash_lease_becomes_dead_letter", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		f.config.MaxAttempts, f.config.LeaseDuration = 1, 30*time.Millisecond
		var err error
		f.source, err = custombridge.NewRelayRepository(f.sourceDB, f.config)
		require.NoError(t, err)
		receipt := bridgeRelayReceipt(t, f.sourceDB, nil)
		delivery, err := f.source.Claim(ctx)
		require.NoError(t, err)
		require.NotNil(t, delivery)
		require.Eventually(t, func() bool {
			_, claimErr := f.source.Claim(ctx)
			return claimErr == nil && bridgeRelayState(t, f, receipt.ID) == "dead_letter"
		}, 3*time.Second, 10*time.Millisecond)
	})
	t.Run("source_schema_absence_does_not_ack_or_delete", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		bridgeRelayReceipt(t, f.sourceDB, nil)
		_, err := f.sourceDB.Exec(`ALTER TABLE bridge.receipt_delivery RENAME TO unavailable_delivery`)
		require.NoError(t, err)
		_, err = f.source.Claim(ctx)
		require.Error(t, err)
		require.Equal(t, 1, bridgeRelayCount(t, f.sourceDB, "bridge.receipt_outbox"))
		require.Zero(t, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.inbox"))
	})
	t.Run("sink_identity_change_is_fenced", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		bridgeRelayReceipt(t, f.sourceDB, nil)
		_, err := f.sinkDB.Exec(`UPDATE bridge_shadow.identity SET sink_id = $1`, uuid.NewString())
		require.NoError(t, err)
		result, err := (&custombridge.Relay{Source: f.source, Sink: f.sink}).DeliverOnce(ctx)
		require.ErrorIs(t, err, custombridge.ErrConsumerBinding)
		require.True(t, result.Failed)
		require.Equal(t, "dead_letter", bridgeRelayState(t, f, result.EventID))
		require.Zero(t, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.inbox"))
	})
	t.Run("concurrent_sink_once_and_full_payload", func(t *testing.T) {
		f := newBridgeRelayFixture(t)
		bridgeRelayReceipt(t, f.sourceDB, nil)
		delivery, err := f.source.Claim(ctx)
		require.NoError(t, err)
		var wg sync.WaitGroup
		results := make(chan custombridge.InboxResult, 4)
		errs := make(chan error, 4)
		for range 4 {
			wg.Go(func() {
				result, err := f.sink.Consume(ctx, delivery.Envelope)
				results <- result
				errs <- err
			})
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		duplicates := 0
		for result := range results {
			if result.Duplicate {
				duplicates++
			}
		}
		require.Equal(t, 3, duplicates)
		var raw string
		require.NoError(t, f.sinkDB.QueryRow(`SELECT receipt_raw FROM bridge_shadow.shadow_receipt_ledger`).Scan(&raw))
		require.JSONEq(t, string(delivery.Envelope.Receipt), raw)
		require.Contains(t, raw, `"meter":"untouched"`)
		require.Contains(t, raw, `"schema_version":1`)
	})
}

func TestBridgeRelayProcessCrash(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("真实 SIGKILL 演练要求 Linux")
	}
	binary := filepath.Join(t.TempDir(), "bridge-relay-worker")
	buildCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-p", "1", "-o", binary, "./testdata/bridge_relay_worker")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build relay child: %s", output)
	for _, stage := range []string{"claimed", "sink_committed"} {
		t.Run(stage, func(t *testing.T) {
			f := newBridgeRelayFixture(t)
			f.config.LeaseDuration = 2 * time.Second
			receipt := bridgeRelayReceipt(t, f.sourceDB, nil)
			cfg := bridgeRelayWorkerConfig{SourceDSN: f.sourceDSN, SinkDSN: f.sinkDSN, Relay: f.config, Stage: stage}
			worker, event := startBridgeRelayWorker(t, binary, cfg)
			require.Equal(t, stage, event.Stage)
			require.Equal(t, worker.cmd.Process.Pid, event.PID)
			require.Equal(t, receipt.ID, event.Delivery.Envelope.EventID)
			require.Equal(t, "leased", bridgeRelayState(t, f, receipt.ID))
			if stage == "sink_committed" {
				require.Equal(t, 1, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.shadow_receipt_ledger"))
			} else {
				require.Zero(t, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.inbox"))
			}
			require.NoError(t, worker.cmd.Process.Signal(syscall.SIGKILL))
			waitErr := worker.cmd.Wait()
			worker.waited = true
			var exitErr *exec.ExitError
			require.ErrorAs(t, waitErr, &exitErr)
			status, ok := exitErr.Sys().(syscall.WaitStatus)
			require.True(t, ok)
			require.True(t, status.Signaled())
			require.Equal(t, syscall.SIGKILL, status.Signal())
			require.Empty(t, worker.stderr.String())
			require.Eventually(t, func() bool {
				var expired bool
				queryErr := f.sourceDB.QueryRow(`SELECT lease_until <= clock_timestamp() FROM bridge.receipt_delivery`).Scan(&expired)
				return queryErr == nil && expired
			}, 5*time.Second, 10*time.Millisecond)
			cfg.Stage = "recover"
			recovered, result := startBridgeRelayWorker(t, binary, cfg)
			require.Equal(t, "recovered", result.Stage)
			require.NotEqual(t, event.PID, result.PID)
			require.True(t, result.Result.Delivered)
			require.Equal(t, stage == "sink_committed", result.Result.Duplicate)
			require.NoError(t, recovered.cmd.Wait(), "recovery stderr=%s", recovered.stderr.String())
			recovered.waited = true
			require.Equal(t, "acked", bridgeRelayState(t, f, receipt.ID))
			require.Equal(t, 1, bridgeRelayCount(t, f.sourceDB, "bridge.receipt_outbox"))
			require.Equal(t, 1, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.inbox"))
			require.Equal(t, 1, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.shadow_receipt_ledger"))
			t.Logf("SIGKILL stage=%s killed_pid=%d recovered_pid=%d duplicate=%v", stage, event.PID, result.PID, result.Result.Duplicate)
		})
	}
}

type bridgeRelayWorkerConfig struct {
	SourceDSN string
	SinkDSN   string
	Relay     custombridge.RelayConfig
	Stage     string
}

type bridgeRelayWorkerEvent struct {
	Stage    string
	PID      int
	Delivery custombridge.Delivery
	Result   custombridge.DeliveryResult
}

type bridgeRelayWorker struct {
	cmd    *exec.Cmd
	stderr bytes.Buffer
	waited bool
}

func startBridgeRelayWorker(t *testing.T, binary string, config bridgeRelayWorkerConfig) (*bridgeRelayWorker, bridgeRelayWorkerEvent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	payload, err := json.Marshal(config)
	require.NoError(t, err)
	worker := &bridgeRelayWorker{cmd: exec.CommandContext(ctx, binary)}
	worker.cmd.Stdin = bytes.NewReader(payload)
	worker.cmd.Stderr = &worker.stderr
	stdout, err := worker.cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, worker.cmd.Start())
	t.Cleanup(func() {
		if !worker.waited {
			_ = worker.cmd.Process.Kill()
			_ = worker.cmd.Wait()
		}
	})
	scanner := bufio.NewScanner(stdout)
	events := make(chan []byte, 1)
	go func() {
		if scanner.Scan() {
			events <- bytes.Clone(scanner.Bytes())
		}
		close(events)
	}()
	select {
	case line, ok := <-events:
		require.True(t, ok, "child exited before barrier")
		var event bridgeRelayWorkerEvent
		require.NoError(t, json.Unmarshal(line, &event))
		return worker, event
	case <-ctx.Done():
		t.Fatal("child did not reach barrier")
		return nil, bridgeRelayWorkerEvent{}
	}
}

func TestBridgeRelayCommand(t *testing.T) {
	f := newBridgeRelayFixture(t)
	receipt := bridgeRelayReceipt(t, f.sourceDB, nil)
	binary := filepath.Join(t.TempDir(), "bridge-relay")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-p", "1", "-o", binary, "../../cmd/bridge-relay")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	command := exec.CommandContext(ctx, binary, "--once", "--source", f.config.Binding.SourceID, "--consumer", f.config.Binding.ConsumerID)
	command.Env = append(os.Environ(), "BRIDGE_RELAY_SOURCE_DSN="+f.sourceDSN, "BRIDGE_RELAY_SINK_DSN="+f.sinkDSN)
	output, err = command.CombinedOutput()
	require.NoError(t, err, "CLI output=%s", output)
	require.NotContains(t, string(output), f.sourceDSN)
	require.NotContains(t, string(output), f.sinkDSN)
	require.Equal(t, "acked", bridgeRelayState(t, f, receipt.ID))
	require.Equal(t, 1, bridgeRelayCount(t, f.sinkDB, "bridge_shadow.shadow_receipt_ledger"))
	wrongSink, wrongDSN := bridgeRelayDatabase(t)
	require.NoError(t, custombridge.ApplyInboxSchema(ctx, wrongSink))
	command = exec.CommandContext(ctx, binary, "--once", "--source", f.config.Binding.SourceID, "--consumer", f.config.Binding.ConsumerID)
	command.Env = append(os.Environ(), "BRIDGE_RELAY_SOURCE_DSN="+f.sourceDSN, "BRIDGE_RELAY_SINK_DSN="+wrongDSN)
	output, err = command.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "identity_mismatch")
	require.Zero(t, bridgeRelayCount(t, wrongSink, "bridge_shadow.inbox"))
	require.Equal(t, 1, bridgeRelayCount(t, f.sourceDB, "bridge.receipt_outbox"))
}
