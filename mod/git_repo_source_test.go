package mod

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
)

func TestClone(t *testing.T) {
	cases := []struct {
		name    string
		require Require
	}{
		{
			name: "resolving branch name",
			require: Require{
				version:    "main",
				sourcePath: "",
				source: &GitRepoSource{
					repo: "github.com/dagger/dagger-action",
				},
			},
		},
		{
			name: "resolving tag",
			require: Require{
				version:    "v1.0.0",
				sourcePath: "",
				source: &GitRepoSource{
					repo: "github.com/dagger/dagger-action",
				},
			},
		},
		// FIXME: disabled until we find a fix: "repo_test.go:56: ssh: handshake failed: knownhosts: key mismatch"
		// {
		// 	name: "dagger private repo",
		// 	require: Require{
		// 		cloneRepo: "github.com/dagger/test",
		// 		clonePath: "",
		// 		version:   "main",
		// 	},
		// 	privateKeyFile:     "./test-ssh-keys/id_ed25519_test",
		// 	privateKeyPassword: "",
		// },
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpDir, err := ioutil.TempDir("", "clone")
			if err != nil {
				t.Fatal("error creating tmp dir")
			}

			defer os.Remove(tmpDir)

			git := c.require.source.(*GitRepoSource)

			err = git.clone(context.TODO(), &c.require, tmpDir)
			if err != nil {
				t.Error(err)
			}
		})
	}
}

func TestListTags(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "clone")
	if err != nil {
		t.Fatal("error creating tmp dir")
	}
	defer os.Remove(tmpDir)

	ctx := context.TODO()

	req := Require{
		version:    "",
		sourcePath: "",
		source: &GitRepoSource{
			repo: "github.com/dagger/dagger-action",
		},
	}

	git := req.source.(*GitRepoSource)

	err = git.clone(ctx, &req, tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	tags, err := git.listTagVersions()
	if err != nil {
		t.Error(err)
	}

	if len(tags) == 0 {
		t.Errorf("could not list repo tags")
	}
}

func TestParseGitArgument(t *testing.T) {
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
			in:   "github1.com/dagger/dagger",
			want: &Require{
				repo:    "github1.com/dagger/dagger",
				path:    "",
				version: "",
				source: &GitRepoSource{
					repo: "github1.com/dagger/dagger",
				},
			},
		},
		{
			name: "Dagger repo with path",
			in:   "github1.com/dagger/dagger/stdlib",
			want: &Require{
				repo:    "github1.com/dagger/dagger",
				path:    "/stdlib",
				version: "",
				source: &GitRepoSource{
					repo: "github1.com/dagger/dagger",
				},
			},
		},
		{
			name: "Dagger repo with longer path",
			in:   "github1.com/dagger/dagger/stdlib/test/test",
			want: &Require{
				repo:    "github1.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "",
				source: &GitRepoSource{
					repo: "github1.com/dagger/dagger",
				},
			},
		},
		{
			name: "Dagger repo with path and version",
			in:   "github1.com/dagger/dagger/stdlib@v0.1",
			want: &Require{
				repo:    "github1.com/dagger/dagger",
				path:    "/stdlib",
				version: "v0.1",
				source: &GitRepoSource{
					repo: "github1.com/dagger/dagger",
				},
			},
		},
		{
			name: "Dagger repo with longer path and version tag",
			in:   "github1.com/dagger/dagger/stdlib/test/test@v0.0.1",
			want: &Require{
				repo:    "github1.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "v0.0.1",
				source: &GitRepoSource{
					repo: "github1.com/dagger/dagger",
				},
			},
		},
		{
			name: "Dagger repo with longer path and commit version",
			in:   "github1.com/dagger/dagger/stdlib/test/test@26a1d46d1b3c",
			want: &Require{
				repo:    "github1.com/dagger/dagger",
				path:    "/stdlib/test/test",
				version: "26a1d46d1b3c",
				source: &GitRepoSource{
					repo: "github1.com/dagger/dagger",
				},
			},
		},
		{
			name: "Custom git provider without folder",
			in:   "git.blocklayer.com/dagger/test.git@main",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "",
				version: "main",
				source: &GitRepoSource{
					repo: "git.blocklayer.com/dagger/test.git",
				},
			},
		},
		{
			name: "Custom git provider with folder and version",
			in:   "git.blocklayer.com/dagger/test.git/test@v1.1.0",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "/test",
				version: "v1.1.0",
				source: &GitRepoSource{
					repo: "git.blocklayer.com/dagger/test.git",
				},
			},
		},
		{
			name: "Custom git provider with folder and version",
			in:   "git.blocklayer.com/dagger/test.git/test@v1.1.0",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "/test",
				version: "v1.1.0",
				source: &GitRepoSource{
					repo: "git.blocklayer.com/dagger/test.git",
				},
			},
		},
		{
			name: "Custom git provider without folder",
			in:   "git.blocklayer.com/dagger/test.git",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "",
				version: "",
				source: &GitRepoSource{
					repo: "git.blocklayer.com/dagger/test.git",
				},
			},
		},
		{
			name: "Custom git provider with folder, no version",
			in:   "git.blocklayer.com/dagger/test.git/test",
			want: &Require{
				repo:    "git.blocklayer.com/dagger/test",
				path:    "/test",
				version: "",
				source: &GitRepoSource{
					repo: "git.blocklayer.com/dagger/test.git",
				},
			},
		},
		{
			name: "Custom git provider with custom port, folder, and version",
			in:   "git.blocklayer.com:7999/ops/dagger.git/stuff/here@v5",
			want: &Require{
				repo:    "git.blocklayer.com:7999/ops/dagger",
				path:    "/stuff/here",
				version: "v5",
				source: &GitRepoSource{
					repo: "git.blocklayer.com:7999/ops/dagger.git",
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
