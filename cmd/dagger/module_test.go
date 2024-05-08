package main

import (
	"net/url"
	"testing"

	"github.com/moby/buildkit/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestOriginToPath(t *testing.T) {
	// Define test cases for each parameter
	schemes := []string{"ssh://git", "git@", "https://", ""}
	rootURLs := []string{"github.com/shykes/daggerverse", "github.com/shykes/daggerverse.git"}
	paths := []string{"/foo/bar", ""}
	versions := []string{"@v0.9.1", ""}

	// Combine test cases
	var testCases []struct {
		origin string
		want   string
	}
	for _, scheme := range schemes {
		for _, rootURL := range rootURLs {
			for _, path := range paths {
				for _, version := range versions {
					origin := scheme + rootURL + path + version
					want := "github.com/shykes/daggerverse" + path + version
					testCases = append(testCases, struct {
						origin string
						want   string
					}{
						origin: origin,
						want:   want,
					})
				}
			}
		}
	}

	for _, tc := range testCases {
		got, err := originToPath(tc.origin)
		if err != nil {
			t.Errorf("originToPath(%q) returned an error: %v", tc.origin, err)
			continue
		}
		if got != tc.want {
			t.Errorf("originToPath(%q) = %q; want %q", tc.origin, got, tc.want)
		}
	}
}

// This covers cases that the full integ test in core/integration/module_test.go can't
// cover right now due to limitation in needing real SSH keys to test e2e.
func TestParseGit(t *testing.T) {
	for _, tc := range []struct {
		urlStr string
		want   *gitutil.GitURL
	}{
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "/shykes/daggerverse",
				Fragment: &gitutil.GitURLFragment{
					Ref: "main",
				},
				Remote: "ssh://git@github.com/shykes/daggerverse",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "/shykes/daggerverse.git",
				Fragment: &gitutil.GitURLFragment{
					Ref: "main",
				},
				Remote: "ssh://git@github.com/shykes/daggerverse.git",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse#v0.9.1",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "/shykes/daggerverse",
				Fragment: &gitutil.GitURLFragment{
					Ref: "v0.9.1",
				},
				Remote: "ssh://git@github.com/shykes/daggerverse",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git#v0.9.1",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "/shykes/daggerverse.git",
				Fragment: &gitutil.GitURLFragment{
					Ref: "v0.9.1",
				},
				Remote: "ssh://git@github.com/shykes/daggerverse.git",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse#v0.9.1:subdir1/subdir2",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "/shykes/daggerverse",
				Fragment: &gitutil.GitURLFragment{
					Ref:    "v0.9.1",
					Subdir: "subdir1/subdir2",
				},
				Remote: "ssh://git@github.com/shykes/daggerverse",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git#v0.9.1:subdir1/subdir2",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "/shykes/daggerverse.git",
				Fragment: &gitutil.GitURLFragment{
					Ref:    "v0.9.1",
					Subdir: "subdir1/subdir2",
				},
				Remote: "ssh://git@github.com/shykes/daggerverse.git",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "sipsma/daggerverse",
				Fragment: &gitutil.GitURLFragment{
					Ref: "main",
				},
				Remote: "git@github.com:sipsma/daggerverse",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse.git",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "sipsma/daggerverse.git",
				Fragment: &gitutil.GitURLFragment{
					Ref: "main",
				},
				Remote: "git@github.com:sipsma/daggerverse.git",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse#v0.9.1",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "sipsma/daggerverse",
				Fragment: &gitutil.GitURLFragment{
					Ref: "v0.9.1",
				},
				Remote: "git@github.com:sipsma/daggerverse",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse.git#v0.9.1",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "sipsma/daggerverse.git",
				Fragment: &gitutil.GitURLFragment{
					Ref: "v0.9.1",
				},
				Remote: "git@github.com:sipsma/daggerverse.git",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse#v0.9.1:subdir1/subdir2",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "sipsma/daggerverse",
				Fragment: &gitutil.GitURLFragment{
					Ref:    "v0.9.1",
					Subdir: "subdir1/subdir2",
				},
				Remote: "git@github.com:sipsma/daggerverse",
			},
		},
		{
			urlStr: "git@github.com:sipsma/daggerverse.git#v0.9.1:subdir1/subdir2",
			want: &gitutil.GitURL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "sipsma/daggerverse.git",
				Fragment: &gitutil.GitURLFragment{
					Ref:    "v0.9.1",
					Subdir: "subdir1/subdir2",
				},
				Remote: "git@github.com:sipsma/daggerverse.git",
			},
		},
	} {
		tc := tc
		t.Run(tc.urlStr, func(t *testing.T) {
			t.Parallel()
			parsedGit, err := parseGit(tc.urlStr)
			require.NoError(t, err)
			require.NotNil(t, parsedGit)
			require.Equal(t, tc.want, parsedGit)
		})
	}
}
