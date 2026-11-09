package quality

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// These invented labels test the admission policy, not real-world calibration.
func calibrationFixture(t testing.TB, origin string) (*Baseline, []LabeledSample) {
	t.Helper()
	b, err := ParseBaseline([]byte(`{"schema_version":1,"revision":"policy-fixture-v1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var samples []LabeledSample
	for group := 0; group < 3; group++ {
		for i := 0; i < 10; i++ {
			human := 0
			verdict := "fail"
			if i < 5 {
				human = 100
				verdict = "pass"
			}
			split := "holdout"
			if group == 0 {
				split = "train"
			}
			s := Sample{ResultID: int64(len(samples) + 1), TaskID: int64(group + 1), Kind: KindQuiz, Model: "model-" + intString(int64(group)), QuestionID: intString(int64(group + 1)), Prompt: "question-" + intString(int64(group)), Expected: "answer-" + intString(int64(group)), Response: "unique-response-" + intString(int64(group)) + "-" + intString(int64(i)), Verdict: verdict, Status: "success"}
			samples = append(samples, LabeledSample{Sample: s, Split: split, LabelSource: origin, Labeler: "invented policy fixture", HumanScore: &human})
		}
	}
	return b, samples
}

func baselineWithLabels(t testing.TB, samples []LabeledSample, origin string) *Baseline {
	t.Helper()
	file := BaselineFile{SchemaVersion: 1, Revision: "labeled-policy-fixture-v1", Calibration: &CalibrationSet{Options: CalibrationOptions{DatasetOrigin: origin}, Samples: samples}}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBaseline(data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestQualityCalibrationHumanHoldoutAndReference(t *testing.T) {
	base, samples := calibrationFixture(t, "human")
	r, err := Calibrate(base, samples, CalibrationOptions{DatasetOrigin: "human"})
	if err != nil {
		t.Fatal(err)
	}
	if r.CoveredReferences != 2 || r.TrainSamples != 10 || r.HoldoutSamples != 20 || len(r.Metrics) != 1 || r.Metrics[0].RankCorrelation == nil || *r.Metrics[0].RankCorrelation != 1 || r.Metrics[0].FalsePositiveRate == nil || *r.Metrics[0].FalsePositiveRate != 0 || r.HumanProvenanceVerified || r.ProductionData != "Unknown" {
		t.Fatalf("calibration: %+v", r)
	}
	b := baselineWithLabels(t, samples, "human")
	s := samples[10].Sample
	ref := QuestionRef(s.Kind, s.QuestionID, s.Prompt, s.Expected)
	if got := b.Reference(s.Kind, ref, s.Model); !got.CalibratedCoverage || got.Median != 100 || got.MAD != 0 {
		t.Fatalf("reference: %+v", got)
	}
	if got := NewScorer(b).Score(s); got.Confidence != 90 || got.Reason != "human_holdout_covered" {
		t.Fatalf("held-out evidence not usable: %+v", got)
	}
	for _, edited := range []Sample{samples[0].Sample, func() Sample { x := s; x.Model = "unseen"; return x }(), func() Sample { x := s; x.Prompt += "edited"; return x }(), func() Sample { x := s; x.Expected += "edited"; return x }()} {
		if NewScorer(b).Score(edited).Confidence > 40 {
			t.Fatal("training or unknown combination inherited holdout coverage")
		}
	}
	copyReport := b.Report()
	copyReport.Coverage[0].Median = -100
	*copyReport.Metrics[0].RankCorrelation = -1
	if b.Report().Coverage[0].Median < 0 || *b.Report().Metrics[0].RankCorrelation != 1 {
		t.Fatal("mutated immutable report")
	}
	reversed := append([]LabeledSample(nil), samples...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	again, err := Calibrate(base, reversed, CalibrationOptions{DatasetOrigin: "human"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, again) {
		t.Fatal("calibration changed with record order")
	}
}

func TestQualityCalibrationSyntheticRemainsNeutral(t *testing.T) {
	_, samples := calibrationFixture(t, "synthetic")
	b := baselineWithLabels(t, samples, "synthetic")
	r := b.Report()
	if r.CoveredReferences != 0 || r.Metrics[0].Reason != "synthetic_only" || r.ProductionData != "Unknown" {
		t.Fatalf("synthetic promoted: %+v", r)
	}
	if NewScorer(b).Score(samples[10].Sample).Confidence > 40 {
		t.Fatal("synthetic confidence exceeded cap")
	}
}

func TestQualityCalibrationRejectsLeakageAndDuplicates(t *testing.T) {
	base, original := calibrationFixture(t, "human")
	for name, change := range map[string]func([]LabeledSample){
		"missing human score":     func(s []LabeledSample) { s[0].HumanScore = nil },
		"no labeler":              func(s []LabeledSample) { s[0].Labeler = " " },
		"unasserted human origin": func(s []LabeledSample) { s[0].LabelSource = "" },
		"model cross split":       func(s []LabeledSample) { s[10].Sample.Model = s[0].Sample.Model },
		"question cross split":    func(s []LabeledSample) { s[10].Sample.QuestionID = s[0].Sample.QuestionID },
		"prompt alias cross split": func(s []LabeledSample) {
			s[10].Sample.Prompt = s[0].Sample.Prompt
			s[10].Sample.Expected = s[0].Sample.Expected
		},
		"task cross split":                  func(s []LabeledSample) { s[10].Sample.TaskID = s[0].Sample.TaskID },
		"duplicate ID":                      func(s []LabeledSample) { s[10].Sample.ResultID = s[0].Sample.ResultID },
		"duplicate within split":            func(s []LabeledSample) { s[1].Sample.Response = s[0].Sample.Response },
		"normalized duplicate within split": func(s []LabeledSample) { s[1].Sample.Response = "  " + s[0].Sample.Response + "\n" },
		"no holdout": func(s []LabeledSample) {
			for i := range s {
				s[i].Split = "train"
			}
		},
		"bad native verdict": func(s []LabeledSample) { s[0].Sample.Verdict = "true" },
	} {
		t.Run(name, func(t *testing.T) {
			samples := append([]LabeledSample(nil), original...)
			change(samples)
			if _, err := Calibrate(base, samples, CalibrationOptions{DatasetOrigin: "human"}); err == nil {
				t.Fatal("accepted bad evidence")
			}
		})
	}
}

func TestQualityCalibrationChangedAnswerCannotLaunderPrompt(t *testing.T) {
	base, original := calibrationFixture(t, "human")
	control, err := Calibrate(base, original, CalibrationOptions{DatasetOrigin: "human"})
	if err != nil || control.CoveredReferences != 2 || len(control.Metrics) != 1 || !control.Metrics[0].Accepted {
		t.Fatalf("independent control is not admissible: %+v, %v", control, err)
	}
	for _, prompt := range []string{original[0].Sample.Prompt, " \t" + original[0].Sample.Prompt + "\n"} {
		samples := append([]LabeledSample(nil), original...)
		for i := 10; i < 20; i++ {
			// The held-out group already has distinct IDs, models, tasks and
			// response content. Changing expected must not hide a seen prompt.
			samples[i].Sample.Prompt = prompt
		}
		report, err := Calibrate(base, samples, CalibrationOptions{DatasetOrigin: "human"})
		if err == nil || !strings.Contains(err.Error(), "leakage") {
			t.Fatalf("changed ID/expected laundered a seen prompt: %+v, %v", report, err)
		}
	}

	// A common short answer is not a question identity or a duplicate sample.
	shared := append([]LabeledSample(nil), original...)
	for i := range shared {
		shared[i].Sample.Expected = "13"
		if i%10 == 0 {
			shared[i].Sample.Response = "13"
		}
	}
	report, err := Calibrate(base, shared, CalibrationOptions{DatasetOrigin: "human"})
	if err != nil || report.CoveredReferences != 2 || !report.Metrics[0].Accepted {
		t.Fatalf("different prompts sharing an answer were conflated: %+v, %v", report, err)
	}
}

func TestQualityCalibrationWeakCellsAndFalsePositives(t *testing.T) {
	base, original := calibrationFixture(t, "human")
	for i := 15; i < 20; i++ {
		original[i].Sample.Verdict = "pass"
	}
	r, err := Calibrate(base, original, CalibrationOptions{DatasetOrigin: "human"})
	if err != nil {
		t.Fatal(err)
	}
	if r.CoveredReferences != 0 || r.Metrics[0].FalsePositiveRate == nil || *r.Metrics[0].FalsePositiveRate != .5 || r.Metrics[0].FalsePositives != 5 {
		t.Fatalf("false positives hidden: %+v", r)
	}
	base, original = calibrationFixture(t, "human")
	// Add a new model/question with only one item; pooled success cannot cover it.
	one := original[10]
	one.Sample.ResultID = 999
	one.Sample.TaskID = 999
	one.Sample.Model = "sparse-model"
	one.Sample.QuestionID = "sparse"
	one.Sample.Prompt = "sparse question"
	one.Sample.Response = "sparse reply"
	original = append(original, one)
	r, err = Calibrate(base, original, CalibrationOptions{DatasetOrigin: "human"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range r.Coverage {
		if cell.Model == "sparse-model" && cell.CalibratedCoverage {
			t.Fatal("sparse cell covered")
		}
	}
	noNegatives := evaluate(cohortKey{KindQuiz, "quiz"}, []scoredLabel{{label: LabeledSample{Split: "holdout", HumanScore: scorePointer(100)}, result: Result{Score: 100}}})
	if noNegatives.FalsePositiveRate != nil || noNegatives.RankCorrelation != nil || noNegatives.Accepted {
		t.Fatal("zero denominator fabricated a passing metric")
	}
}

func scorePointer(score int) *int { return &score }

func TestQualityCalibrationRanksAndMAD(t *testing.T) {
	for _, tc := range []struct {
		a, b []float64
		rho  float64
	}{
		{[]float64{1, 2, 2, 4}, []float64{10, 20, 20, 40}, 1},
		{[]float64{1, 2, 3}, []float64{3, 2, 1}, -1},
		{[]float64{1, 2, 3}, []float64{1, 3, 2}, .5},
	} {
		got := spearman(tc.a, tc.b)
		if got == nil || math.Abs(*got-tc.rho) > 1e-12 {
			t.Fatalf("Spearman: %v", got)
		}
	}
	if spearman([]float64{1, 1}, []float64{0, 1}) != nil {
		t.Fatal("constant ranks were assigned confidence")
	}
	median, mad := medianMAD([]float64{75, 85, 90, 95, 100})
	if median != 90 || mad != 5 {
		t.Fatalf("median/MAD: %v %v", median, mad)
	}
}

func TestQualityCalibrationNormalizedGlyphLeakage(t *testing.T) {
	b := makeBaseline(t)
	makeLabel := func(id int64, split, prompt, response, model string) LabeledSample {
		return LabeledSample{Sample: Sample{ResultID: id, TaskID: id, Kind: KindCharArt, Model: model, Prompt: prompt, Response: response, Status: "success"}, Split: split, HumanScore: scorePointer(100), Labeler: "fixture", LabelSource: "human"}
	}
	first := makeLabel(1, "train", "draw sun", sunGrid, "train-model")
	for name, response := range map[string]string{"fenced": "```\n" + sunGrid + "\n```", "fullwidth": strings.ReplaceAll(sunGrid, "#", "＃")} {
		t.Run(name, func(t *testing.T) {
			second := makeLabel(2, "holdout", "draw field", response, "test-model")
			if _, err := Calibrate(b, []LabeledSample{first, second}, CalibrationOptions{DatasetOrigin: "human"}); err == nil {
				t.Fatal("reformatted duplicate leaked")
			}
		})
	}
}

func TestQualityGlyphHoldoutCoverageAndStyleFence(t *testing.T) {
	// The fake human declarations below test policy mechanics only. No real
	// person/model/production data was used to establish calibration quality.
	grids := []string{sunGrid, fieldGrid, "#####\n#    \n#    \n#    \n#    "}
	targets := []string{"日", "田", "厂"}
	file := BaselineFile{SchemaVersion: 1, Revision: "glyph-policy-fixture-v1", Calibration: &CalibrationSet{Options: CalibrationOptions{DatasetOrigin: "human"}}}
	for group, original := range grids {
		prompt := "glyph question " + intString(int64(group))
		file.Templates = append(file.Templates, Template{ID: "glyph-" + intString(int64(group)), QuestionRef: QuestionRef(KindCharArt, "", prompt, ""), Target: targets[group], Grid: original})
		expanded := make([][]byte, 15)
		lines := strings.Split(original, "\n")
		for y := range expanded {
			expanded[y] = make([]byte, 15)
			for x := range expanded[y] {
				expanded[y][x] = lines[y/3][x/3]
			}
		}
		for i := 0; i < 10; i++ {
			var drawing []string
			if i < 5 {
				for y, row := range expanded {
					copyRow := append([]byte(nil), row...)
					if y == 1 {
						copyRow[i*3+1] = ' '
					}
					drawing = append(drawing, string(copyRow))
				}
			} else {
				for y := range 15 {
					row := make([]byte, 15)
					for x := range row {
						row[x] = ' '
						if (x*13+y*7+group*11+i*3)%(17+i) < 3+group || (x == 0 || x == 14) && (y == 0 || y == 14) {
							row[x] = '#'
						}
					}
					drawing = append(drawing, string(row))
				}
			}
			split := "holdout"
			if group == 0 {
				split = "train"
			}
			human := 0
			if i < 5 {
				human = 100
			}
			file.Calibration.Samples = append(file.Calibration.Samples, LabeledSample{Sample: Sample{ResultID: int64(group*10 + i + 1), TaskID: int64(group + 1), Kind: KindCharArt, Model: "glyph-model-" + intString(int64(group)), Prompt: prompt, Response: strings.Join(drawing, "\n"), Status: "success"}, Split: split, HumanScore: &human, Labeler: "invented policy fixture", LabelSource: "human"})
		}
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBaseline(data)
	if err != nil {
		t.Fatal(err)
	}
	if report := b.Report(); report.CoveredReferences != 2 {
		t.Fatalf("glyph mechanics not covered: %+v", report)
	}
	sample := file.Calibration.Samples[10].Sample
	if r := NewScorer(b).Score(sample); r.Confidence != 90 || r.Score < 80 {
		t.Fatalf("held-out glyph not covered: %+v", r)
	}
	sample.Response = strings.ReplaceAll(sample.Response, "#", "x")
	if r := NewScorer(b).Score(sample); r.Confidence > 40 || r.Score < 80 {
		t.Fatalf("unlabeled style inherited confidence: %+v", r)
	}
	if report := b.Report(); report.HumanProvenanceVerified || report.ProductionData != "Unknown" {
		t.Fatal("policy fixture became a production claim")
	}
}
