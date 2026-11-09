package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	_ "github.com/lib/pq"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("bridge-schema", flag.ContinueOnError)
	apply := flags.Bool("apply", false, "explicitly apply Core bridge receipt and ingress schemas")
	applyTelemetry := flags.Bool("apply-telemetry", false, "explicitly apply only Core bridge telemetry schema")
	dsnFile := flags.String("dsn-file", "", "file containing the isolated Core database DSN")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected bridge-schema arguments")
	}
	if *apply && *applyTelemetry {
		return errors.New("--apply and --apply-telemetry are mutually exclusive")
	}
	if !*apply && !*applyTelemetry {
		fmt.Fprintln(os.Stdout, "No changes applied; use --apply or --apply-telemetry with BRIDGE_CORE_DSN or --dsn-file.")
		return nil
	}
	dsn := os.Getenv("BRIDGE_CORE_DSN")
	if *dsnFile != "" {
		raw, err := os.ReadFile(*dsnFile)
		if err != nil {
			return errors.New("cannot read bridge database DSN file")
		}
		dsn = strings.TrimSpace(string(raw))
	}
	if strings.TrimSpace(dsn) == "" {
		return errors.New("bridge database DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return errors.New("cannot open bridge database")
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return errors.New("cannot connect to bridge database")
	}
	if *applyTelemetry {
		if err := custombridge.ApplyTelemetrySchema(ctx, db); err != nil {
			return errors.New("bridge telemetry schema application failed")
		}
		fmt.Fprintln(os.Stdout, "Core bridge telemetry schema applied.")
		return nil
	}
	if err := custombridge.ApplyReceiptSchema(ctx, db); err != nil {
		return errors.New("bridge receipt schema application failed")
	}
	if err := custombridge.ApplyForwardSchema(ctx, db); err != nil {
		return errors.New("bridge ingress schema application failed")
	}
	fmt.Fprintln(os.Stdout, "Core bridge receipt and ingress schemas applied.")
	return nil
}
