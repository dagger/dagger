package releaseversion

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ref     string
		version string
		wantErr string
	}{
		{
			name:    "long tag ref with prefix",
			ref:     "refs/tags/v1.0.0-beta.9",
			version: "1.0.0-beta.9",
		},
		{
			name:    "long tag ref without prefix",
			ref:     "refs/tags/1.2.3",
			version: "1.2.3",
		},
		{
			name:    "long branch ref",
			ref:     "refs/heads/v2.0.0",
			version: "2.0.0",
		},
		{
			name:    "short ref",
			ref:     "v3.4.5+build.6",
			version: "3.4.5+build.6",
		},
		{
			name:    "invalid version",
			ref:     "refs/tags/latest",
			wantErr: `source ref "refs/tags/latest" is not a semantic version`,
		},
		{
			name:    "missing minor and patch",
			ref:     "refs/tags/v1",
			wantErr: `source ref "refs/tags/v1" is not a semantic version`,
		},
		{
			name:    "missing patch",
			ref:     "refs/tags/v1.0",
			wantErr: `source ref "refs/tags/v1.0" is not a semantic version`,
		},
		{
			name:    "commit",
			ref:     "0123456789abcdef0123456789abcdef01234567",
			wantErr: `source ref "0123456789abcdef0123456789abcdef01234567" is not a semantic version`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			version, err := Parse(tt.ref)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Parse(%q) error = %v, want %q", tt.ref, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.ref, err)
			}
			if version != tt.version {
				t.Errorf("Parse(%q) = %q, want %q", tt.ref, version, tt.version)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		ref                 string
		as                  string
		collapsePrereleases bool
		collapsePatch       bool
		version             string
		rolling             bool
		wantErr             string
	}{
		{
			name:          "stable tag defaults to minor channel",
			ref:           "refs/tags/v0.21.9",
			collapsePatch: true,
			version:       "0.21",
			rolling:       true,
		},
		{
			name:                "prerelease tag defaults to prerelease minor channel",
			ref:                 "refs/tags/v1.0.0-beta.42",
			collapsePrereleases: true,
			collapsePatch:       true,
			version:             "1.0-beta",
			rolling:             true,
		},
		{
			name:                "explicit branch destination is exact and rolling",
			ref:                 "refs/heads/main",
			as:                  "1.0-beta",
			collapsePrereleases: true,
			collapsePatch:       true,
			version:             "1.0-beta",
			rolling:             true,
		},
		{
			name:                "explicit tag destination ignores collapsing",
			ref:                 "refs/tags/v0.21.9",
			as:                  "0.21.9",
			collapsePrereleases: true,
			collapsePatch:       true,
			version:             "0.21.9",
			rolling:             true,
		},
		{
			name:    "branch requires explicit destination",
			ref:     "refs/heads/main",
			wantErr: `source ref "refs/heads/main" is not a semantic version`,
		},
		{
			name:    "explicit destination must be semantic",
			ref:     "refs/heads/main",
			as:      "../1.0-beta",
			wantErr: `docs version "../1.0-beta" is not semantic`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			version, rolling, err := Resolve(tt.ref, tt.as, tt.collapsePrereleases, tt.collapsePatch)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Resolve() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if version != tt.version || rolling != tt.rolling {
				t.Errorf("Resolve() = (%q, %t), want (%q, %t)", version, rolling, tt.version, tt.rolling)
			}
		})
	}
}

func TestCollapsePrerelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version   string
		want      string
		collapsed bool
	}{
		{version: "1.0.0-beta.42", want: "1.0.0-beta", collapsed: true},
		{version: "1.0.0-beta.42+build.7", want: "1.0.0-beta+build.7", collapsed: true},
		{version: "4.2.0-demo.7", want: "4.2.0-demo", collapsed: true},
		{version: "1.2.3-alpha.preview.9", want: "1.2.3-alpha.preview", collapsed: true},
		{version: "1.0.0-rc", want: "1.0.0-rc"},
		{version: "1.0.0", want: "1.0.0"},
		{version: "not-semver", want: "not-semver"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			t.Parallel()
			got, collapsed := CollapsePrerelease(tt.version)
			if got != tt.want || collapsed != tt.collapsed {
				t.Errorf("CollapsePrerelease(%q) = (%q, %t), want (%q, %t)", tt.version, got, collapsed, tt.want, tt.collapsed)
			}
		})
	}
}

func TestCollapsePatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version   string
		want      string
		collapsed bool
	}{
		{version: "1.0.0-beta", want: "1.0-beta", collapsed: true},
		{version: "4.2.0-demo.42", want: "4.2-demo.42", collapsed: true},
		{version: "1.0.0", want: "1.0", collapsed: true},
		{version: "1.0.1", want: "1.0", collapsed: true},
		{version: "1.0.0-beta+build.7", want: "1.0-beta+build.7", collapsed: true},
		{version: "1.0", want: "1.0"},
		{version: "1", want: "1"},
		{version: "not-semver", want: "not-semver"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			t.Parallel()
			got, collapsed := CollapsePatch(tt.version)
			if got != tt.want || collapsed != tt.collapsed {
				t.Errorf("CollapsePatch(%q) = (%q, %t), want (%q, %t)", tt.version, got, collapsed, tt.want, tt.collapsed)
			}
		})
	}
}

func TestRename(t *testing.T) {
	t.Parallel()

	versions := []string{"1.0-beta", "0.21.4", "0.20.2"}
	got, err := Rename(versions, "0.21.4", "0.21")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.0-beta", "0.21", "0.20.2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Rename() = %v, want %v", got, want)
	}
	if versions[1] != "0.21.4" {
		t.Error("Rename mutated its input")
	}
}

func TestRenameRejectsInvalidChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		from    string
		to      string
		wantErr string
	}{
		{name: "missing source", from: "0.19.11", to: "0.19", wantErr: `docs version "0.19.11" does not exist`},
		{name: "existing destination", from: "0.21.4", to: "0.20.2", wantErr: `docs version "0.20.2" already exists`},
		{name: "same version", from: "0.21.4", to: "0.21.4", wantErr: `docs versions are both "0.21.4"`},
		{name: "invalid source", from: "../0.21.4", to: "0.21", wantErr: `docs version "../0.21.4" is not semantic`},
		{name: "invalid destination", from: "0.21.4", to: "../0.21", wantErr: `docs version "../0.21" is not semantic`},
	}

	versions := []string{"0.21.4", "0.20.2"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Rename(versions, tt.from, tt.to)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Rename() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestSortNewestFirst(t *testing.T) {
	t.Parallel()

	versions := []string{
		"0.21.4",
		"1.0-beta",
		"0.20.2",
		"0.21.9",
		"1.0",
	}
	want := []string{
		"1.0",
		"1.0-beta",
		"0.21.9",
		"0.21.4",
		"0.20.2",
	}

	got, err := SortNewestFirst(versions)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SortNewestFirst() = %v, want %v", got, want)
	}
	if versions[0] != "0.21.4" {
		t.Error("SortNewestFirst mutated its input")
	}
}

func TestSortNewestFirstRejectsNonSemanticVersion(t *testing.T) {
	t.Parallel()

	_, err := SortNewestFirst([]string{"1.0", "next"})
	if err == nil || err.Error() != `docs version "next" is not semantic` {
		t.Fatalf("SortNewestFirst() error = %v", err)
	}
}
