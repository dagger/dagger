package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRelativePath(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}

	tempDir := t.TempDir()
	relDir, err := filepath.Rel(cwd, tempDir)
	if err != nil {
		t.Fatalf("relative temp dir: %v", err)
	}

	dbPath := filepath.Join(relDir, "odag.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open with relative path %q: %v", dbPath, err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	if err := st.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tempDir, "odag.db")); err != nil {
		t.Fatalf("expected sqlite db file to exist: %v", err)
	}
}
