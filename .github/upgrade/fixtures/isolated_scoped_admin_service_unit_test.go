//go:build unit

// Pure upstream tests retained verbatim in the unit lane; DB originals use integration.
package service

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestScopedAdminAccountFieldsRejectUnknown(t *testing.T) {
	for _, body := range []string{`{"credentials":{}}`, `{"extra":{}}`, `{"platform":"openai"}`} {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(body), &fields)
		if _, err := scopedAccountFieldPermissions(fields, false); err == nil {
			t.Fatalf("应拒绝字段: %s", body)
		}
	}
}

func TestScopedAdminAccountModelMappingFieldPermissions(t *testing.T) {
	for _, create := range []bool{false, true} {
		for _, mapping := range []string{`{"gpt-6":"gpt-6","alias":"gpt-6"}`, `{}`, `null`} {
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(`{"credentials":{"model_mapping":`+mapping+`}}`), &fields))
			permissions, err := scopedAccountFieldPermissions(fields, create)
			require.NoError(t, err)
			require.Equal(t, []string{"accounts.models.write"}, permissions)
		}
	}
	for _, mapping := range []string{`[]`, `"gpt-6"`, `{"gpt-6":42}`, `{"gpt-6":null}`, `{"gpt-6":{}}`, `{"":"gpt-6"}`, `{"gpt-6":" "}`, `{"gpt-6":"` + strings.Repeat("x", 65536) + `"}`} {
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(`{"credentials":{"model_mapping":`+mapping+`}}`), &fields))
		_, err := scopedAccountFieldPermissions(fields, false)
		require.Equal(t, 400, infraerrors.Code(err), mapping)
	}
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(`{"credentials":{"model_mapping":{},"access_token":"secret","base_url":"https://example.com"}}`), &fields))
	permissions, err := scopedAccountFieldPermissions(fields, false)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"accounts.models.write", "accounts.credentials", "accounts.network"}, permissions)
}

func TestScopedAdminGroupReplacementPreservesForeignBindings(t *testing.T) {
	policy := adminaccess.Policy{Rules: []adminaccess.Rule{{Permissions: []string{"accounts.groups"}, GroupIDs: []int64{1}, AccountTypes: []string{"oauth"}}}}
	resource := adminaccess.Account{ID: 9, Type: "oauth", GroupIDs: []int64{1}}
	for _, tc := range []struct {
		request []int64
		want    []int64
		denied  bool
	}{{[]int64{1}, []int64{2, 1}, false}, {[]int64{}, []int64{2}, false}, {[]int64{2}, nil, true}} {
		got, err := scopedAdminMergeGroups(policy, resource, []int64{1, 2}, tc.request)
		if (err != nil) != tc.denied || (!tc.denied && !reflect.DeepEqual(got, tc.want)) {
			t.Fatalf("request=%v got=%v err=%v", tc.request, got, err)
		}
	}
}

func TestScopedAdminAccountCredentialsNeedIndependentPermissions(t *testing.T) {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"credentials":{"api_key":"secret","base_url":"https://example.com"},"status":"active","concurrency":2}`), &fields)
	permissions, err := scopedAccountFieldPermissions(fields, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"accounts.credentials", "accounts.network", "accounts.status", "accounts.schedule"} {
		found := false
		for _, p := range permissions {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("缺少独立权限 %s", want)
		}
	}
}

func TestNormalizeAccountProxySelectionConflicts(t *testing.T) {
	id := int64(7)
	zero := int64(0)
	for _, tc := range []struct {
		mode    string
		id      *int64
		random  bool
		want    string
		invalid bool
	}{
		{"", &id, false, "", false}, {"specified", &id, false, "specified", false}, {"direct", nil, false, "direct", false}, {"", nil, true, "random", false}, {"random", nil, false, "random", false}, {"least_used", nil, false, "least_used", false},
		{"random", &id, false, "", true}, {"least_used", &id, false, "", true}, {"direct", &id, false, "", true}, {"specified", &zero, false, "", true}, {"unknown", nil, false, "", true}, {"least_used", nil, true, "", true},
	} {
		got, err := NormalizeAccountProxyMode(tc.mode, tc.id, tc.random)
		if tc.invalid {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		}
	}
}
