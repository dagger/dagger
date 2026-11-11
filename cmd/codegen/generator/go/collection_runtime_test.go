package gogenerator

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

func TestCollectionRuntimeState(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.26\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
import "encoding/json"
// +collection
type Items struct { Keys []string }
func (r Items) MarshalJSON() ([]byte, error) {
 var concrete struct { Keys []string }
 concrete.Keys = r.Keys
 return json.Marshal(&concrete)
}
func (r *Items) UnmarshalJSON(bs []byte) error {
 var concrete struct { Keys []string }
 if err := json.Unmarshal(bs, &concrete); err != nil { return err }
 r.Keys = concrete.Keys
 return nil
}
func main() {
 var items Items
 if err := json.Unmarshal([]byte("{\"Keys\":[\"a\"],\"__daggerCollectionBase\":\"original\"}"), &items); err != nil { panic(err) }
 copy := items
 copy.Keys = []string{"b"}
 b, err := json.Marshal(copy)
 if err != nil { panic(err) }
 println(string(b))
 // Positional literals, including elided types, still create new collections.
 type Alias = Items
 type ItemList []Alias
 type ItemMap map[string]*Alias
 type Derived Items
 fresh := []Items{{[]string{"c"}}, Items{[]string{"d"}}}
 fresh = append(fresh, Alias{[]string{"e"}})
 fresh = append(fresh, ItemList{{[]string{"f"}}}...)
 fresh = append(fresh, *ItemMap{"g": {[]string{"g"}}}["g"])
 fresh = append(fresh, Items(Derived{[]string{"h"}}))
 // A local type with the same name is not a collection.
 { type Items struct { N int }; _ = Items{1} }
 b, err = json.Marshal(fresh)
 if err != nil { panic(err) }
 println(string(b))
}
`), 0600))
	t.Chdir(dir)
	require.NoError(t, PrepareCollectionRuntime("."))
	cmd := exec.CommandContext(t.Context(), "go", "run", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Equal(t, "{\"Keys\":[\"b\"],\"__daggerCollectionBase\":\"original\"}\n[{\"Keys\":[\"c\"]},{\"Keys\":[\"d\"]},{\"Keys\":[\"e\"]},{\"Keys\":[\"f\"]},{\"Keys\":[\"g\"]},{\"Keys\":[\"h\"]}]\n", string(out))
}

// Preparing a collection's runtime loads its package through the export
// driver when the Go SDK configures one, asking it for the export data of
// what the package imports, which the driver serves from the prebuilt
// archives; and fails when the driver declines or doesn't report, rather
// than leave go/packages to compile every package the module imports.
func TestCollectionRuntimeThroughExportDriver(t *testing.T) {
	for _, tc := range []struct {
		name string
		// report is what the driver appends to its log, "" for nothing.
		report string
		// handled is whether the driver answers with the package.
		handled bool
		// wantErr is part of the error wanted, "" for none.
		wantErr string
	}{{
		name:    "declined",
		report:  `{"declined":"archives built by go1.26.0, go command is go1.26.1"}`,
		wantErr: "declined to serve codegen from the prebuilt archives: archives built by go1.26.0, go command is go1.26.1",
	}, {
		name:    "no report",
		handled: true,
		wantErr: "didn't serve codegen from the prebuilt archives",
	}, {
		name:    "served",
		report:  `{"roots":1}`,
		handled: true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			main := filepath.Join(dir, "main.go")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/items\n\ngo 1.26\n"), 0o644))
			require.NoError(t, os.WriteFile(main, []byte("package main\n\n// +collection\ntype Items struct{ Keys []string }\n\nfunc main() { _ = Items{nil} }\n"), 0o644))
			resp := []byte(`{"NotHandled":true}`)
			if tc.handled {
				var err error
				resp, err = json.Marshal(map[string]any{
					"Compiler": "gc",
					"Arch":     "amd64",
					"Roots":    []string{"example.com/items"},
					"Packages": []map[string]any{{
						"ID":              "example.com/items",
						"Name":            "main",
						"PkgPath":         "example.com/items",
						"GoFiles":         []string{main},
						"CompiledGoFiles": []string{main},
					}},
				})
				require.NoError(t, err)
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "resp.json"), resp, 0o644))
			script := "#!/bin/sh\ncat >" + filepath.Join(dir, "req.json") + "\n"
			if tc.report != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "report.json"), []byte(tc.report+"\n"), 0o644))
				script += `cat ` + filepath.Join(dir, "report.json") + ` >>"$GOLANGCI_EXPORT_DRIVER_LOG"` + "\n"
			}
			script += "cat " + filepath.Join(dir, "resp.json") + "\n"
			driver := filepath.Join(dir, "driver.sh")
			require.NoError(t, os.WriteFile(driver, []byte(script), 0o755))
			t.Setenv("GOPACKAGESDRIVER", driver)
			t.Setenv("GOLANGCI_EXPORT_ARCHIVES", filepath.Join(dir, "archives"))
			t.Setenv("GOFLAGS", "")
			t.Setenv("GOWORK", "off")

			err := PrepareCollectionRuntime(dir)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			var req packages.DriverRequest
			data, err := os.ReadFile(filepath.Join(dir, "req.json"))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &req))
			require.NotZero(t, req.Mode&packages.NeedExportFile, "the driver serves the archives' export data only when asked for it")
			src, err := os.ReadFile(main)
			require.NoError(t, err)
			require.Contains(t, string(src), collectionRuntimeBase+" string")
			require.Contains(t, string(src), `Items{nil, ""}`)
		})
	}
}
