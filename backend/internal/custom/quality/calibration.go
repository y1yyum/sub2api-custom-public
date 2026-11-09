package quality

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
)

const (
	MaxCalibrationSamples = 1000
	CalibrationRevision   = "human-group-holdout-v1"
)

type CalibrationOptions struct {
	DatasetOrigin string `json:"dataset_origin"`
}
type CalibrationSet struct {
	Options CalibrationOptions `json:"options"`
	Samples []LabeledSample    `json:"samples"`
}
type LabeledSample struct {
	Sample      Sample `json:"sample"`
	Split       string `json:"split"`
	LabelSource string `json:"label_source"`
	Labeler     string `json:"labeler"`
	HumanScore  *int   `json:"human_score"`
}
type CalibrationMetric struct {
	Kind              string   `json:"kind"`
	Style             string   `json:"style"`
	TrainSamples      int      `json:"train_samples"`
	HoldoutSamples    int      `json:"holdout_samples"`
	GoodSamples       int      `json:"good_samples"`
	BadSamples        int      `json:"bad_samples"`
	RankCorrelation   *float64 `json:"rank_correlation"`
	FalsePositiveRate *float64 `json:"false_positive_rate"`
	FalsePositives    int      `json:"false_positives"`
	Accepted          bool     `json:"accepted"`
	Reason            string   `json:"reason"`
}
type Coverage struct {
	Kind               string  `json:"kind"`
	QuestionRef        string  `json:"question_ref"`
	Model              string  `json:"model"`
	Style              string  `json:"style"`
	GoodSamples        int     `json:"good_samples"`
	BadSamples         int     `json:"bad_samples"`
	Median             float64 `json:"median"`
	MAD                float64 `json:"mad"`
	CalibratedCoverage bool    `json:"calibrated_coverage"`
}
type CalibrationReport struct {
	CalibrationRevision     string              `json:"calibration_revision"`
	ScorerRevision          string              `json:"scorer_revision"`
	TemplateRevision        string              `json:"template_revision"`
	TemplateSource          string              `json:"template_source"`
	BaselineRevision        string              `json:"baseline_revision"`
	BaselineSHA256          string              `json:"baseline_sha256"`
	DatasetOrigin           string              `json:"dataset_origin"`
	HumanProvenanceVerified bool                `json:"human_provenance_verified"`
	ProductionData          string              `json:"production_data"`
	DataDigest              string              `json:"data_digest"`
	InputSamples            int                 `json:"input_samples"`
	TrainSamples            int                 `json:"train_samples"`
	HoldoutSamples          int                 `json:"holdout_samples"`
	CoveredReferences       int                 `json:"covered_references"`
	Metrics                 []CalibrationMetric `json:"metrics"`
	Coverage                []Coverage          `json:"coverage"`
}

type scoredLabel struct {
	label  LabeledSample
	result Result
}
type cohortKey struct{ kind, style string }

// Calibrate evaluates the frozen scorer; it never tunes on holdout labels.
// Good means human >=80; bad <=40; prediction >=80 is positive. Coverage needs
// >=5 good and >=5 bad per held-out reference, Spearman >=.6 and FPR <=.1.
// Models, question IDs/text, tasks and canonical outputs must be split-disjoint.
func Calibrate(b *Baseline, samples []LabeledSample, options CalibrationOptions) (CalibrationReport, error) {
	r := CalibrationReport{CalibrationRevision: CalibrationRevision, ScorerRevision: ScorerRevision, TemplateRevision: TemplateRevision, TemplateSource: TemplateSource, BaselineRevision: b.Revision(), BaselineSHA256: b.Digest(), DatasetOrigin: options.DatasetOrigin, ProductionData: "Unknown", InputSamples: len(samples)}
	if b == nil || len(samples) == 0 || len(samples) > MaxCalibrationSamples || (options.DatasetOrigin != "human" && options.DatasetOrigin != "synthetic") {
		return r, errors.New("quality: calibration needs an explicit origin and bounded labels")
	}
	rows := make([]scoredLabel, 0, len(samples))
	seenIDs := make(map[int64]bool)
	seenSamples := make(map[string]bool)
	identities := make(map[string]string)
	cohorts := make(map[cohortKey][]scoredLabel)
	cells := make(map[styleKey][]scoredLabel)
	for _, label := range samples {
		s := label.Sample
		if label.HumanScore == nil || *label.HumanScore < 0 || *label.HumanScore > 100 || strings.TrimSpace(label.Labeler) == "" || len(label.Labeler) > 256 || label.LabelSource != options.DatasetOrigin || (label.Split != "train" && label.Split != "holdout") || s.ResultID <= 0 || seenIDs[s.ResultID] || s.TaskID <= 0 || s.Model == "" || strings.TrimSpace(s.Model) != s.Model || len(s.Model) > 256 || s.Status != "success" || len(s.Prompt) == 0 || len(s.Prompt) > MaxResponseBytes || len(s.Expected) > MaxResponseBytes || len(s.Response) > MaxResponseBytes || len(s.QuestionID) > 256 {
			return r, errors.New("quality: invalid, unlabeled or duplicate calibration sample")
		}
		seenIDs[s.ResultID] = true
		if s.Kind != KindCharArt && s.Kind != KindQuiz || s.Kind == KindQuiz && (s.QuestionID == "" || s.Expected == "" || s.Verdict != "pass" && s.Verdict != "fail") {
			return r, errors.New("quality: invalid calibration kind or native verdict")
		}
		result := NewScorer(b).Score(s)
		if result.Reason == "template_missing" || result.Reason == "baseline_missing" {
			return r, errors.New("quality: calibration template missing")
		}
		style := result.Style
		if style == "" {
			style = "rejected"
			result.Style = style
		}
		duplicate := duplicateKey(s, b)
		canonicalSample := result.QuestionRef + ":" + duplicate
		if seenSamples[canonicalSample] {
			return r, errors.New("quality: duplicate calibration content")
		}
		seenSamples[canonicalSample] = true
		// Kind-independent model and native quiz ID checks prevent partition
		// laundering with edited prompts, renamed result IDs or other tasks.
		keys := []string{"model:" + s.Model, "question:" + result.QuestionRef, "task:" + intString(s.TaskID), "output:" + duplicate}
		keys = append(keys, "question-text:"+digest([]byte(strings.Join(strings.Fields(s.Prompt), " "))))
		if s.Kind == KindQuiz {
			keys = append(keys, "quiz-id:"+s.QuestionID)
		}
		if s.Kind == KindCharArt {
			keys = append(keys, "target:"+b.templates[result.QuestionRef][0].target)
		}
		for _, key := range keys {
			if prior, exists := identities[key]; exists && prior != label.Split {
				return r, errors.New("quality: train/holdout group or duplicate leakage")
			}
			identities[key] = label.Split
		}
		row := scoredLabel{label: label, result: result}
		rows = append(rows, row)
		ck := cohortKey{s.Kind, style}
		cohorts[ck] = append(cohorts[ck], row)
		if label.Split == "train" {
			r.TrainSamples++
		} else {
			r.HoldoutSamples++
			key := styleKey{referenceKey{s.Kind, result.QuestionRef, s.Model}, style}
			cells[key] = append(cells[key], row)
		}
	}
	if r.TrainSamples == 0 || r.HoldoutSamples == 0 {
		return r, errors.New("quality: both train and holdout are required")
	}
	// Sorting canonical records makes input ordering irrelevant to the digest.
	canonical := make([]string, len(rows))
	for i, row := range rows {
		encoded, _ := json.Marshal(row.label)
		canonical[i] = string(encoded)
	}
	sort.Strings(canonical)
	encoded, _ := json.Marshal(struct {
		Revision string
		Scorer   string
		Template string
		Baseline string
		Options  CalibrationOptions
		Records  []string
	}{CalibrationRevision, ScorerRevision, TemplateRevision, b.digest, options, canonical})
	r.DataDigest = digest(encoded)
	accepted := make(map[cohortKey]bool)
	for key, group := range cohorts {
		metric := evaluate(key, group)
		if options.DatasetOrigin == "synthetic" {
			metric.Accepted = false
			metric.Reason = "synthetic_only"
		}
		r.Metrics = append(r.Metrics, metric)
		accepted[key] = metric.Accepted
	}
	sort.Slice(r.Metrics, func(i, j int) bool {
		a, c := r.Metrics[i], r.Metrics[j]
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		return a.Style < c.Style
	})
	for key, group := range cells {
		coverage := Coverage{Kind: key.kind, QuestionRef: key.question, Model: key.model, Style: key.style}
		var good []float64
		for _, row := range group {
			if *row.label.HumanScore >= 80 {
				coverage.GoodSamples++
				good = append(good, float64(row.result.Score))
			}
			if *row.label.HumanScore <= 40 {
				coverage.BadSamples++
			}
		}
		coverage.Median, coverage.MAD = medianMAD(good)
		// A pooled metric cannot confer coverage on a weak model/question cell.
		metric := evaluate(cohortKey{key.kind, key.style}, group)
		coverage.CalibratedCoverage = accepted[cohortKey{key.kind, key.style}] && coverage.GoodSamples >= 5 && coverage.BadSamples >= 5 && metric.RankCorrelation != nil && *metric.RankCorrelation >= .6 && metric.FalsePositiveRate != nil && *metric.FalsePositiveRate <= .1
		if coverage.CalibratedCoverage {
			r.CoveredReferences++
		}
		r.Coverage = append(r.Coverage, coverage)
	}
	sort.Slice(r.Coverage, func(i, j int) bool {
		a, c := r.Coverage[i], r.Coverage[j]
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		if a.QuestionRef != c.QuestionRef {
			return a.QuestionRef < c.QuestionRef
		}
		if a.Model != c.Model {
			return a.Model < c.Model
		}
		return a.Style < c.Style
	})
	return r, nil
}

func intString(n int64) string { encoded, _ := json.Marshal(n); return string(encoded) }

func duplicateKey(s Sample, b *Baseline) string {
	if s.Kind == KindCharArt {
		templates := b.templates[QuestionRef(s.Kind, s.QuestionID, s.Prompt, s.Expected)]
		if len(templates) > 0 {
			g, reason := normalize(s.Response, templates[0].target)
			if reason == "" {
				encoded, _ := json.Marshal(render(g))
				return digest(encoded)
			}
		}
	}
	// Invalid grids and quiz replies still share exact normalized text identity.
	if s.Kind == KindQuiz {
		return digest([]byte(strings.Join(strings.Fields(s.Prompt), " ") + "\x00" + strings.Join(strings.Fields(s.Expected), " ") + "\x00" + strings.Join(strings.Fields(s.Response), " ")))
	}
	return digest([]byte(strings.Join(strings.Fields(s.Response), " ")))
}

func evaluate(key cohortKey, rows []scoredLabel) CalibrationMetric {
	m := CalibrationMetric{Kind: key.kind, Style: key.style, Reason: "insufficient_coverage"}
	trainGood, trainBad, falsePositives := 0, 0, 0
	models, questions := make(map[string]bool), make(map[string]bool)
	var predicted, human []float64
	for _, row := range rows {
		label := *row.label.HumanScore
		if row.label.Split == "train" {
			m.TrainSamples++
			if label >= 80 {
				trainGood++
			}
			if label <= 40 {
				trainBad++
			}
			continue
		}
		m.HoldoutSamples++
		models[row.label.Sample.Model], questions[row.result.QuestionRef] = true, true
		predicted = append(predicted, float64(row.result.Score))
		human = append(human, float64(label))
		if label >= 80 {
			m.GoodSamples++
		}
		if label <= 40 {
			m.BadSamples++
			if row.result.Score >= 80 {
				falsePositives++
			}
		}
	}
	m.RankCorrelation = spearman(predicted, human)
	if m.BadSamples > 0 {
		value := float64(falsePositives) / float64(m.BadSamples)
		m.FalsePositiveRate = &value
	}
	m.FalsePositives = falsePositives
	if trainGood >= 5 && trainBad >= 5 && m.GoodSamples >= 5 && m.BadSamples >= 5 && len(models) >= 2 && len(questions) >= 2 && m.RankCorrelation != nil && *m.RankCorrelation >= .6 && m.FalsePositiveRate != nil && *m.FalsePositiveRate <= .1 {
		m.Accepted, m.Reason = true, "human_holdout_passed"
	} else if m.RankCorrelation != nil && m.FalsePositiveRate != nil && (*m.RankCorrelation < .6 || *m.FalsePositiveRate > .1) {
		m.Reason = "holdout_quality_failed"
	}
	return m
}

func medianMAD(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	median := midpoint(copyValues)
	for i, value := range copyValues {
		copyValues[i] = math.Abs(value - median)
	}
	sort.Float64s(copyValues)
	return median, midpoint(copyValues)
}

func midpoint(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func spearman(a, b []float64) *float64 {
	if len(a) < 2 || len(a) != len(b) {
		return nil
	}
	x, y := ranks(a), ranks(b)
	mean := (float64(len(a)) + 1) / 2
	xy, xx, yy := 0.0, 0.0, 0.0
	for i := range x {
		dx, dy := x[i]-mean, y[i]-mean
		xy += dx * dy
		xx += dx * dx
		yy += dy * dy
	}
	if xx == 0 || yy == 0 {
		return nil
	}
	value := math.Max(-1, math.Min(1, xy/math.Sqrt(xx*yy)))
	return &value
}

func ranks(values []float64) []float64 {
	indices := make([]int, len(values))
	for i := range indices {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool { return values[indices[i]] < values[indices[j]] })
	out := make([]float64, len(values))
	for i := 0; i < len(indices); {
		j := i + 1
		for j < len(indices) && values[indices[j]] == values[indices[i]] {
			j++
		}
		average := float64(i+1+j) / 2
		for k := i; k < j; k++ {
			out[indices[k]] = average
		}
		i = j
	}
	return out
}
