package astscan_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/generator/go/astscan"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TestScan(t *testing.T) {
	cases := []struct {
		name       string
		moduleName string
	}{
		{"empty", "empty"},
		{"single_struct", "echo"},
		{"interface", "zoo"},
		{"enum", "status"},
		{"enum_untyped", "status"},
		{"void_return", "echo"},
		{"optional_args", "echo"},
		{"module_local_dagger", "my-module"},
	}
	schema := loadSchema(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := astscan.Scan(filepath.Join("testdata", tc.name), tc.moduleName, schema)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			expectedPath := filepath.Join("testdata", tc.name, "expected.json")
			expectedBytes, err := os.ReadFile(expectedPath)
			if err != nil {
				// The empty case skips fixture comparison as long as
				// the scanner really produced an empty ModuleTypes.
				if os.IsNotExist(err) && len(got.Objects)+len(got.Interfaces)+len(got.Enums) == 0 {
					return
				}
				t.Fatalf("read expected: %v", err)
			}
			gotBytes, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal got: %v", err)
			}
			assertJSONEqual(t, gotBytes, expectedBytes)
		})
	}
}

func TestScan_UnresolvedType(t *testing.T) {
	schema := loadSchema(t)
	_, err := astscan.Scan(filepath.Join("testdata", "unresolved_type"), "echo", schema)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported external type") {
		t.Errorf("unexpected error: %v", err)
	}
	// Error should be position-annotated so module authors can jump
	// directly to the offending source location.
	if !strings.Contains(err.Error(), "main.go:") {
		t.Errorf("expected error to include source position (main.go:), got: %v", err)
	}
}

func loadSchema(t *testing.T) *introspection.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var resp introspection.Response
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return resp.Schema
}

// assertJSONEqual compares two JSON byte slices after canonical
// re-marshalling so formatting differences don't cause false
// negatives. Copied from schematool_test.go.
func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	gb, _ := json.MarshalIndent(g, "", "  ")
	wb, _ := json.MarshalIndent(w, "", "  ")
	if !bytes.Equal(gb, wb) {
		t.Errorf("json mismatch\n---got---\n%s\n---want---\n%s", gb, wb)
	}
}
