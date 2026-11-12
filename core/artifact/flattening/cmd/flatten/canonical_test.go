package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestCanonicalFixtures(t *testing.T) {
	for _, name := range []string{"canonical-compose", "canonical-dagger"} {
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
				t.Fatalf("want:\n%s\ngot:\n%s", want, out.String())
			}
		})
	}
}

func TestCanonicalSelectionKeepsSchema(t *testing.T) {
	input, err := os.ReadFile("../../testdata/canonical-compose.json")
	if err != nil {
		t.Fatal(err)
	}
	var all, selected bytes.Buffer
	if err := run(nil, bytes.NewReader(input), &all); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-only", "healthCheck.run"}, bytes.NewReader(input), &selected); err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(all.String(), "\n\n", 2)
	selectedParts := strings.SplitN(selected.String(), "\n\n", 2)
	if parts[0] != selectedParts[0] {
		t.Fatal("selection changed dimension names")
	}
	allLines := strings.Split(parts[1], "\n")
	selectedLines := strings.Split(selectedParts[1], "\n")
	if strings.Join(strings.Fields(allLines[0]), "\t") != strings.Join(strings.Fields(selectedLines[0]), "\t") || len(selectedLines) != 4 {
		t.Fatalf("selection changed columns or row count:\n%s", selected.String())
	}
	for _, row := range selectedLines[1:3] {
		if !strings.HasSuffix(row, "healthCheck.run") {
			t.Fatalf("selection kept another name: %s", row)
		}
	}
}

func TestCanonicalRejectsInvalidInputBeforeOutput(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"old path", `[{"module":"app","path":["tests"],"type":"Check"}]`, `unknown field "path"`},
		{"unknown field", `[{"typo":"Check"}]`, `unknown field "typo"`},
		{"trailing JSON", `[] []`, `expected one JSON array`},
		{"trailing text", `[] x`, `read schema:`},
		{"null schema", `null`, `expected one JSON array`},
		{"null row", `[{"type":"Check","items":[null]}]`, `expected an array`},
		{"null key", `[{"type":"Check","items":[[null]]}]`, `must be a string`},
		{"non-string key", `[{"type":"Check","items":[[2]]}]`, `read schema:`},
		{"duplicate row", `[{"type":"Check","items":[[],[]]}]`, `duplicate typed coordinates`},
		{"duplicate after selection", `[{"type":"Check","localPath":["other"],"items":[[],[]]}]`, `duplicate typed coordinates`},
		{"extra keys", `[{"type":"Check","items":[["x"]]}]`, `keys`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run([]string{"-only", "check"}, strings.NewReader(tc.input), &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
			if out.Len() != 0 {
				t.Fatalf("invalid input produced output: %s", out.String())
			}
		})
	}
}

func TestCanonicalKeyDisplay(t *testing.T) {
	input := `[{"module":"app","type":"Check","collections":[{"fields":["tests"],"type":"Tests","itemType":"Test"}],"items":[[""],["-"],["a\tb"],["a\nb"],["a\u0000b"],["a\u007fb"]]},{"module":"app","type":"Service","items":[[]]}]`
	var out bytes.Buffer
	if err := run(nil, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`""`, `"-"`, `"a\tb"`, `"a\nb"`, `"a\x00b"`, `"a\x7fb"`} {
		if !strings.Contains(out.String(), value) {
			t.Fatalf("output lost key %s:\n%s", value, out.String())
		}
	}
	if !strings.Contains(out.String(), "Service") || !strings.Contains(out.String(), "  -") {
		t.Fatalf("output lost absent coordinate:\n%s", out.String())
	}
}

func TestCanonicalTypesCanShareCoordinates(t *testing.T) {
	var out bytes.Buffer
	if err := run(nil, strings.NewReader(`[{"type":"Check","items":[[]]},{"type":"Service","items":[[]]}]`), &out); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalCollectionTypeUnifiesRoutes(t *testing.T) {
	const input = `[
 {"module":"app","type":"Check","localPath":["unit"],"collections":[{"fields":["primary"],"type":"AppPackages","itemType":"AppPackage"}],"items":[["one"]]},
 {"module":"app","type":"Check","localPath":["slow"],"collections":[{"fields":["secondary"],"type":"AppPackages","itemType":"AppPackage"}],"items":[["two"]]}
 ]`
	var out bytes.Buffer
	if err := run(nil, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "AppPackages") != 1 {
		t.Fatalf("routes did not share one collection type column:\n%s", out.String())
	}
	if strings.Contains(out.String(), "/app/") || !strings.Contains(out.String(), "unit") || !strings.Contains(out.String(), "slow") {
		t.Fatalf("output changed type coordinates or local names:\n%s", out.String())
	}
}
