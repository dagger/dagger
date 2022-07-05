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
			in:   "github.com/dagger/dagger.git/stdlib",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib",
				version: "",
			},
		},
		{
			name: "Dagger repo with longer path",
			in:   "github.com/dagger/dagger.git/stdlib/test/test",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "",
			},
		},
		{
			name: "Dagger repo with path and version",
			in:   "github.com/dagger/dagger.git/stdlib@v0.1",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib",
				version: "v0.1",
			},
		},
		{
			name: "Dagger repo with longer path and version tag",
			in:   "github.com/dagger/dagger.git/stdlib/test/test@v0.0.1",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "v0.0.1",
			},
		},
		{
			name: "Universe Dagger repo with path",
			in:   "universe.dagger.io/gcp/gke@v0.1.0-alpha.20",
			want: &Require{
				repo:    "universe.dagger.io",
				path:    "/gcp/gke",
				version: "v0.1.0-alpha.20",

				cloneRepo: "github.com/dagger/dagger",
				clonePath: "/stdlib/gcp/gke",
			},
		},
		{
			name: "Universe Dagger repo",
			in:   "universe.dagger.io@v0.1.0-alpha.23",
			want: &Require{
				repo:    "universe.dagger.io",
				path:    "",
				version: "v0.1.0-alpha.23",

				cloneRepo: "github.com/dagger/dagger",
				clonePath: "/stdlib",
			},
		},
		{
			name: "Dagger repo with longer path and commit version",
			in:   "github.com/dagger/dagger.git/stdlib/test/test@26a1d46d1b3c",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "26a1d46d1b3c",
			},
		},
		{
			name: "Custom git provider without path",
			in:   "git.blocklayer.com/dagger/test.git",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "",
				version: "",
			},
		},
		{
			name: "Custom git provider without path and with branch",
			in:   "git.blocklayer.com/dagger/test@main",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "",
				version: "main",
			},
		},
		{
			name: "Custom git provider with path and version",
			in:   "git.blocklayer.com/dagger/test.git/test@v1.1.0",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "/test",
				version: "v1.1.0",
			},
		},
		{
			name: "Custom git provider with path, no version",
			in:   "git.blocklayer.com/dagger/test.git/test",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "/test",
				version: "",
			},
		},
		{
			name: "Custom git provider with longer paths and version",
			in:   "git.blocklayer.com/dagger/lib/packages/test.git/test/example@v1.1.0",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/lib/packages/test",
				path:    "/test/example",
				version: "v1.1.0",
			},
		},
		{
			name: "Custom git provider with longer paths, no version",
			in:   "git.blocklayer.com/dagger/lib/packages/test.git/test/example",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/lib/packages/test",
				path:    "/test/example",
				version: "",
			},
		},
		{
			name: "Custom git provider with custom port, path, and version",
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
