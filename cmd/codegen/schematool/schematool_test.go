package schematool_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/cmd/codegen/schematool"
)

func TestMerge(t *testing.T) {
	cases := []string{
		"single_object",
		"interface",
		"enum",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata", name)

			schema := loadSchema(t, filepath.Join(dir, "schema.json"))
			modTypes := loadModuleTypes(t, filepath.Join(dir, "module_types.json"))

			if err := schematool.Merge(schema, modTypes); err != nil {
				t.Fatalf("merge: %v", err)
			}

			got := marshal(t, schema)
			want := readFile(t, filepath.Join(dir, "expected.json"))
			assertJSONEqual(t, got, want)
		})
	}
}

func TestMergeIdempotent(t *testing.T) {
	dir := filepath.Join("testdata", "single_object")
	schema := loadSchema(t, filepath.Join(dir, "schema.json"))
	modTypes := loadModuleTypes(t, filepath.Join(dir, "module_types.json"))

	if err := schematool.Merge(schema, modTypes); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	// Second merge of the same mod types must be a no-op, not an
	// error. The multi-pass codegen loop re-enters Merge with the
	// same schema pointer, so Merge has to stay idempotent.
	if err := schematool.Merge(schema, modTypes); err != nil {
		t.Fatalf("second merge (should be idempotent): %v", err)
	}

	got := marshal(t, schema)
	want := readFile(t, filepath.Join(dir, "expected.json"))
	assertJSONEqual(t, got, want)
}

func TestMergeConflict(t *testing.T) {
	dir := filepath.Join("testdata", "conflict")
	schema := loadSchema(t, filepath.Join(dir, "schema.json"))
	modTypes := loadModuleTypes(t, filepath.Join(dir, "module_types.json"))

	err := schematool.Merge(schema, modTypes)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error does not mention conflict: %v", err)
	}
}

func TestDecodeModuleTypes_UnknownField(t *testing.T) {
	input := `{"name":"m","unknownField":true}`
	_, err := schematool.DecodeModuleTypes(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("expected 'unknown field' in error, got: %v", err)
	}
}

func TestInspect(t *testing.T) {
	schema := loadSchema(t, filepath.Join("testdata", "single_object", "expected.json"))

	t.Run("ListTypes all", func(t *testing.T) {
		got := schematool.ListTypes(schema, "")
		if len(got) < 3 {
			t.Errorf("expected >=3 types, got %d", len(got))
		}
	})
	t.Run("ListTypes filter", func(t *testing.T) {
		got := schematool.ListTypes(schema, "OBJECT")
		for _, name := range got {
			if schematool.DescribeType(schema, name).Kind != "OBJECT" {
				t.Errorf("filter returned non-OBJECT type %q", name)
			}
		}
	})
	t.Run("HasType", func(t *testing.T) {
		if !schematool.HasType(schema, "Echo") {
			t.Error("Echo should exist")
		}
		if schematool.HasType(schema, "Nonexistent") {
			t.Error("Nonexistent should not exist")
		}
	})
	t.Run("DescribeType", func(t *testing.T) {
		got := schematool.DescribeType(schema, "Echo")
		if got == nil {
			t.Fatal("Echo missing")
		}
		if got.Name != "Echo" {
			t.Errorf("wrong name: %s", got.Name)
		}
	})
}

func loadSchema(t *testing.T, path string) *introspection.Schema {
	t.Helper()
	data := readFile(t, path)
	var resp introspection.Response
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return resp.Schema
}

func loadModuleTypes(t *testing.T, path string) *schematool.ModuleTypes {
	t.Helper()
	data := readFile(t, path)
	mt, err := schematool.DecodeModuleTypes(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode module types: %v", err)
	}
	return mt
}

func marshal(t *testing.T, schema *introspection.Schema) []byte {
	t.Helper()
	out := struct {
		Schema        *introspection.Schema `json:"__schema"`
		SchemaVersion string                `json:"__schemaVersion"`
	}{Schema: schema, SchemaVersion: "test"}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// assertJSONEqual compares two JSON byte slices after canonical
// re-marshalling so formatting differences don't cause false negatives.
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
