package version

import (
	"strings"
	"testing"
)

func TestVersionFromEmbed(t *testing.T) {
	if version == "" {
		t.Fatal("version is empty; expected the contents of VERSION")
	}
	if version != strings.TrimSpace(version) {
		t.Errorf("version has surrounding whitespace: %q", version)
	}
	if strings.HasPrefix(version, "v") {
		t.Errorf("version retains a leading %q: %q", "v", version)
	}
}

func TestVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		dirty   bool
		opts    []Opt
		want    string
	}{
		{"bare", "0.21.3", "", false, nil, "0.21.3"},
		{"v", "0.21.3", "", false, []Opt{WithV()}, "v0.21.3"},
		{"commit clean", "0.21.3", "42424242deadbeef", false, []Opt{WithCommit()}, "0.21.3+42424242"},
		{"commit dirty", "0.21.3", "42424242deadbeef", true, []Opt{WithCommit()}, "0.21.3+42424242.dirty"},
		{"short commit", "0.21.3", "abc", false, []Opt{WithCommit()}, "0.21.3+abc"},
		{"v and commit", "0.21.3", "42424242deadbeef", false, []Opt{WithV(), WithCommit()}, "v0.21.3+42424242"},
		{"commit unknown ignores commit and dirty", "0.21.3", "", true, []Opt{WithV(), WithCommit()}, "v0.21.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			swap(t, &version, tt.version)
			swap(t, &Commit, tt.commit)
			swapBool(t, &Dirty, tt.dirty)

			got := Version(tt.opts...)
			if got != tt.want {
				t.Errorf("Version(%v) = %q, want %q", tt.opts, got, tt.want)
			}
		})
	}
}

func TestCommitState(t *testing.T) {
	tests := []struct {
		name   string
		commit string
		dirty  bool
		want   string
	}{
		{"no commit", "", false, ""},
		{"clean", "42424242deadbeef", false, "42424242"},
		{"dirty", "42424242deadbeef", true, "42424242+dirty"},
		{"short commit", "abc", false, "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			swap(t, &Commit, tt.commit)
			swapBool(t, &Dirty, tt.dirty)

			got := CommitState()
			if got != tt.want {
				t.Errorf("CommitState() = %q, want %q", got, tt.want)
			}
		})
	}
}

func swap[T any](t *testing.T, p *T, v T) {
	t.Helper()
	prev := *p
	*p = v
	t.Cleanup(func() { *p = prev })
}

func swapBool(t *testing.T, p *bool, v bool) {
	t.Helper()
	prev := *p
	*p = v
	t.Cleanup(func() { *p = prev })
}
