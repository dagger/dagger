package vcs

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil/types"
)

func TestConvertRef(t *testing.T) {
	bkClientDirFalse := &MockBuildkitClient{
		StatFunc: func(ctx context.Context, path string, followLinks bool) (*types.Stat, error) {
			return &types.Stat{Mode: uint32(os.ModeDevice)}, nil // stat.Dir() returns false
		},
	}

	testCases := []struct {
		name           string
		urlStr         string
		expectedUrlStr string
	}{
		// New ref style - basic ref
		{
			name:           "basic",
			urlStr:         "github.com/sipsma/daggerverse",
			expectedUrlStr: "https://github.com/sipsma/daggerverse",
		},
		{
			name:           "basic with .git",
			urlStr:         "github.com/sipsma/daggerverse.git",
			expectedUrlStr: "https://github.com/sipsma/daggerverse.git",
		},

		// New ref style - basic ref with subdir
		{
			name:           "basic with subdir",
			urlStr:         "github.com/sipsma/daggerverse/subdir1/subdir2",
			expectedUrlStr: "https://github.com/sipsma/daggerverse:subdir1/subdir2",
		},
		{
			name:           "basic with subdir and .git",
			urlStr:         "github.com/sipsma/daggerverse.git/subdir1/subdir2",
			expectedUrlStr: "https://github.com/sipsma/daggerverse.git:subdir1/subdir2",
		},

		// New ref style - basic ref with version
		{
			name:           "basic with version",
			urlStr:         "github.com/sipsma/daggerverse@v0.9.1",
			expectedUrlStr: "https://github.com/sipsma/daggerverse#v0.9.1",
		},
		{
			name:           "basic with version and .git",
			urlStr:         "github.com/sipsma/daggerverse.git@v0.9.1",
			expectedUrlStr: "https://github.com/sipsma/daggerverse.git#v0.9.1",
		},

		//New ref style - basic ref version and subdir
		{
			name:           "basic with subdir and version",
			urlStr:         "github.com/sipsma/daggerverse/subdir1/subdir2@v0.9.1",
			expectedUrlStr: "https://github.com/sipsma/daggerverse#v0.9.1:subdir1/subdir2",
		},
		{
			name:           "basic with subdir, version and .git",
			urlStr:         "github.com/sipsma/daggerverse.git/subdir1/subdir2@v0.9.1",
			expectedUrlStr: "https://github.com/sipsma/daggerverse.git#v0.9.1:subdir1/subdir2",
		},
		// other CI
		{
			name:           "GitLab with subdir",
			urlStr:         "gitlab.com/grouville-public/subgroup/daggerverse/zip",
			expectedUrlStr: "https://gitlab.com/grouville-public/subgroup/daggerverse:zip",
		},
		{
			name:           "GitLab with subdir, with .git",
			urlStr:         "gitlab.com/grouville-public/subgroup/daggerverse.git/zip",
			expectedUrlStr: "https://gitlab.com/grouville-public/subgroup/daggerverse.git:zip",
		},

		// Vanity ref
		{
			name:           "Vanity ref with version",
			urlStr:         "dagger.io/dagger@v0.9.1",
			expectedUrlStr: "https://github.com/dagger/dagger-go-sdk#v0.9.1",
		},

		// Edge cases
		{
			name:           "Local path",
			urlStr:         "./foo/bar",
			expectedUrlStr: "./foo/bar",
		},
		{
			name:           "Local path with view",
			urlStr:         "./foo/bar:view",
			expectedUrlStr: "./foo/bar:view",
		},
		{
			name:           "Implicit local path with view",
			urlStr:         "foo/bar:view",
			expectedUrlStr: "foo/bar:view",
		},
		{
			name:           "Implicit local path",
			urlStr:         "foo/bar",
			expectedUrlStr: "foo/bar",
		},
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
			convertedUrlStr, _ := ConvertToBuildKitRef(context.TODO(), tc.urlStr, bkClientDirFalse, ParseRefStringDir)
			require.Equal(t, tc.expectedUrlStr, convertedUrlStr)
		})
	}
}
