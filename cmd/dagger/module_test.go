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

// TestConvertRef tests the conversion of a ref to a BuildKit-compatible ref.
// This is used to convert a multi VCS ref to a BuildKit-compatible ref.
func TestConvertRef(t *testing.T) {
	testCases := []struct {
		name           string
		urlStr         string
		expectedUrlStr string
	}{
		// SSH ref without version or subdir
		{
			name:           "SSH ref without .git",
			urlStr:         "ssh://git@github.com/shykes/daggerverse",
			expectedUrlStr: "ssh://git@github.com/shykes/daggerverse",
		},
		{
			name:           "SSH ref with .git",
			urlStr:         "ssh://git@github.com/shykes/daggerverse.git",
			expectedUrlStr: "ssh://git@github.com/shykes/daggerverse.git",
		},

		// SSH ref with version
		{
			name:           "SSH ref with version",
			urlStr:         "ssh://git@github.com/shykes/daggerverse@v0.9.1",
			expectedUrlStr: "ssh://git@github.com/shykes/daggerverse#v0.9.1",
		},
		{
			name:           "SSH ref with .git and version",
			urlStr:         "ssh://git@github.com/shykes/daggerverse.git@v0.9.1",
			expectedUrlStr: "ssh://git@github.com/shykes/daggerverse.git#v0.9.1",
		},

		// SSH ref with version and subdir
		{
			name:           "SSH ref with subdir and version",
			urlStr:         "ssh://git@github.com/shykes/daggerverse/subdir1/subdir2@v0.9.1",
			expectedUrlStr: "ssh://git@github.com/shykes/daggerverse#v0.9.1:subdir1/subdir2",
		},
		{
			name:           "SSH ref with .git, subdir and version",
			urlStr:         "ssh://git@github.com/shykes/daggerverse.git/subdir1/subdir2@v0.9.1",
			expectedUrlStr: "ssh://git@github.com/shykes/daggerverse.git#v0.9.1:subdir1/subdir2",
		},

		// Implicit SSH ref without version or subdir
		{
			name:           "Git ref without .git",
			urlStr:         "git@github.com:sipsma/daggerverse",
			expectedUrlStr: "ssh://git@github.com:sipsma/daggerverse",
		},
		{
			name:           "Git ref with .git",
			urlStr:         "git@github.com:sipsma/daggerverse.git",
			expectedUrlStr: "ssh://git@github.com:sipsma/daggerverse.git",
		},

		// Implicit SSH ref with version
		{
			name:           "Git ref with version",
			urlStr:         "git@github.com:sipsma/daggerverse@v0.9.1",
			expectedUrlStr: "ssh://git@github.com:sipsma/daggerverse#v0.9.1",
		},
		{
			name:           "Git ref with .git and version",
			urlStr:         "git@github.com:sipsma/daggerverse.git@v0.9.1",
			expectedUrlStr: "ssh://git@github.com:sipsma/daggerverse.git#v0.9.1",
		},

		// Implicit SSH ref with version and subdir
		{
			name:           "Git ref with subdir and version",
			urlStr:         "git@github.com:sipsma/daggerverse/subdir1/subdir2@v0.9.1",
			expectedUrlStr: "ssh://git@github.com:sipsma/daggerverse#v0.9.1:subdir1/subdir2",
		},
		{
			name:           "Git ref with .git, subdir and version",
			urlStr:         "git@github.com:sipsma/daggerverse.git/subdir1/subdir2@v0.9.1",
			expectedUrlStr: "ssh://git@github.com:sipsma/daggerverse.git#v0.9.1:subdir1/subdir2",
		},

		// Explicit HTTPS ref with version and subdir
		{
			name:           "HTTPS ref with subdir and version",
			urlStr:         "https://github.com/sipsma/daggerverse/subdir1/subdir2@v0.9.1",
			expectedUrlStr: "https://github.com/sipsma/daggerverse#v0.9.1:subdir1/subdir2",
		},
		{
			name:           "HTTPS ref with .git, subdir and version",
			urlStr:         "https://github.com/sipsma/daggerverse.git/subdir1/subdir2@v0.9.1",
			expectedUrlStr: "https://github.com/sipsma/daggerverse.git#v0.9.1:subdir1/subdir2",
		},

		// Implicit HTTPS without subdir or version
		{
			name:           "ref without scheme with subdir",
			urlStr:         "github.com/sipsma/daggerverse",
			expectedUrlStr: "https://github.com/sipsma/daggerverse",
		},
		{
			name:           "ref without scheme with subdir and .git",
			urlStr:         "github.com/sipsma/daggerverse.git",
			expectedUrlStr: "https://github.com/sipsma/daggerverse.git",
		},

		// Implicit HTTPS with subdir
		{
			name:           "ref without scheme and subdir",
			urlStr:         "github.com/sipsma/daggerverse/subdir1/subdir2",
			expectedUrlStr: "https://github.com/sipsma/daggerverse:subdir1/subdir2",
		},
		{
			name:           "ref without scheme, subdir and version, with .git",
			urlStr:         "github.com/sipsma/daggerverse.git/subdir1/subdir2",
			expectedUrlStr: "https://github.com/sipsma/daggerverse.git:subdir1/subdir2",
		},

		// Implicit HTTPS with version
		{
			name:           "ref without scheme with version",
			urlStr:         "github.com/sipsma/daggerverse@v0.9.1",
			expectedUrlStr: "https://github.com/sipsma/daggerverse#v0.9.1",
		},
		{
			name:           "ref without scheme with version and .git",
			urlStr:         "github.com/sipsma/daggerverse.git@v0.9.1",
			expectedUrlStr: "https://github.com/sipsma/daggerverse.git#v0.9.1",
		},

		// Implicit HTTPS with version and subdir
		{
			name:           "ref without scheme with subdir and version",
			urlStr:         "github.com/sipsma/daggerverse/subdir1/subdir2@v0.9.1",
			expectedUrlStr: "https://github.com/sipsma/daggerverse#v0.9.1:subdir1/subdir2",
		},
		{
			name:           "ref without scheme with subdir and version and .git",
			urlStr:         "github.com/sipsma/daggerverse.git/subdir1/subdir2",
			expectedUrlStr: "https://github.com/sipsma/daggerverse.git:subdir1/subdir2",
		},

		// Vanity ref
		{
			name:           "Vanity ref with version",
			urlStr:         "dagger.io/dagger@v0.9.1",
			expectedUrlStr: "https://github.com/dagger/dagger-go-sdk#v0.9.1",
		},
		// { // with subdir, requires to modify dagger.io/dagger netlify's redirect
		// 	name:           "Vanity refL with version",
		// 	urlStr:         "dagger.io/dagger@v0.9.1",
		// 	expectedUrlStr: "https://github.com/dagger/dagger-go-sdk#v0.9.1",
		// },

		// Edge cases
		{
			name:           "Empty ref",
			urlStr:         "",
			expectedUrlStr: "",
		},
		{
			name:           "Invalid ref",
			urlStr:         "invalid-url",
			expectedUrlStr: "invalid-url",
		},
		{
			name:           "Invalid FTP scheme",
			urlStr:         "ftp://github.com/sipsma/daggerverse",
			expectedUrlStr: "ftp://github.com/sipsma/daggerverse",
		},

		// Retro-compatibility test cases
		{
			name:           "Retro SSH ref with version",
			urlStr:         "ssh://git@github.com/shykes/daggerverse#v0.9.1",
			expectedUrlStr: "ssh://git@github.com/shykes/daggerverse#v0.9.1",
		},
		{
			name:           "Retro SSH ref with .git and version",
			urlStr:         "ssh://git@github.com/shykes/daggerverse.git#v0.9.1",
			expectedUrlStr: "ssh://git@github.com/shykes/daggerverse.git#v0.9.1",
		},
		{
			name:           "Retro SSH ref with subdir and version",
			urlStr:         "ssh://git@github.com/shykes/daggerverse#v0.9.1:subdir1/subdir2",
			expectedUrlStr: "ssh://git@github.com/shykes/daggerverse#v0.9.1:subdir1/subdir2",
		},
		{
			name:           "Retro SSH ref with .git, subdir and version",
			urlStr:         "ssh://git@github.com/shykes/daggerverse.git#v0.9.1:subdir1/subdir2",
			expectedUrlStr: "ssh://git@github.com/shykes/daggerverse.git#v0.9.1:subdir1/subdir2",
		},
		{
			name:           "Retro Git ref with version",
			urlStr:         "git@github.com:sipsma/daggerverse#v0.9.1",
			expectedUrlStr: "git@github.com:sipsma/daggerverse#v0.9.1",
		},
		{
			name:           "Retro Git ref with .git and version",
			urlStr:         "git@github.com:sipsma/daggerverse.git#v0.9.1",
			expectedUrlStr: "git@github.com:sipsma/daggerverse.git#v0.9.1",
		},
		{
			name:           "Retro Git ref with subdir and version",
			urlStr:         "git@github.com:sipsma/daggerverse#v0.9.1:subdir1/subdir2",
			expectedUrlStr: "git@github.com:sipsma/daggerverse#v0.9.1:subdir1/subdir2",
		},
		{
			name:           "Retro Git ref with .git, subdir and version",
			urlStr:         "git@github.com:sipsma/daggerverse.git#v0.9.1:subdir1/subdir2",
			expectedUrlStr: "git@github.com:sipsma/daggerverse.git#v0.9.1:subdir1/subdir2",
		},
		{
			name:           "Retro no sheme ref with subdir and version",
			urlStr:         "github.com:sipsma/daggerverse#v0.9.1:subdir1/subdir2",
			expectedUrlStr: "github.com:sipsma/daggerverse#v0.9.1:subdir1/subdir2",
		},
		{
			name:           "Retro no scheme ref with .git, subdir and version",
			urlStr:         "github.com:sipsma/daggerverse.git#v0.9.1:subdir1/subdir2",
			expectedUrlStr: "github.com:sipsma/daggerverse.git#v0.9.1:subdir1/subdir2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			convertedUrlStr := vcsToBuildKitRef(tc.urlStr)
			require.Equal(t, tc.expectedUrlStr, convertedUrlStr)
		})
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
