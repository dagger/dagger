package daggercmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/internal/hotline"
)

func TestSelectedFiles(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir("src", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("src/main.go", []byte("selected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("private.txt", []byte("not selected"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := collectFiles([]string{"./src/main.go"})
	if err != nil || len(files) != 1 || files[0].Path != "src/main.go" || string(files[0].Data) != "selected" {
		t.Fatalf("snapshot: %+v %v", files, err)
	}
	os.WriteFile("src/main.go", []byte("changed"), 0600)
	if string(files[0].Data) != "selected" {
		t.Fatal("snapshot changed")
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0600)
	if err = os.Symlink(filepath.Join(outside, "secret"), "outside-link"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"src", "../secret", "outside-link", filepath.Join(outside, "secret"), "missing"} {
		if _, err = collectFiles([]string{path}); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if _, err = collectFiles([]string{"src/main.go", "src/main.go"}); err == nil {
		t.Fatal("accepted duplicate")
	}
	os.WriteFile("large", make([]byte, hotline.MaxAttachmentBytes+1), 0600)
	if _, err = collectFiles([]string{"large"}); err == nil {
		t.Fatal("accepted large file")
	}
}
