package version

import (
	"strings"
	"testing"
)

func TestVersionFromEmbed(t *testing.T) {
	if Version == "" {
		t.Fatal("Version is empty; expected the contents of VERSION")
	}
	if Version != strings.TrimSpace(Version) {
		t.Errorf("Version has surrounding whitespace: %q", Version)
	}
}

func TestCanonical(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		dirty   bool
		want    string
	}{
		{"no commit", "0.21.3", "", false, "0.21.3"},
		{"clean", "0.21.3", "42424242deadbeef", false, "0.21.3+42424242"},
		{"dirty", "0.21.3", "42424242deadbeef", true, "0.21.3+42424242.dirty"},
		{"short commit", "0.21.3", "abc", false, "0.21.3+abc"},
		{"dirty without commit ignores dirty", "0.21.3", "", true, "0.21.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			swap(t, &Version, tt.version)
			swap(t, &Commit, tt.commit)
			swapBool(t, &Dirty, tt.dirty)

			got := Canonical()
			if got != tt.want {
				t.Errorf("Canonical() = %q, want %q", got, tt.want)
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
