package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathsFromRegistry(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "authorities.json")
	contents := []byte(`{"authorities":{"go-client":{"include":[{"path":{"path":"z.go"}},{"path":{"path":"a.go"}}]}}}`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := PathsFromRegistry(path, "go-client")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths, []string{"a.go", "z.go"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PathsFromRegistry() = %v, want %v", got, want)
	}
}

func TestPathsFromRegistryRejectsSymbolSelector(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "authorities.json")
	contents := []byte(`{"authorities":{"go-client":{"include":[{"symbol":{"path":"client.go","locator":"Client"}}]}}}`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PathsFromRegistry(path, "go-client"); err == nil {
		t.Fatal("PathsFromRegistry accepted a non-path selector")
	}
}
