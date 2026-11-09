package quality

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
)

const sunGrid = "#####\n#   #\n#####\n#   #\n#####"
const fieldGrid = "#####\n# # #\n#####\n# # #\n#####"

func makeBaseline(t testing.TB) *Baseline {
	t.Helper()
	file := BaselineFile{SchemaVersion: 1, Revision: "fixture-v1", Templates: []Template{
		{ID: "sun-solid-v1", QuestionRef: QuestionRef(KindCharArt, "", "draw sun", ""), Target: "日", Grid: sunGrid},
		{ID: "field-solid-v1", QuestionRef: QuestionRef(KindCharArt, "", "draw field", ""), Target: "田", Grid: fieldGrid},
	}}
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

func TestQualityGlyphNormalizationAndNegatives(t *testing.T) {
	scorer := NewScorer(makeBaseline(t))
	base := Sample{ResultID: 42, Kind: KindCharArt, Model: "fixture", Prompt: "draw sun", Status: "success"}
	equalGrid := strings.ReplaceAll(sunGrid, "\n", "\n\n")
	var spaced []string
	for _, line := range strings.Split(equalGrid, "\n") {
		spaced = append(spaced, strings.Join(strings.Split(line, ""), " "))
	}
	for name, response := range map[string]string{
		"exact":         sunGrid,
		"fenced":        "\n```ascii\n" + sunGrid + "\n```\n",
		"fullwidth":     strings.ReplaceAll(strings.ReplaceAll(sunGrid, "#", "＃"), " ", "　"),
		"blank spacing": strings.Join(spaced, "\n"),
		"padding":       "\n   #####   \n   #   #\n   ##### \n   #   #\n   #####\n\n",
		"crlf":          strings.ReplaceAll(sunGrid, "\n", "\r\n"),
	} {
		t.Run(name, func(t *testing.T) {
			s := base
			s.Response = response
			result := scorer.Score(s)
			if result.Score != 100 || result.Confidence != 40 || result.Reason != "glyph_uncalibrated" || result.SourceResultID != 42 || result.SourceSHA256 != digest([]byte(response)) {
				t.Fatalf("unexpected normalized score: %+v", result)
			}
			if result.Components.Dice != 1 || result.Components.Projection != 1 || result.Components.Layout != 1 {
				t.Fatalf("components: %+v", result.Components)
			}
		})
	}
	for name, response := range map[string]string{
		"wrong":      "#   #\n # # \n  #  \n # # \n#   #",
		"horizontal": "##########",
		"vertical":   "#\n#\n#\n#\n#\n#\n#\n#",
		"sparse":     "#    \n     \n     \n     \n    #",
	} {
		t.Run(name, func(t *testing.T) {
			s := base
			s.Response = response
			result := scorer.Score(s)
			if result.Score >= 80 {
				t.Fatalf("false positive %s: %+v", name, result)
			}
		})
	}
	for _, tc := range []struct{ response, reason string }{
		{"日", "copied_target"}, {"```\n" + sunGrid + "\n```\n日", "copied_target"},
		{"Here is the requested drawing", "plain_text"}, {"这是答案", "non_grid_text"},
		{"\u200b#", "non_grid_text"}, {"#\x00#", "non_grid_text"},
		{"\xff", "invalid_utf8"}, {" \n\t", "empty_grid"},
		{"```python\n#\n```", "invalid_fence"}, {"```\n#", "invalid_fence"},
		{"#\n```\n#", "invalid_fence"},
	} {
		t.Run(tc.reason+fmt.Sprint(len(tc.response)), func(t *testing.T) {
			s := base
			s.Response = tc.response
			result := scorer.Score(s)
			if result.Confidence != 0 || result.Reason != tc.reason {
				t.Fatalf("wanted %s: %+v", tc.reason, result)
			}
		})
	}
}

func TestQualityQuestionBindingAndQuizVerdict(t *testing.T) {
	b := makeBaseline(t)
	scorer := NewScorer(b)
	base := Sample{Kind: KindCharArt, Prompt: "draw sun", Response: sunGrid, Status: "success"}
	if QuestionRef(KindCharArt, "changed", "draw sun", "ignored") != QuestionRef(KindCharArt, "", "draw sun", "") {
		t.Fatal("char-art not bound to native prompt")
	}
	base.Prompt += " "
	if result := scorer.Score(base); result.Reason != "template_missing" {
		t.Fatalf("edited question reused baseline: %+v", result)
	}
	quiz := Sample{Kind: KindQuiz, QuestionID: "123", Prompt: "2+2", Expected: "4", Response: "4", Status: "success"}
	for _, tc := range []struct {
		verdict string
		score   int
		reason  string
	}{
		{"", 0, "quiz_missing_verdict_or_expected"}, {"pass", 100, "native_quiz_verdict_uncalibrated"}, {"fail", 0, "native_quiz_verdict_uncalibrated"},
	} {
		quiz.Verdict = tc.verdict
		r := scorer.Score(quiz)
		if r.Score != tc.score || r.Reason != tc.reason || r.Confidence > 40 {
			t.Fatalf("quiz regraded: %+v", r)
		}
	}
	quiz.Verdict = "pass"
	quiz.Response = "wrong text already graded upstream"
	if scorer.Score(quiz).Score != 100 {
		t.Fatal("changed authoritative quiz verdict")
	}
	quiz.Expected = ""
	if scorer.Score(quiz).Confidence != 0 {
		t.Fatal("accepted absent expected answer")
	}
	quiz.Expected = "4"
	quiz.Status = "failed"
	if r := scorer.Score(quiz); r.Score != 0 || r.Reason != "request_not_successful" {
		t.Fatalf("HTTP failure treated as intelligence: %+v", r)
	}
	ref := QuestionRef(KindQuiz, "123", "2+2", "4")
	for _, other := range []string{QuestionRef(KindQuiz, "124", "2+2", "4"), QuestionRef(KindQuiz, "123", "2+3", "4"), QuestionRef(KindQuiz, "123", "2+2", "5")} {
		if ref == other {
			t.Fatal("quiz question edits do not invalidate baseline")
		}
	}
	var nilScorer *Scorer
	if r := nilScorer.Score(Sample{Kind: KindCharArt, Status: "success"}); r.Reason != "baseline_missing" {
		t.Fatalf("nil baseline: %+v", r)
	}
}

func TestQualityInputBoundsAndStorageContract(t *testing.T) {
	scorer := NewScorer(makeBaseline(t))
	for _, tc := range []struct{ response, reason string }{
		{strings.Repeat("#", MaxResponseBytes+1), "input_limit"},
		{strings.Repeat("#\n", MaxRows), "row_limit"},
		{strings.Repeat("#", MaxColumns+1), "column_limit"},
		{strings.Repeat("\t", MaxColumns/4+1) + "#", "column_limit"},
	} {
		r := scorer.Score(Sample{Kind: KindCharArt, Prompt: "draw sun", Response: tc.response, Status: "success"})
		if r.Reason != tc.reason || r.Confidence != 0 {
			t.Fatalf("limit bypass %s: %+v", tc.reason, r)
		}
	}
	r := scorer.Score(Sample{Kind: KindCharArt, Prompt: "draw sun", Response: sunGrid, Status: "success"})
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"response", "prompt", sunGrid, "draw sun"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("result leaks raw text %q", forbidden)
		}
	}
	maxGrid := strings.TrimSuffix(strings.Repeat(strings.Repeat("#", MaxColumns)+"\n", MaxRows), "\n")
	// The byte cap also applies before the line/column parser.
	if r := scorer.Score(Sample{Kind: KindCharArt, Prompt: "draw sun", Response: maxGrid, Status: "success"}); r.Reason != "input_limit" {
		t.Fatalf("max raster byte cap: %+v", r)
	}
}

func TestQualityOversizeResponseRetainsBoundedIdentity(t *testing.T) {
	b := makeBaseline(t)
	scorer := NewScorer(b)
	for _, kind := range []string{KindCharArt, KindQuiz} {
		for _, size := range []int{MaxResponseBytes + 1, 32 * 1024, 64 * 1024} {
			t.Run(fmt.Sprintf("%s/%d", kind, size), func(t *testing.T) {
				sample := Sample{ResultID: 73, TaskID: 9, Kind: kind, Model: "model", QuestionID: "12", Prompt: "draw sun", Expected: "answer", Verdict: "pass", Response: strings.Repeat("x", size), Status: "success"}
				result := scorer.Score(sample)
				if result.QuestionRef != QuestionRef(sample.Kind, sample.QuestionID, sample.Prompt, sample.Expected) || result.SourceResultID != sample.ResultID || result.Kind != kind || result.QuestionID != sample.QuestionID || result.ScorerRevision != ScorerRevision || result.BaselineRevision != b.Revision() {
					t.Fatalf("bounded identity lost for oversized response: %+v", result)
				}
				if result.Reason != "input_limit" || result.Confidence != 0 || result.Score != 0 || result.SourceSHA256 != "" {
					t.Fatalf("oversized response was hashed or scored: %+v", result)
				}
			})
		}
	}
	for name, edit := range map[string]func(*Sample){
		"prompt":      func(s *Sample) { s.Prompt = strings.Repeat("p", MaxResponseBytes+1) },
		"expected":    func(s *Sample) { s.Expected = strings.Repeat("e", MaxResponseBytes+1) },
		"question ID": func(s *Sample) { s.QuestionID = strings.Repeat("q", 257) },
		"model":       func(s *Sample) { s.Model = strings.Repeat("m", 257) },
		"kind":        func(s *Sample) { s.Kind = strings.Repeat("k", 257) },
	} {
		t.Run(name, func(t *testing.T) {
			sample := Sample{Kind: KindQuiz, Prompt: "bounded", Response: "bounded", Expected: "answer", Verdict: "pass", Status: "success"}
			edit(&sample)
			result := scorer.Score(sample)
			if result.Reason != "input_limit" || result.QuestionRef != "" || result.SourceSHA256 != "" || result.Confidence != 0 {
				t.Fatalf("unbounded metadata was hashed or scored: %+v", result)
			}
		})
	}
}

func TestQualityConcurrentScorer(t *testing.T) {
	s := NewScorer(makeBaseline(t))
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 15; j++ {
				r := s.Score(Sample{Kind: KindCharArt, Prompt: "draw sun", Response: sunGrid, Status: "success"})
				if r.Score != 100 {
					t.Errorf("concurrent result %+v", r)
					return
				}
			}
		}()
	}
	workers.Wait()
}

func BenchmarkQualityGlyph(b *testing.B) {
	scorer := NewScorer(makeBaseline(b))
	sample := Sample{Kind: KindCharArt, Prompt: "draw sun", Response: sunGrid, Status: "success"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scorer.Score(sample)
	}
}

func BenchmarkQualityMaximumGrid(b *testing.B) {
	scorer := NewScorer(makeBaseline(b))
	sample := Sample{Kind: KindCharArt, Prompt: "draw sun", Response: strings.TrimSuffix(strings.Repeat(strings.Repeat("# ", 63)+"#\n", 128), "\n"), Status: "success"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scorer.Score(sample)
	}
}

func TestQualityGlyphMirrorAndMultipleTemplates(t *testing.T) {
	const asymmetric = "#####\n#    \n#    \n#    \n#    "
	const variant = "######\n##    \n##    \n##    \n##    \n##    "
	prompt := "draw cliff"
	file := BaselineFile{SchemaVersion: 1, Revision: "variants-fixture-v1", Templates: []Template{
		{ID: "cliff-v1", QuestionRef: QuestionRef(KindCharArt, "", prompt, ""), Target: "厂", Grid: asymmetric},
		{ID: "cliff-thick-v1", QuestionRef: QuestionRef(KindCharArt, "", prompt, ""), Target: "厂", Grid: variant},
	}}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBaseline(data)
	if err != nil {
		t.Fatal(err)
	}
	scorer := NewScorer(b)
	sample := Sample{Kind: KindCharArt, Prompt: prompt, Response: variant, Status: "success"}
	if r := scorer.Score(sample); r.Score != 100 || r.TemplateID != "cliff-thick-v1" {
		t.Fatalf("explicit variant not matched: %+v", r)
	}
	sample.Response = "#####\n    #\n    #\n    #\n    #"
	if r := scorer.Score(sample); r.Score >= 80 {
		t.Fatalf("mirror matched without permission: %+v", r)
	}
}

func BenchmarkQualityMaximumTemplates(b *testing.B) {
	file := BaselineFile{SchemaVersion: 1, Revision: "bench-fixture-v1"}
	for i := range 8 {
		file.Templates = append(file.Templates, Template{ID: fmt.Sprint(i), QuestionRef: QuestionRef(KindCharArt, "", "draw sun", ""), Target: "日", Grid: sunGrid})
	}
	data, err := json.Marshal(file)
	if err != nil {
		b.Fatal(err)
	}
	baseline, err := ParseBaseline(data)
	if err != nil {
		b.Fatal(err)
	}
	scorer := NewScorer(baseline)
	sample := Sample{Kind: KindCharArt, Prompt: "draw sun", Response: sunGrid, Status: "success"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scorer.Score(sample)
	}
}

func TestQualityTranslationBoundAndWeightedFormula(t *testing.T) {
	g, reason := normalize(sunGrid, "日")
	if reason != "" {
		t.Fatal(reason)
	}
	original := render(g)
	for _, shift := range []int{1, 4} {
		var moved bitmap
		for y := range 32 {
			for x := shift; x < 32; x++ {
				moved[y][x] = original[y][x-shift]
			}
		}
		parts, score := compare(moved, original, g.aspect(), g.aspect())
		if shift == 1 && math.Abs(score-1) > 1e-12 {
			t.Fatalf("one-pixel alignment not tolerated: %v %+v", score, parts)
		}
		if shift == 4 && score >= 1 {
			t.Fatal("unbounded translation accepted")
		}
		if score != .7*parts.Dice+.2*parts.Projection+.1*parts.Layout {
			t.Fatal("component weights changed")
		}
	}
}
