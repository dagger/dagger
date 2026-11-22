package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

func TestParseFunctionErrorReport(t *testing.T) {
	t.Parallel()
	errID, err := call.New().
		Append(&ast.Type{NamedType: "Error", NonNull: true}, "error",
			call.WithArgs(call.NewArgument("message", call.NewLiteralString("it failed"), false))).
		Encode()
	require.NoError(t, err)
	dirID, err := call.New().Append(&ast.Type{NamedType: "Directory", NonNull: true}, "directory").Encode()
	require.NoError(t, err)
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
		{"id", `{"id": "` + errID + `"}`, &FunctionErrorReport{ID: errID}},
		{"a JSON string is text", `"it failed"`, &FunctionErrorReport{Message: `"it failed"`}},
		{"an object without a message is text", `{"values": {}}`, &FunctionErrorReport{Message: `{"values": {}}`}},
		{"a malformed object is text", `{"message": 3}`, &FunctionErrorReport{Message: `{"message": 3}`}},
		{"an object with other fields is text", `{"message": "not found", "code": 404}`,
			&FunctionErrorReport{Message: `{"message": "not found", "code": 404}`}},
		{"an id beside a message is text", `{"id": "not_found", "message": "no such droplet"}`,
			&FunctionErrorReport{Message: `{"id": "not_found", "message": "no such droplet"}`}},
		{"an id that is no ID is text", `{"id": "not_found"}`, &FunctionErrorReport{Message: `{"id": "not_found"}`}},
		{"an id of another type is text", `{"id": "` + dirID + `"}`, &FunctionErrorReport{Message: `{"id": "` + dirID + `"}`}},
		{"trailing content is text", `{"message": "a"} {"message": "b"}`,
			&FunctionErrorReport{Message: `{"message": "a"} {"message": "b"}`}},
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
