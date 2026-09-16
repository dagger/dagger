package gitref

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLooksRemote(t *testing.T) {
	for _, ref := range []string{"github.com/does/notexist@v1", "go.example/tools@v1", "https://host/repo", "git@github.com:team/repo"} {
		require.True(t, LooksRemote(ref), ref)
	}
	for _, ref := range []string{"./github.com/does/notexist@v1", "../module", "/module", "missing/module", "module.go"} {
		require.False(t, LooksRemote(ref), ref)
	}
}

func TestDisplayRef(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"go.example/tools@v1", "go.example/tools@v1"},
		{"https://token@example.com/repo.git#v1", "https://***@example.com/repo.git#v1"},
		{"https://user:password@example.com/repo@v1", "https://***@example.com/repo@v1"},
		{"ssh://git@example.com/repo.git#main:tools", "ssh://git@example.com/repo.git#main:tools"},
		{"ssh://git:password@example.com/repo.git#main", "ssh://git:***@example.com/repo.git#main"},
		{"git@example.com:repo.git#main", "git@example.com:repo.git#main"},
		{"https://example.com/repo?token=secret#v1", "https://example.com/repo?***#v1"},
		{"https://example.com/repo\nsecret", "https://example.com/repo�secret"},
	} {
		require.Equal(t, tc.want, DisplayRef(tc.input))
	}
}
