package main

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOriginToPath(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   string
	}{
		{
			origin: "ssh://git@github.com/shykes/daggerverse",
			want:   "github.com/shykes/daggerverse",
		},
		{
			origin: "ssh://git@github.com/shykes/daggerverse.git",
			want:   "github.com/shykes/daggerverse",
		},
		{
			origin: "git@github.com:sipsma/daggerverse",
			want:   "github.com/sipsma/daggerverse",
		},
		{
			origin: "git@github.com:sipsma/daggerverse.git",
			want:   "github.com/sipsma/daggerverse",
		},
		{
			origin: "https://github.com/sipsma/daggerverse",
			want:   "github.com/sipsma/daggerverse",
		},
		{
			origin: "https://github.com/sipsma/daggerverse.git",
			want:   "github.com/sipsma/daggerverse",
		},
	} {
		p, err := originToPath(tc.origin)
		require.NoError(t, err)
		require.Equal(t, tc.want, p)
	}
}

// This covers cases that the full integ test in core/integration/module_test.go can't
// cover right now due to limitation in needing real SSH keys to test e2e.
func TestParseGit(t *testing.T) {
	for _, tc := range []struct {
		urlStr string
		want   parsedGitURL
	}{
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/shykes/daggerverse",
				},
				ref: "main",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/shykes/daggerverse.git",
				},
				ref: "main",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse#v0.9.1",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/shykes/daggerverse",
				},
				ref: "v0.9.1",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git#v0.9.1",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/shykes/daggerverse.git",
				},
				ref: "v0.9.1",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse#v0.9.1:subdir1/subdir2",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/shykes/daggerverse",
				},
				ref:    "v0.9.1",
				subdir: "subdir1/subdir2",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git#v0.9.1:subdir1/subdir2",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/shykes/daggerverse.git",
				},
				ref:    "v0.9.1",
				subdir: "subdir1/subdir2",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/sipsma/daggerverse",
				},
				ref: "main",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse.git",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/sipsma/daggerverse.git",
				},
				ref: "main",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse#v0.9.1",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/sipsma/daggerverse",
				},
				ref: "v0.9.1",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse.git#v0.9.1",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/sipsma/daggerverse.git",
				},
				ref: "v0.9.1",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse#v0.9.1:subdir1/subdir2",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/sipsma/daggerverse",
				},
				ref:    "v0.9.1",
				subdir: "subdir1/subdir2",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse.git#v0.9.1:subdir1/subdir2",
			want: parsedGitURL{
				url: &url.URL{
					Scheme: "ssh",
					User:   url.User("git"),
					Host:   "github.com",
					Path:   "/sipsma/daggerverse.git",
				},
				ref:    "v0.9.1",
				subdir: "subdir1/subdir2",
			},
		},
	} {
		tc := tc
		t.Run(tc.urlStr, func(t *testing.T) {
			t.Parallel()
			parsedGit, err := parseGit(tc.urlStr)
			require.NoError(t, err)
			require.NotNil(t, parsedGit)
			require.Equal(t, tc.want, *parsedGit)
		})
	}
}
