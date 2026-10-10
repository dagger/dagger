package names

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

const testSchemaJSON = `{
  "__schemaVersion": "v1.0.0-beta.15",
  "__schema": {"types": [
    {"kind": "OBJECT", "name": "Query", "fields": [
      {"name": "withGPU", "args": [{"name": "callID"}, {"name": "_"}]},
      {"name": "formatIdentifiers", "args": []}
    ]},
    {"kind": "INPUT_OBJECT", "name": "BuildArg", "inputFields": [{"name": "value"}]},
    {"kind": "ENUM", "name": "Mode", "enumValues": [{"name": "PerSession"}]},
    {"kind": "OBJECT", "name": "__Type", "fields": [{"name": "kind"}]}
  ]}
}`

var testFormats = []Format{
	{Casing: "CAMEL", Acronyms: "CAPITALIZED"},
	{Casing: "SCREAMING_SNAKE", Acronyms: "UPPERCASE"},
}

func TestFile(t *testing.T) {
	type call struct {
		names   []string
		format  string
		version string
	}
	var calls []call
	fake := func(_ context.Context, names []string, format Format, version string) ([]string, error) {
		calls = append(calls, call{names, format.String(), version})
		out := make([]string, len(names))
		for i, name := range names {
			out[i] = format.Casing + "(" + name + ")"
		}
		return out, nil
	}

	file, err := File(t.Context(), []byte(testSchemaJSON), testFormats, fake)
	if err != nil {
		t.Fatal(err)
	}

	// introspection names and names without letters or digits are left out;
	// the dictionary is the schema's version's
	names := []string{"BuildArg", "Mode", "PerSession", "Query", "callID", "formatIdentifiers", "value", "withGPU"}
	wantCalls := []call{
		{names, "CAMEL:CAPITALIZED", "v1.0.0-beta.15"},
		{names, "SCREAMING_SNAKE:UPPERCASE", "v1.0.0-beta.15"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls = %v, want %v", calls, wantCalls)
	}
	if got := file["CAMEL:CAPITALIZED"]["withGPU"]; got != "CAMEL(withGPU)" {
		t.Errorf("CAMEL:CAPITALIZED withGPU = %q", got)
	}
	if got := file["SCREAMING_SNAKE:UPPERCASE"]["PerSession"]; got != "SCREAMING_SNAKE(PerSession)" {
		t.Errorf("SCREAMING_SNAKE:UPPERCASE PerSession = %q", got)
	}
	if len(file) != 2 || len(file["CAMEL:CAPITALIZED"]) != len(names) {
		t.Errorf("file = %v", file)
	}
}

func TestFileWithoutFormatIdentifiers(t *testing.T) {
	schemaJSON := strings.Replace(testSchemaJSON, `"formatIdentifiers"`, `"identifier"`, 1)
	file, err := File(t.Context(), []byte(schemaJSON), testFormats,
		func(context.Context, []string, Format, string) ([]string, error) {
			t.Fatal("formatted names for a schema without formatIdentifiers")
			return nil, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if file != nil {
		t.Errorf("file = %v, want nil", file)
	}
}

func TestFileChecksResults(t *testing.T) {
	_, err := File(t.Context(), []byte(testSchemaJSON), testFormats,
		func(context.Context, []string, Format, string) ([]string, error) {
			return []string{"one"}, nil
		})
	if err == nil || !strings.Contains(err.Error(), "got 1 back") {
		t.Errorf("err = %v", err)
	}
}
