package gogenerator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A package loaded through a GOPACKAGESDRIVER, which go/packages gives no
// module, still has its module: the code generator reads its directory.
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

	pkg, _, err := loadPackage(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Module == nil {
		t.Fatal("the package has no module")
	}
	if pkg.Module.Path != "example.com/hello" || pkg.Module.Dir != dir {
		t.Errorf("module = %s in %s, want example.com/hello in %s", pkg.Module.Path, pkg.Module.Dir, dir)
	}
}
