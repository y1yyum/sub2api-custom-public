package requestmodel

import "testing"

func TestTopLevelModelMultiplicityContract(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		duplicate bool
	}{
		{"different", `{"model":"cheap","model":"expensive"}`, true},
		{"identical", `{"model":"same","model":"same"}`, true},
		{"case", `{"model":"same","MODEL":"same"}`, true},
		{"escaped", `{"mo\u0064el":"same","model":"same"}`, true},
		{"escaped_case", `{"\u004dodel":"same","model":"same"}`, true},
		{"null", `{"model":null,"model":"same"}`, true},
		{"number", `{"model":"same","Model":42}`, true},
		{"empty", `{"model":"","model":"same"}`, true},
		{"object", `{"model":{},"model":"same"}`, true},
		{"single", `{"model":"same"}`, false},
		{"nested", `{"model":"same","input":{"model":"other"}}`, false},
		{"nested_repeats", `{"model":"same","input":{"model":"a","model":"b"}}`, false},
		{"array_root", `[{"model":"a"},{"model":"b"}]`, false},
		{"malformed_is_parser_error", `{"model":"a","model":`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasDuplicateTopLevelModel([]byte(tc.body)); got != tc.duplicate {
				t.Fatalf("duplicate = %v, want %v", got, tc.duplicate)
			}
		})
	}
}
