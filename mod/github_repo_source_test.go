package mod

import (
	"context"
	"testing"
)

func TestGithubGetVersions(t *testing.T) {
	ctx := context.TODO()

	repo := &GithubRepoSource{
		owner: "dagger",
		repo:  "dagger",
		ref:   "",
	}

	tags, err := repo.getVersions(ctx)
	if err != nil {
		t.Error(err)
	}

	if len(tags) == 0 {
		t.Errorf("could not list repo tags")
	}

	for _, item := range tags {
		if item == "v0.2.7" {
			return
		}
	}

	t.Errorf("could not find v0.2.7 in tags")
}

func TestParseUniverseArgument(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     *Require
		hasError bool
	}{
		{
			name: "Alpha Dagger repo with path",
			in:   "universe.dagger.io/gcp/gke@v0.1.0-alpha.20",
			want: &Require{
				repo:    "universe.dagger.io",
				path:    "/gcp/gke",
				version: "v0.1.0-alpha.20",

				sourcePath: "pkg/universe.dagger.io/gcp/gke",
				source: &GithubRepoSource{
					owner: "dagger",
					repo:  "dagger",
				},
			},
		},
		{
			name: "Alpha Dagger repo",
			in:   "universe.dagger.io@v0.1.0-alpha.23",
			want: &Require{
				repo:    "universe.dagger.io",
				path:    "",
				version: "v0.1.0-alpha.23",

				sourcePath: "pkg/universe.dagger.io",
				source: &GithubRepoSource{
					owner: "dagger",
					repo:  "dagger",
				},
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

			if c.want.sourcePath != "" && got.sourcePath != c.want.sourcePath {
				t.Errorf("clone paths differ (%q): want %s, got %s", c.in, c.want.sourcePath, got.sourcePath)
			}

			wantGit := c.want.source.(*GithubRepoSource)
			gotGit := got.source.(*GithubRepoSource)

			if gotGit.owner != wantGit.owner {
				t.Errorf("clone owners differ (%q): want %s, got %s", c.in, wantGit.owner, gotGit.owner)
			}

			if gotGit.repo != wantGit.repo {
				t.Errorf("clone repos differ (%q): want %s, got %s", c.in, wantGit.repo, gotGit.repo)
			}
		})
	}
}

func TestParseGithubArgument(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     *Require
		hasError bool
	}{
		{
			name: "Dagger repo with path",
			in:   "github.com/dagger/dagger/stdlib@v1.2.7",
			want: &Require{
				repo:       "github.com/dagger/dagger",
				path:       "/stdlib",
				version:    "v1.2.7",
				sourcePath: "",
				source: &GithubRepoSource{
					owner: "dagger",
					repo:  "dagger",
					ref:   "v1.2.7",
				},
			},
		},
		{
			name: "Dagger repo with longer path",
			in:   "github.com/dagger/dagger/stdlib/test/test@v1.2.7",
			want: &Require{
				repo:       "github.com/dagger/dagger",
				path:       "/stdlib/test/test",
				version:    "v1.2.7",
				sourcePath: "",
				source: &GithubRepoSource{
					owner: "dagger",
					repo:  "dagger",
					ref:   "v1.2.7",
				},
			},
		},
		{
			name: "Dagger repo with path and version",
			in:   "github.com/dagger/dagger/stdlib@v0.1",
			want: &Require{
				repo:       "github.com/dagger/dagger",
				path:       "/stdlib",
				version:    "v0.1",
				sourcePath: "",
				source: &GithubRepoSource{
					owner: "dagger",
					repo:  "dagger",
					ref:   "v0.1",
				},
			},
		},
		{
			name: "Dagger repo with longer path and version tag",
			in:   "github.com/dagger/dagger/stdlib/test/test@v0.0.1",
			want: &Require{
				repo:       "github.com/dagger/dagger",
				path:       "/stdlib/test/test",
				version:    "v0.0.1",
				sourcePath: "",
				source: &GithubRepoSource{
					owner: "dagger",
					repo:  "dagger",
					ref:   "v0.0.1",
				},
			},
		},
		{
			name: "Dagger repo with longer path and commit version",
			in:   "github.com/dagger/dagger/stdlib/test/test@26a1d46d1b3c",
			want: &Require{
				repo:       "github.com/dagger/dagger",
				path:       "/stdlib/test/test",
				version:    "26a1d46d1b3c",
				sourcePath: "",
				source: &GithubRepoSource{
					owner: "dagger",
					repo:  "dagger",
					ref:   "26a1d46d1b3c",
				},
			},
		},
		{
			name: "Other repo with longer path and commit version",
			in:   "github.com/owner/repo/stdlib/test/test@26a1d46d1b3c",
			want: &Require{
				repo:       "github.com/owner/repo",
				path:       "/stdlib/test/test",
				version:    "26a1d46d1b3c",
				sourcePath: "",
				source: &GithubRepoSource{
					owner: "owner",
					repo:  "repo",
					ref:   "26a1d46d1b3c",
				},
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

			assertRequire(c.in, c.want, got, t)
		})
	}
}
