package mod

import (
	"testing"
)

func TestParseArgument(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     *Require
		hasError bool
	}{
		{
			name:     "Random",
			in:       "abcd/bla@:/xyz",
			hasError: true,
		},
		{
			name: "Dagger repo",
			in:   "github.com/dagger/dagger",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "",
				version: "",
			},
		},
		{
			name: "Dagger repo with path",
			in:   "github.com/dagger/dagger/stdlib",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib",
				version: "",
			},
		},
		{
			name: "Dagger repo with longer path",
			in:   "github.com/dagger/dagger/stdlib/test/test",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "",
			},
		},
		{
			name: "Dagger repo with path and version",
			in:   "github.com/dagger/dagger/stdlib@v0.1",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib",
				version: "v0.1",
			},
		},
		{
			name: "Dagger repo with longer path and version tag",
			in:   "github.com/dagger/dagger/stdlib/test/test@v0.0.1",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "v0.0.1",
			},
		},
		{
			name: "Alpha Dagger repo with path",
			in:   "alpha.dagger.io/gcp/gke@v0.1.0-alpha.20",
			want: &Require{
				repo:    "alpha.dagger.io",
				path:    "/gcp/gke",
				version: "v0.1.0-alpha.20",

				cloneRepo: "github.com/dagger/dagger",
				clonePath: "/stdlib/gcp/gke",
			},
		},
		{
			name: "Alpha Dagger repo",
			in:   "alpha.dagger.io@v0.1.0-alpha.23",
			want: &Require{
				repo:    "alpha.dagger.io",
				path:    "",
				version: "v0.1.0-alpha.23",

				cloneRepo: "github.com/dagger/dagger",
				clonePath: "/stdlib",
			},
		},
		{
			name: "Dagger repo with longer path and commit version",
			in:   "github.com/dagger/dagger/stdlib/test/test@26a1d46d1b3c",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "26a1d46d1b3c",
			},
		},
		{
			name: "Custom git provider without folder",
			in:   "git.blocklayer.com/dagger/test.git@main",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "",
				version: "main",
			},
		},
		{
			name: "Custom git provider with folder and version",
			in:   "git.blocklayer.com/dagger/test.git/test@v1.1.0",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "/test",
				version: "v1.1.0",
			},
		},
		{
			name: "Custom git provider with folder and version",
			in:   "git.blocklayer.com/dagger/test.git/test@v1.1.0",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "/test",
				version: "v1.1.0",
			},
		},
		{
			name: "Custom git provider without folder",
			in:   "git.blocklayer.com/dagger/test.git",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "",
				version: "",
			},
		},
		{
			name: "Custom git provider with folder, no version",
			in:   "git.blocklayer.com/dagger/test.git/test",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "/test",
				version: "",
			},
		},
		{
			name: "Custom git provider with custom port, folder, and version",
			in:   "git.blocklayer.com:7999/ops/dagger.git/stuff/here@v5",
			want: &Require{
				repo:    "git.blocklayer.com:7999/ops/dagger",
				path:    "/stuff/here",
				version: "v5",
			},
		},
		// TODO: Add more tests for ports!
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := newRequire(c.in, "")
			if err != nil && c.hasError {
				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got.repo != c.want.repo {
				t.Errorf("repos differ %q: want %s, got %s", c.in, c.want.repo, got.repo)
			}

			if got.path != c.want.path {
				t.Errorf("paths differ %q: want %s, got %s", c.in, c.want.path, got.path)
			}

			if got.version != c.want.version {
				t.Errorf("versions differ (%q): want %s, got %s", c.in, c.want.version, got.version)
			}
		})
	}
}
