// Package quality evaluates saved monitoring results without external services.
package quality

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	ScorerRevision   = "offline-grid32-v1"
	TemplateRevision = "explicit-grid-v1"
	TemplateSource   = "operator-authored-grid-v1"
	KindCharArt      = "char_art"
	KindQuiz         = "quiz"
	MaxResponseBytes = 16 * 1024
	MaxRows          = 128
	MaxColumns       = 128
)

type Sample struct {
	ResultID   int64  `json:"result_id"`
	TaskID     int64  `json:"task_id"`
	Kind       string `json:"kind"`
	Model      string `json:"model"`
	QuestionID string `json:"question_id"`
	Prompt     string `json:"prompt"`
	Response   string `json:"response"`
	Expected   string `json:"expected"`
	Verdict    string `json:"verdict"`
	Status     string `json:"status"`
}

type Components struct {
	Dice       float64 `json:"dice"`
	Projection float64 `json:"projection"`
	Layout     float64 `json:"layout"`
}

type Result struct {
	SourceResultID   int64      `json:"source_result_id"`
	SourceSHA256     string     `json:"source_sha256"`
	Kind             string     `json:"kind"`
	QuestionID       string     `json:"question_id"`
	QuestionRef      string     `json:"question_ref"`
	Score            int        `json:"score"`
	Confidence       int        `json:"confidence"`
	Reason           string     `json:"reason"`
	ScorerRevision   string     `json:"scorer_revision"`
	BaselineRevision string     `json:"baseline_revision"`
	TemplateID       string     `json:"template_id,omitempty"`
	Style            string     `json:"style,omitempty"`
	Components       Components `json:"components"`
}

type Reference struct {
	Median             float64 `json:"median"`
	MAD                float64 `json:"mad"`
	CalibratedCoverage bool    `json:"calibrated_coverage"`
}

// QuestionRef binds exact source text, not a normalized or inferred question.
// Char-art has no native question ID; quiz IDs alone do not survive edits.
func QuestionRef(kind, questionID, prompt, expected string) string {
	if kind == KindCharArt {
		questionID, expected = "", ""
	}
	encoded, _ := json.Marshal([5]string{"quality-question-v1", kind, questionID, prompt, expected})
	return digest(encoded)
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

type Scorer struct{ baseline *Baseline }

// NewScorer shares an immutable baseline and is safe for concurrent use.
// A nil baseline deliberately produces no calibrated glyph scores.
func NewScorer(b *Baseline) *Scorer { return &Scorer{baseline: b} }

func (s *Scorer) Score(sample Sample) Result {
	r := Result{
		SourceResultID: sample.ResultID,
		Kind:           sample.Kind, QuestionID: sample.QuestionID,
		ScorerRevision: ScorerRevision,
	}
	if s != nil && s.baseline != nil {
		r.BaselineRevision = s.baseline.revision
	}
	if len(sample.Prompt) > MaxResponseBytes || len(sample.Expected) > MaxResponseBytes || len(sample.Model) > 256 || len(sample.QuestionID) > 256 || len(sample.Kind) > 256 {
		r.Reason = "input_limit"
		return r
	}
	r.QuestionRef = QuestionRef(sample.Kind, sample.QuestionID, sample.Prompt, sample.Expected)
	if len(sample.Response) > MaxResponseBytes {
		r.Reason = "input_limit"
		return r
	}
	r.SourceSHA256 = digest([]byte(sample.Response))
	if sample.Status != "success" {
		r.Reason = "request_not_successful"
		return r
	}
	switch sample.Kind {
	case KindQuiz:
		if sample.Expected == "" || (sample.Verdict != "pass" && sample.Verdict != "fail") {
			r.Reason = "quiz_missing_verdict_or_expected"
			return r
		}
		if sample.Verdict == "pass" {
			r.Score = 100
		}
		r.Confidence, r.Reason, r.Style = 40, "native_quiz_verdict_uncalibrated", "quiz"
	case KindCharArt:
		if s == nil || s.baseline == nil {
			r.Reason = "baseline_missing"
			return r
		}
		templates := s.baseline.templates[r.QuestionRef]
		if len(templates) == 0 {
			r.Reason = "template_missing"
			return r
		}
		grid, reason := normalize(sample.Response, templates[0].target)
		if reason != "" {
			r.Reason = reason
			return r
		}
		bitmap := render(grid)
		best := -1.0
		for _, template := range templates {
			parts, score := compare(bitmap, template.bitmap, grid.aspect(), template.aspect)
			if score > best {
				best, r.Components, r.TemplateID = score, parts, template.id
			}
		}
		r.Score = int(best*100 + 0.5)
		r.Confidence, r.Reason, r.Style = 40, "glyph_uncalibrated", grid.style
	default:
		r.Reason = "unsupported_kind"
		return r
	}
	if s != nil && s.baseline != nil && s.baseline.covered(sample.Kind, r.QuestionRef, sample.Model, r.Style) {
		r.Confidence, r.Reason = 90, "human_holdout_covered"
	}
	return r
}
