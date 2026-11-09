package quality

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxBaselineBytes = 2 * 1024 * 1024

type Template struct {
	ID          string `json:"id"`
	QuestionRef string `json:"question_ref"`
	Target      string `json:"target"`
	Grid        string `json:"grid"`
}

type BaselineFile struct {
	SchemaVersion int             `json:"schema_version"`
	Revision      string          `json:"revision"`
	Templates     []Template      `json:"templates"`
	Calibration   *CalibrationSet `json:"calibration,omitempty"`
}

type compiledTemplate struct {
	id, target string
	bitmap     bitmap
	aspect     float64
}

type Baseline struct {
	revision   string
	digest     string
	templates  map[string][]compiledTemplate
	references map[referenceKey]Reference
	styles     map[styleKey]bool
	report     CalibrationReport
}

type referenceKey struct{ kind, question, model string }
type styleKey struct {
	referenceKey
	style string
}

// ParseBaseline compiles only explicit templates. JSON duplicate keys (including
// case aliases), unknown fields and malformed calibration evidence are errors.
func ParseBaseline(data []byte) (*Baseline, error) {
	if len(data) == 0 || len(data) > MaxBaselineBytes || !utf8.Valid(data) {
		return nil, errors.New("quality: invalid baseline size or encoding")
	}
	if err := uniqueJSON(data); err != nil {
		return nil, err
	}
	var file BaselineFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("quality: invalid baseline: %w", err)
	}
	if file.SchemaVersion != 1 || strings.TrimSpace(file.Revision) != file.Revision || file.Revision == "" || len(file.Revision) > 128 || len(file.Templates) > 256 {
		return nil, errors.New("quality: unsupported baseline version or template count")
	}
	b := &Baseline{revision: file.Revision, digest: digest(data), templates: make(map[string][]compiledTemplate), references: make(map[referenceKey]Reference), styles: make(map[styleKey]bool)}
	ids := make(map[string]bool)
	for _, template := range file.Templates {
		_, size := utf8.DecodeRuneInString(template.Target)
		target, _ := utf8.DecodeRuneInString(template.Target)
		if template.ID == "" || len(template.ID) > 128 || ids[template.ID] || !validRef(template.QuestionRef) || size != len(template.Target) || !unicode.Is(unicode.Han, target) {
			return nil, errors.New("quality: invalid template identity or target")
		}
		ids[template.ID] = true
		g, reason := normalize(template.Grid, template.Target)
		if reason != "" {
			return nil, fmt.Errorf("quality: invalid template grid: %s", reason)
		}
		prior := b.templates[template.QuestionRef]
		if len(prior) >= 8 || len(prior) > 0 && prior[0].target != template.Target {
			return nil, errors.New("quality: conflicting or excessive question templates")
		}
		b.templates[template.QuestionRef] = append(prior, compiledTemplate{id: template.ID, target: template.Target, bitmap: render(g), aspect: g.aspect()})
	}
	b.report = CalibrationReport{ScorerRevision: ScorerRevision, TemplateRevision: TemplateRevision, TemplateSource: TemplateSource, BaselineRevision: b.revision, BaselineSHA256: b.digest, ProductionData: "Unknown"}
	if file.Calibration != nil {
		report, err := Calibrate(b, file.Calibration.Samples, file.Calibration.Options)
		if err != nil {
			return nil, err
		}
		b.report = report
		for _, coverage := range report.Coverage {
			if !coverage.CalibratedCoverage {
				continue
			}
			key := referenceKey{coverage.Kind, coverage.QuestionRef, coverage.Model}
			// Each reference has one style. Mixing incomparable styles is neutral.
			if _, exists := b.references[key]; exists {
				return nil, errors.New("quality: multiple calibrated styles for one reference")
			}
			b.references[key] = Reference{Median: coverage.Median, MAD: coverage.MAD, CalibratedCoverage: true}
			b.styles[styleKey{key, coverage.Style}] = true
		}
	}
	return b, nil
}

func validRef(ref string) bool {
	decoded, err := hex.DecodeString(ref)
	return err == nil && len(decoded) == 32 && strings.ToLower(ref) == ref
}

func (b *Baseline) Revision() string {
	if b == nil {
		return ""
	}
	return b.revision
}
func (b *Baseline) Digest() string {
	if b == nil {
		return ""
	}
	return b.digest
}

func (b *Baseline) Reference(kind, questionRef, model string) Reference {
	if b == nil {
		return Reference{}
	}
	return b.references[referenceKey{kind, questionRef, model}]
}

func (b *Baseline) covered(kind, question, model, style string) bool {
	return b.styles[styleKey{referenceKey{kind, question, model}, style}]
}

// Report returns an independent value; mutating it cannot change future scores.
func (b *Baseline) Report() CalibrationReport {
	if b == nil {
		return CalibrationReport{ProductionData: "Unknown"}
	}
	r := b.report
	r.Coverage = append([]Coverage(nil), r.Coverage...)
	r.Metrics = append([]CalibrationMetric(nil), r.Metrics...)
	for i := range r.Metrics {
		if r.Metrics[i].RankCorrelation != nil {
			value := *r.Metrics[i].RankCorrelation
			r.Metrics[i].RankCorrelation = &value
		}
		if r.Metrics[i].FalsePositiveRate != nil {
			value := *r.Metrics[i].FalsePositiveRate
			r.Metrics[i].FalsePositiveRate = &value
		}
	}
	return r
}

func uniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	tokens := 0
	var read func(int) error
	read = func(depth int) error {
		if depth > 16 {
			return errors.New("quality: JSON nesting limit")
		}
		token, err := decoder.Token()
		tokens++
		if tokens > 100000 {
			return errors.New("quality: JSON token limit")
		}
		if err != nil {
			return errors.New("quality: invalid JSON")
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := make(map[string]bool)
			for decoder.More() {
				token, err = decoder.Token()
				if err != nil {
					return errors.New("quality: invalid JSON object")
				}
				key, ok := token.(string)
				if !ok {
					return errors.New("quality: invalid JSON key")
				}
				if key != strings.ToLower(key) {
					return errors.New("quality: JSON keys must use exact lowercase tags")
				}
				if keys[key] {
					return errors.New("quality: duplicate JSON key")
				}
				keys[key] = true
				if err := read(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			count := 0
			for decoder.More() {
				count++
				if count > MaxCalibrationSamples {
					return errors.New("quality: JSON array limit")
				}
				if err := read(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("quality: invalid JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := read(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("quality: trailing JSON")
	}
	return nil
}
