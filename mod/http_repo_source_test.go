package mod

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
)

func TestDownload(t *testing.T) {
	const URL = "https://github.com/dagger/dagger/archive/refs/tags/v0.2.7.tar.gz"

	cases := []struct {
		name    string
		require Require
		auth    string
	}{
		{
			name: "download archives",
			require: Require{
				version: "v0.2.7",
				source: &HTTPRepoSource{
					repo: URL,
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpDir, err := ioutil.TempDir("", "clone")
			if err != nil {
				t.Fatal("error creating tmp dir")
			}

			defer os.Remove(tmpDir)

			err = download(context.TODO(), URL, tmpDir, c.auth, false)
			if err != nil {
				t.Error(err)
			}
		})
	}
}

func TestParseHTTPArgument(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     *Require
		hasError bool
	}{
		{
			name: "HTTPS Dagger repo",
			in:   "https://github.com/dagger/dagger#/archive/refs/tags/v0.2.7.tar.gz",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "",
				version: "",
				source: &HTTPRepoSource{
					repo: "https://github.com/dagger/dagger/archive/refs/tags/v0.2.7.tar.gz",
				},
			},
		},
		{
			name: "HTTPS Dagger repo without hash-slash",
			in:   "https://github.com/dagger/dagger#archive/refs/tags/v0.2.7.tar.gz",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "",
				version: "",
				source: &HTTPRepoSource{
					repo: "https://github.com/dagger/dagger/archive/refs/tags/v0.2.7.tar.gz",
				},
			},
		},
		{
			name: "HTTP Dagger repo",
			in:   "http://github.com/dagger/dagger#/archive/refs/tags/v0.2.7.tar.gz",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "",
				version: "",
				source: &HTTPRepoSource{
					repo: "http://github.com/dagger/dagger/archive/refs/tags/v0.2.7.tar.gz",
				},
			},
		},
		{
			name: "HTTP Dagger repo without hash-slash",
			in:   "http://github.com/dagger/dagger#archive/refs/tags/v0.2.7.tar.gz",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "",
				version: "",
				source: &HTTPRepoSource{
					repo: "http://github.com/dagger/dagger/archive/refs/tags/v0.2.7.tar.gz",
				},
			},
		},
		{
			name: "HTTP Dagger repo with at",
			in:   "http://test:foo@github.com/dagger/dagger#/archive/refs/tags/v0.2.7.tar.gz",
			want: &Require{
				repo:    "github.com/dagger/dagger",
				path:    "",
				version: "",
				source: &HTTPRepoSource{
					repo: "http://test:foo@github.com/dagger/dagger/archive/refs/tags/v0.2.7.tar.gz",
				},
			},
		},
		{
			name: "HTTP Dagger repo with port",
			in:   "http://github.com:1234/dagger/dagger#/archive/refs/tags/v0.2.7.tar.gz",
			want: &Require{
				repo:    "github.com:1234/dagger/dagger",
				path:    "",
				version: "",
				source: &HTTPRepoSource{
					repo: "http://github.com:1234/dagger/dagger/archive/refs/tags/v0.2.7.tar.gz",
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
