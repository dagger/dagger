package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceFindDisplayPath(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		entry  string
		want   string
	}{
		{"current directory file", ".", "file.txt", "./file.txt"},
		{"current directory child", ".", "sub/", "./sub/"},
		{"relative target", "sub", "deep/file.txt", "sub/deep/file.txt"},
		{"absolute target", "/sub", "deep/file.txt", "/sub/deep/file.txt"},
		{"workspace root", "/", "sub/file.txt", "/sub/file.txt"},
		{"parent target", "..", "file.txt", "../file.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, workspaceFindDisplayPath(tc.target, tc.entry))
		})
	}
}

func TestWorkspaceFindNameMatches(t *testing.T) {
	for _, tc := range []struct {
		name     string
		target   string
		patterns []string
		want     bool
	}{
		{"no patterns", "sub/file.go", nil, true},
		{"file match", "sub/file.go", []string{"*.go"}, true},
		{"directory match", "sub/vendor/", []string{"vendor"}, true},
		{"hidden match", "sub/.hidden", []string{".*"}, true},
		{"base name only", "sub/file.go", []string{"sub/*"}, false},
		{"any pattern", "sub/file.go", []string{"*.md", "*.go"}, true},
		{"no match", "sub/file.go", []string{"*.md"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, workspaceFindNameMatches(tc.target, tc.patterns))
		})
	}
}
