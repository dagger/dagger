package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRendersCliCaseNameAsDangType(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "main.dang")
	outPath := filepath.Join(dir, "out", "main.dang")

	if err := os.WriteFile(templatePath, []byte("type __SDK_NAME__ {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{"my-sdk", templatePath, outPath}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "type MySdk {") {
		t.Fatalf("rendered template mismatch:\n%s", got)
	}
}

func TestRunRejectsNameThatCannotProduceDangType(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "main.dang")
	outPath := filepath.Join(dir, "out", "main.dang")

	if err := os.WriteFile(templatePath, []byte("type __SDK_NAME__ {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := run([]string{"123-sdk", templatePath, outPath})
	if err == nil {
		t.Fatal("expected invalid root type name to fail")
	}
	if !strings.Contains(err.Error(), "valid Dang root type") {
		t.Fatalf("unexpected error: %v", err)
	}
}
