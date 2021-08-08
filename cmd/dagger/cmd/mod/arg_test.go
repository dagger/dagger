package mod

import "testing"

func TestParseArgument(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     *require
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
			want: &require{
				repo:    "github.com/dagger/dagger",
				path:    "",
				version: "",
			},
		},
		{
			name: "Dagger repo with path",
			in:   "github.com/dagger/dagger/stdlib",
			want: &require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib",
				version: "",
			},
		},
		{
			name: "Dagger repo with longer path",
			in:   "github.com/dagger/dagger/stdlib/test/test",
			want: &require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "",
			},
		},
		{
			name: "Dagger repo with path and version",
			in:   "github.com/dagger/dagger/stdlib@v0.1",
			want: &require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib",
				version: "v0.1",
			},
		},
		{
			name: "Dagger repo with longer path and version",
			in:   "github.com/dagger/dagger/stdlib/test/test@v0.0.1",
			want: &require{
				repo:    "github.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "v0.0.1",
			},
		},
		{
			name: "Alpha Dagger repo",
			in:   "alpha.dagger.io/gcp/gke@v0.1.0-alpha.20",
			want: &require{
				repo:    "alpha.dagger.io",
				path:    "/gcp/gke",
				version: "v0.1.0-alpha.20",

				cloneRepo: "github.com/dagger/dagger",
				clonePath: "/stdlib/gcp/gke",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseArgument(c.in)
			if err != nil && c.hasError {
				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got.repo != c.want.repo {
				t.Errorf("repos differ: want %s, got %s", c.want.repo, got.repo)
			}

			if got.path != c.want.path {
				t.Errorf("paths differ: want %s, got %s", c.want.path, got.path)
			}

			if got.version != c.want.version {
				t.Errorf("versions differ: want %s, got %s", c.want.version, got.version)
			}
		})
	}
}
