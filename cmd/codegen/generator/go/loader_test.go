package gogenerator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A package loaded through a GOPACKAGESDRIVER, which go/packages gives no
// module, still has its module: the code generator reads it from its go.mod,
// with no go command, which would read the module cache.
func TestLoadPackageThroughDriverHasModule(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{
		"go.mod":  "module example.com/hello\n\ngo 1.22\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A driver that answers with the package and nothing of its module,
	// as a driver's answer reaches go/packages.
	resp, err := json.Marshal(map[string]any{
		"Compiler": "gc",
		"Arch":     "amd64",
		"Roots":    []string{"example.com/hello"},
		"Packages": []map[string]any{{
			"ID":              "example.com/hello",
			"Name":            "main",
			"PkgPath":         "example.com/hello",
			"GoFiles":         []string{filepath.Join(dir, "main.go")},
			"CompiledGoFiles": []string{filepath.Join(dir, "main.go")},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resp.json"), resp, 0o644); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, "driver.sh")
	if err := os.WriteFile(driver, []byte("#!/bin/sh\ncat >/dev/null\ncat "+filepath.Join(dir, "resp.json")+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPACKAGESDRIVER", driver)
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	// A go command that runs nothing.
	noGo := t.TempDir()
	if err := os.WriteFile(filepath.Join(noGo, "go"), []byte("#!/bin/sh\necho \"go $*: no go command may run\" >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", noGo+string(os.PathListSeparator)+os.Getenv("PATH"))

	pkg, _, err := loadPackage(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Module == nil {
		t.Fatal("the package has no module")
	}
	if pkg.Module.Path != "example.com/hello" || pkg.Module.Dir != dir || pkg.Module.GoVersion != "1.22" || !pkg.Module.Main {
		t.Errorf("module = %+v, want example.com/hello, go 1.22, main, in %s", *pkg.Module, dir)
	}
}

// Codegen loading packages through the export driver fails when the driver
// declines, naming why, rather than leave go/packages to compile every
// package the module imports; and when the driver doesn't report at all.
func TestLoadPackageThroughExportDriver(t *testing.T) {
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
		report:  `{"declined":"manifest: manifest version 1, want 2"}`,
		wantErr: "declined to serve codegen from the prebuilt archives: manifest: manifest version 1, want 2",
	}, {
		name:    "no report",
		handled: true,
		wantErr: "didn't serve codegen from the prebuilt archives",
	}, {
		name:    "served",
		report:  `{"roots":1,"prebuilt":3,"missing":["example.com/hello"]}`,
		handled: true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{
				"go.mod":  "module example.com/hello\n\ngo 1.22\n",
				"main.go": "package main\n\nfunc main() {}\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			resp := []byte(`{"NotHandled":true}`)
			if tc.handled {
				var err error
				resp, err = json.Marshal(map[string]any{
					"Compiler": "gc",
					"Arch":     "amd64",
					"Roots":    []string{"example.com/hello"},
					"Packages": []map[string]any{{
						"ID":              "example.com/hello",
						"Name":            "main",
						"PkgPath":         "example.com/hello",
						"GoFiles":         []string{filepath.Join(dir, "main.go")},
						"CompiledGoFiles": []string{filepath.Join(dir, "main.go")},
					}},
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "resp.json"), resp, 0o644); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\ncat >/dev/null\n"
			if tc.report != "" {
				if err := os.WriteFile(filepath.Join(dir, "report.json"), []byte(tc.report+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				script += `cat ` + filepath.Join(dir, "report.json") + ` >>"$GOLANGCI_EXPORT_DRIVER_LOG"` + "\n"
			}
			script += "cat " + filepath.Join(dir, "resp.json") + "\n"
			driver := filepath.Join(dir, "driver.sh")
			if err := os.WriteFile(driver, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GOPACKAGESDRIVER", driver)
			t.Setenv("GOLANGCI_EXPORT_ARCHIVES", filepath.Join(dir, "archives"))
			t.Setenv("GOFLAGS", "")
			t.Setenv("GOWORK", "off")

			pkg, _, err := loadPackage(context.Background(), dir, false)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if pkg.Name != "main" {
					t.Errorf("package name = %q, want main", pkg.Name)
				}
				return
			}
			if err == nil {
				t.Fatalf("loaded package %s, want an error", pkg.PkgPath)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
