package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const namesTestSchema = `{
  "__schemaVersion": "v1.0.0",
  "__schema": {
    "queryType": {"name": "Query"},
    "types": [
      {
        "name": "Query",
        "fields": [
          {"name": "gitRef", "args": [{"name": "url"}]},
          %s
        ]
      },
      {
        "name": "GitRef",
        "fields": [{"name": "prerequisiteSHAs", "args": []}]
      },
      {
        "name": "GitRefOpts",
        "inputFields": [{"name": "withGPU"}, {"name": "_"}, {"name": "café"}]
      },
      {
        "name": "NetworkProtocol",
        "enumValues": [{"name": "TCP"}, {"name": "UDP"}]
      },
      {
        "name": "__Type",
        "fields": [{"name": "possibleTypes", "args": []}]
      }
    ]
  }
}`

const formatIdentifiersField = `{"name": "formatIdentifiers", "args": [{"name": "names"}, {"name": "casing"}, {"name": "acronyms"}, {"name": "version"}]}`

func namesTestJSON(withFormatIdentifiers bool) []byte {
	field := `{"name": "id", "args": []}`
	if withFormatIdentifiers {
		field = formatIdentifiersField
	}
	return []byte(strings.Replace(namesTestSchema, "%s", field, 1))
}

func TestSchemaNames(t *testing.T) {
	var schema introspectionSchema
	if err := json.Unmarshal(namesTestJSON(true), &schema); err != nil {
		t.Fatal(err)
	}
	if !schema.hasFormatIdentifiers() {
		t.Error("expected Query.formatIdentifiers")
	}
	if schema.SchemaVersion != "v1.0.0" {
		t.Errorf("schema version: got %q", schema.SchemaVersion)
	}
	want := []string{
		"GitRef", "GitRefOpts", "NetworkProtocol", "Query", "TCP", "UDP",
		"acronyms", "casing", "formatIdentifiers", "gitRef", "names",
		"prerequisiteSHAs", "url", "version", "withGPU",
	}
	if got := schema.names(); !reflect.DeepEqual(got, want) {
		t.Errorf("names:\n got %q\nwant %q", got, want)
	}

	schema = introspectionSchema{}
	if err := json.Unmarshal(namesTestJSON(false), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.hasFormatIdentifiers() {
		t.Error("expected no Query.formatIdentifiers")
	}
}

func TestSchemaNamesCustomQueryType(t *testing.T) {
	var schema introspectionSchema
	data := `{"__schema": {"queryType": {"name": "Root"}, "types": [
	  {"name": "Query", "fields": [{"name": "formatIdentifiers"}]},
	  {"name": "Root", "fields": [{"name": "id"}]}
	]}}`
	if err := json.Unmarshal([]byte(data), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.hasFormatIdentifiers() {
		t.Error("formatIdentifiers on a type other than the query type must not count")
	}
}

func TestFormattable(t *testing.T) {
	for name, want := range map[string]bool{
		"withGPU": true,
		"_":       false,
		"__":      false,
		"x_1":     true,
		"9":       true,
		"café":    false,
		"":        false,
	} {
		if got := formattable(name); got != want {
			t.Errorf("formattable(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBatchNames(t *testing.T) {
	if got := batchNames(nil, 10); got != nil {
		t.Errorf("batchNames(nil): got %q", got)
	}
	want := [][]string{{"abcd", "efg"}, {"hijklmnopq"}, {"r"}}
	if got := batchNames([]string{"abcd", "efg", "hijklmnopq", "r"}, 7); !reflect.DeepEqual(got, want) {
		t.Errorf("batchNames:\n got %q\nwant %q", got, want)
	}
}

func TestSchemaNamesFile(t *testing.T) {
	ctx := context.Background()
	var calls int
	camel := func(_ context.Context, names []string, version string) ([]string, error) {
		calls++
		if version != "v1.0.0" {
			t.Errorf("version: got %q", version)
		}
		out := make([]string, len(names))
		for i, name := range names {
			out[i] = "camel_" + name
		}
		return out, nil
	}

	data, err := schemaNamesFile(ctx, namesTestJSON(true), camel)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("calls: got %d, want 1", calls)
	}
	var file map[string]map[string]string
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file) != 1 {
		t.Errorf("formats: got %d, want 1", len(file))
	}
	names, ok := file["CAMEL:CAPITALIZED"]
	if !ok {
		t.Fatalf("no CAMEL:CAPITALIZED names in %s", data)
	}
	for name, want := range map[string]string{
		"prerequisiteSHAs": "camel_prerequisiteSHAs",
		"withGPU":          "camel_withGPU",
		"NetworkProtocol":  "camel_NetworkProtocol",
	} {
		if got := names[name]; got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"_", "café", "possibleTypes", "__Type"} {
		if _, ok := names[name]; ok {
			t.Errorf("unexpected name %q", name)
		}
	}

	// A schema without Query.formatIdentifiers formats nothing: codegen
	// keeps its legacy conversion.
	calls = 0
	data, err = schemaNamesFile(ctx, namesTestJSON(false), camel)
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Errorf("expected no names file, got %s", data)
	}
	if calls != 0 {
		t.Errorf("calls: got %d, want 0", calls)
	}

	// A short answer is an error, not a partial map.
	_, err = schemaNamesFile(ctx, namesTestJSON(true), func(context.Context, []string, string) ([]string, error) {
		return []string{"x"}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "got 1 back") {
		t.Errorf("short answer: got error %v", err)
	}

	// Invalid JSON is an error.
	if _, err := schemaNamesFile(ctx, []byte("{"), camel); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}
