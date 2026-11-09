package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Wei-Shaw/sub2api/internal/custom/quality"
)

func run(input io.Reader, output io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(input, quality.MaxBaselineBytes+1))
	if err != nil {
		return fmt.Errorf("read baseline: %w", err)
	}
	baseline, err := quality.ParseBaseline(data)
	if err != nil {
		return err
	}
	report := baseline.Report()
	if report.InputSamples == 0 {
		return fmt.Errorf("quality: explicit labels and holdout required")
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
