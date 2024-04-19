package build

import (
	"testing"

	"github.com/moby/buildkit/client"
	"github.com/stretchr/testify/require"
)

func TestParseImportCache(t *testing.T) {
	type testCase struct {
		importCaches []string // --import-cache
		expected     []client.CacheOptionsEntry
		expectedErr  string
	}
	testCases := []testCase{
		{
			importCaches: []string{"type=registry,ref=example.com/foo/bar", "type=local,src=/path/to/store"},
			expected: []client.CacheOptionsEntry{
				{
					Type: "registry",
					Attrs: map[string]string{
						"ref": "example.com/foo/bar",
					},
				},
				{
					Type: "local",
					Attrs: map[string]string{
						"src": "/path/to/store",
					},
				},
			},
		},
		{
			importCaches: []string{"example.com/foo/bar", "example.com/baz/qux"},
			expected: []client.CacheOptionsEntry{
				{
					Type: "registry",
					Attrs: map[string]string{
						"ref": "example.com/foo/bar",
					},
				},
				{
					Type: "registry",
					Attrs: map[string]string{
						"ref": "example.com/baz/qux",
					},
				},
			},
		},
		{
			importCaches: []string{"type=gha,url=https://foo.bar,token=foo"},
			expected: []client.CacheOptionsEntry{
				{
					Type: "gha",
					Attrs: map[string]string{
						"url":   "https://foo.bar",
						"token": "foo",
					},
				},
			},
		},
		{
			importCaches: []string{"type=gha"},
			expected: []client.CacheOptionsEntry{
				{
					Type: "gha",
					Attrs: map[string]string{
						"url":   "https://github.com/test", // Set from env below
						"token": "bar",                     // Set from env below
					},
				},
			},
		},
	}

	// Set values for GitHub parse cache
	t.Setenv("ACTIONS_CACHE_URL", "https://github.com/test")
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "bar")

	for _, tc := range testCases {
		im, err := ParseImportCache(tc.importCaches)
		if tc.expectedErr == "" {
			require.EqualValues(t, tc.expected, im)
		} else {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
		}
	}
}
