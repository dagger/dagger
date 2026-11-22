package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseFunctionErrorReport(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		content string
		want    *FunctionErrorReport
	}{
		{"empty", " \n", nil},
		{"plain text", "it failed [traceparent:x]\n", &FunctionErrorReport{Message: "it failed [traceparent:x]"}},
		{"data", `{"message": "it failed", "values": {"exitCode": 3, "extra.why": {"a": [1]}}}`, &FunctionErrorReport{
			Message: "it failed",
			Values:  map[string]json.RawMessage{"exitCode": json.RawMessage(`3`), "extra.why": json.RawMessage(`{"a": [1]}`)},
		}},
		{"id", `{"id": "abc"}`, &FunctionErrorReport{ID: "abc"}},
		{"a JSON string is text", `"it failed"`, &FunctionErrorReport{Message: `"it failed"`}},
		{"an object without a message is text", `{"values": {}}`, &FunctionErrorReport{Message: `{"values": {}}`}},
		{"a malformed object is text", `{"message": 3}`, &FunctionErrorReport{Message: `{"message": 3}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ParseFunctionErrorReport([]byte(tc.content)))
		})
	}
}

func TestFunctionErrorReportReturnsValuesSorted(t *testing.T) {
	t.Parallel()
	fnCall := newFunctionCall(FunctionCall{Name: "fn"})
	report := ParseFunctionErrorReport([]byte(`{"message": "it failed", "values": {"b": "two", "a": 1}}`))
	require.NoError(t, report.Return(t.Context(), fnCall))
	returned, set, err := fnCall.returnResult()
	require.NoError(t, err)
	require.True(t, set)
	require.True(t, returned.HasError)
	require.Equal(t, "it failed", returned.Error.Message)
	var names []string
	var values []string
	for _, v := range returned.Error.Values {
		names = append(names, v.Name)
		values = append(values, string(v.Value))
	}
	require.Equal(t, []string{"a", "b"}, names)
	require.Equal(t, []string{`1`, `"two"`}, values)
}
