package core

import (
	"encoding/json"
	"errors"
	"strings"
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
		{"data", `{"message": "it failed", "values": {"exitCode": 3, "extra.why": {"a": [1]}}}`, &FunctionErrorReport{
			Message: "it failed",
			Values:  map[string]json.RawMessage{"exitCode": json.RawMessage(`3`), "extra.why": json.RawMessage(`{"a": [1]}`)},
		}},
		{"a message alone", `{"message": "it failed [traceparent:x]"}`, &FunctionErrorReport{Message: "it failed [traceparent:x]"}},
		{"id", `{"id": "` + errID + `"}`, &FunctionErrorReport{ID: errID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report, err := ParseFunctionErrorReport([]byte(tc.content))
			require.NoError(t, err)
			require.Equal(t, tc.want, report)
		})
	}
	for _, tc := range []struct {
		name    string
		content string
		why     string
	}{
		{"plain text", "it failed [traceparent:x]\n", `not a JSON object: "it failed [traceparent:x]"`},
		{"long plain text", strings.Repeat("x", 100), `not a JSON object: "` + strings.Repeat("x", 80) + `"...`},
		{"a JSON string", `"it failed"`, `not a JSON object: "\"it failed\""`},
		{"an array", `[{"message": "it failed"}]`, `not a JSON object: "[{\"message\": \"it failed\"}]"`},
		{"truncated", `{"message": "it fai`, `invalid JSON: unexpected end of the content`},
		{"malformed", `{"message": "a", "values": {"x": NaN}}`, `invalid JSON at offset 34: invalid character 'N'`},
		{"a closing bracket after the object", `{"message": "a"}}`, "content after the JSON object"},
		{"no message or id", `{"values": {}}`, `neither a "message" nor an "id"`},
		{"an empty message", `{"message": ""}`, `neither a "message" nor an "id"`},
		{"a message of the wrong type", `{"message": 3}`, `field "message": want a string, got number`},
		{"values of the wrong type", `{"message": "a", "values": [1]}`, `field "values": want an object, got array`},
		{"another field", `{"message": "not found", "code": 404}`, `unknown field "code"`},
		{"an id beside a message", `{"id": "not_found", "message": "no such droplet"}`, `"id" beside "message" or "values"`},
		{"an id that is no ID", `{"id": "not_found"}`, `"id" is not an Error ID`},
		{"an id of another type", `{"id": "` + dirID + `"}`, `"id" is not an Error ID`},
		{"content after the object", `{"message": "a"} {"message": "b"}`, "content after the JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report, err := ParseFunctionErrorReport([]byte(tc.content))
			require.Nil(t, report)
			require.ErrorContains(t, err, tc.why)
		})
	}
}

// A process that wrote an invalid report fails its call with an error that
// says so, and its own failure, values and all, from the function call.
func TestInvalidFunctionErrorReport(t *testing.T) {
	t.Parallel()
	execErr := &ExecError{
		Err:      errors.New("exit code: 6 [traceparent:0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331]"),
		Cmd:      []string{"sh", "/probe.sh"},
		ExitCode: 6,
		Stdout:   "out",
		Stderr:   "err",
	}
	_, invalid := ParseFunctionErrorReport([]byte("it failed"))
	report := execErr.invalidFunctionErrorReport(invalid)
	require.Equal(t, `invalid function error report: not a JSON object: "it failed"; the module process failed with: exit code: 6`, report.Message)
	require.Equal(t, map[string]json.RawMessage{
		"_type":    json.RawMessage(`"EXEC_ERROR"`),
		"cmd":      json.RawMessage(`["sh","/probe.sh"]`),
		"exitCode": json.RawMessage(`6`),
		"stdout":   json.RawMessage(`"out"`),
		"stderr":   json.RawMessage(`"err"`),
	}, report.Values)
}

func TestFunctionErrorReportReturnsValuesSorted(t *testing.T) {
	t.Parallel()
	fnCall := newFunctionCall(FunctionCall{Name: "fn"})
	report, err := ParseFunctionErrorReport([]byte(`{"message": "it failed", "values": {"b": "two", "a": 1}}`))
	require.NoError(t, err)
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
