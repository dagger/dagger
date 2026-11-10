package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestFixtures(t *testing.T) {
	for _, name := range []string{"compose", "dagger-workspace"} {
		t.Run(name, func(t *testing.T) {
			input, err := os.ReadFile("../../testdata/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile("../../testdata/" + name + ".txt")
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := run(nil, bytes.NewReader(input), &out); err != nil {
				t.Fatal(err)
			}
			if out.String() != string(want) {
				t.Fatalf("flattening differs from %s.txt:\n%s", name, out.String())
			}
		})
	}
}

func TestComposeSelection(t *testing.T) {
	input, err := os.ReadFile("../../testdata/compose.json")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(nil, bytes.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	var selected bytes.Buffer
	if err := run([]string{"-only", "docker/compose/projects/services/up"}, bytes.NewReader(input), &selected); err != nil {
		t.Fatal(err)
	}
	// Filtering rows must keep the complete column definition table.
	allColumns := strings.SplitN(out.String(), "\n\n", 2)[0]
	selectedColumns := strings.SplitN(selected.String(), "\n\n", 2)[0]
	if allColumns != selectedColumns {
		t.Fatal("row selection changed columns")
	}
	if strings.Contains(selected.String(), "services/all/up") {
		t.Fatal("selection kept the collection helper")
	}
	if !strings.Contains(selected.String(), "worker") || !strings.Contains(selected.String(), "web") {
		t.Fatal("selection lost service items")
	}
}

func TestInvalidInputs(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"unknown field", `[{"typo":"Check"}]`, `unknown field "typo"`},
		{"extra JSON", `[] []`, `expected one JSON array`},
		{"missing keys", `[{"module":"app","path":["tests"],"type":"Check","collections":[{"fields":["tests"],"itemType":"Test"}],"items":[[]]}]`, `needs 1 collection keys, got 0`},
		{"repeated dimension", `[{"module":"matrix","path":["rows"],"type":"Cell","collections":[{"fields":["rows"],"itemType":"Columns"},{"itemType":"Cell"}],"items":[["r","c"]]}]`, `has repeated dimension /matrix/rows`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(nil, strings.NewReader(tc.input), &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
			if out.Len() != 0 {
				t.Fatalf("error produced partial output: %s", out.String())
			}
		})
	}
}

func TestEmptyAndControlCharacterKeys(t *testing.T) {
	input := `[{"module":"app","path":["tests"],"type":"Check","collections":[{"fields":["tests"],"itemType":"Test"}],"items":[[""],["-"],["a\tb"]]}]`
	var out bytes.Buffer
	if err := run(nil, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`""`, `"-"`, `"a\tb"`} {
		if !strings.Contains(out.String(), value) {
			t.Fatalf("output lost key %s:\n%s", value, out.String())
		}
	}
}
