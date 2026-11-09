package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/custom/quality"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestQualityCalibrateRequiresLabels(t *testing.T) {
	var output bytes.Buffer
	for _, input := range []string{`{"schema_version":1,"revision":"v1"}`, `{"schema_version":1,"revision":"v1","calibration":{"options":{"dataset_origin":"human"},"samples":[]}}`, strings.Repeat(" ", quality.MaxBaselineBytes+1)} {
		if err := run(strings.NewReader(input), &output); err == nil {
			t.Fatal("unlabeled input reported calibrated")
		}
	}
}

func TestQualityCalibrateReportAndOutputFailure(t *testing.T) {
	file := quality.BaselineFile{SchemaVersion: 1, Revision: "cli-fixture-v1", Calibration: &quality.CalibrationSet{Options: quality.CalibrationOptions{DatasetOrigin: "synthetic"}}}
	for group := 0; group < 3; group++ {
		for i := 0; i < 10; i++ {
			score, verdict := 0, "fail"
			if i < 5 {
				score, verdict = 100, "pass"
			}
			split := "holdout"
			if group == 0 {
				split = "train"
			}
			file.Calibration.Samples = append(file.Calibration.Samples, quality.LabeledSample{Sample: quality.Sample{
				ResultID: int64(group*10 + i + 1), TaskID: int64(group + 1), Kind: quality.KindQuiz, Model: fmt.Sprint("model-", group), QuestionID: fmt.Sprint("q-", group), Prompt: fmt.Sprint("question ", group), Expected: fmt.Sprint("answer ", group), Response: fmt.Sprint("reply ", group, " item ", i), Verdict: verdict, Status: "success",
			}, Split: split, LabelSource: "synthetic", Labeler: "fixture", HumanScore: &score})
		}
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = run(bytes.NewReader(data), &output); err != nil {
		t.Fatal(err)
	}
	var report quality.CalibrationReport
	if err = json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.InputSamples != 30 || report.BaselineSHA256 == "" || report.DataDigest == "" || report.CoveredReferences != 0 || report.ProductionData != "Unknown" {
		t.Fatalf("bad report: %+v", report)
	}
	if err = run(bytes.NewReader(data), failingWriter{}); err != io.ErrClosedPipe {
		t.Fatalf("output failure hidden: %v", err)
	}
}
