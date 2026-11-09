package quality

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQualityBaselineRejectsAmbiguityAndBounds(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":1,"revision":"v1","revision":"v2"}`,
		`{"schema_version":1,"revision":"v1","Revision":"v2"}`,
		`{"Schema_version":1,"revision":"v1"}`,
		`{"schema_version":1,"REVISION":"v1"}`,
		`{"schema_version":1,"revision":"v1","unknown":true}`,
		`{"schema_version":2,"revision":"v1"}`,
		`{"schema_version":1,"revision":" "}`,
		`{"schema_version":1,"revision":"v1"} {}`,
		`{"schema_version":1,"revision":"v1","templates":[` + strings.Repeat(`{},`, 1000) + `{}]}`,
		`{"schema_version":1,"revision":"v1","templates":` + strings.Repeat("[", 18) + `0` + strings.Repeat("]", 18) + `}`,
		strings.Repeat(" ", MaxBaselineBytes+1),
	} {
		if _, err := ParseBaseline([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid baseline %.100s", raw)
		}
	}
	file := BaselineFile{SchemaVersion: 1, Revision: "v1", Templates: []Template{{ID: "first", QuestionRef: QuestionRef(KindCharArt, "", "draw", ""), Target: "日", Grid: sunGrid}}}
	for _, change := range []func(*BaselineFile){
		func(f *BaselineFile) { f.Templates[0].Target = "日田" },
		func(f *BaselineFile) { f.Templates[0].Target = "x" },
		func(f *BaselineFile) { f.Templates[0].Grid = "日" },
		func(f *BaselineFile) { f.Templates[0].QuestionRef = "forged" },
		func(f *BaselineFile) { f.Templates = append(f.Templates, f.Templates[0]) },
		func(f *BaselineFile) {
			other := f.Templates[0]
			other.ID = "other"
			other.Target = "田"
			f.Templates = append(f.Templates, other)
		},
		func(f *BaselineFile) {
			for i := 0; i < 8; i++ {
				other := f.Templates[0]
				other.ID += strings.Repeat("x", i+1)
				f.Templates = append(f.Templates, other)
			}
		},
	} {
		copyFile := file
		copyFile.Templates = append([]Template(nil), file.Templates...)
		change(&copyFile)
		data, err := json.Marshal(copyFile)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ParseBaseline(data); err == nil {
			t.Fatal("accepted invalid template")
		}
	}
}

func TestQualityBaselineDigestAndUnknownCoverage(t *testing.T) {
	raw := []byte(`{"schema_version":1,"revision":"v1"}`)
	b, err := ParseBaseline(raw)
	if err != nil {
		t.Fatal(err)
	}
	if b.Digest() != digest(raw) || b.Report().BaselineSHA256 != digest(raw) || b.Revision() != "v1" || b.Report().ProductionData != "Unknown" || b.Report().TemplateSource != TemplateSource {
		t.Fatalf("baseline metadata: %+v", b.Report())
	}
	other, err := ParseBaseline(append(raw, ' '))
	if err != nil {
		t.Fatal(err)
	}
	if b.Digest() == other.Digest() {
		t.Fatal("full-file digest ignored whitespace")
	}
	if b.Reference(KindQuiz, "question", "model").CalibratedCoverage {
		t.Fatal("missing labels confer coverage")
	}
	var absent *Baseline
	if absent.Reference(KindQuiz, "", " ").CalibratedCoverage || absent.Digest() != "" || absent.Report().ProductionData != "Unknown" {
		t.Fatal("nil baseline not neutral")
	}
}

func TestQualityBaselineJSONTokenBudget(t *testing.T) {
	array := "[" + strings.Repeat("0,", 999) + "0]"
	raw := `{"schema_version":1,"revision":"v1","templates":[` + strings.Repeat(array+",", 100) + array + ` ]}`
	if _, err := ParseBaseline([]byte(raw)); err == nil || !strings.Contains(err.Error(), "token limit") {
		t.Fatalf("token budget not checked before decode: %v", err)
	}
}
