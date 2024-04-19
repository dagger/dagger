package build

import (
	"testing"

	"github.com/moby/buildkit/client"
	"github.com/stretchr/testify/require"
)

func TestParseExportCache(t *testing.T) {
	type testCase struct {
		exportCaches []string // --export-cache
		expected     []client.CacheOptionsEntry
		expectedErr  string
	}
	testCases := []testCase{
		{
			exportCaches: []string{"type=registry,ref=example.com/foo/bar"},
			expected: []client.CacheOptionsEntry{
				{
					Type: "registry",
					Attrs: map[string]string{
						"ref":  "example.com/foo/bar",
						"mode": "min",
					},
				},
			},
		},
		{
			exportCaches: []string{"example.com/foo/bar"},
			expected: []client.CacheOptionsEntry{
				{
					Type: "registry",
					Attrs: map[string]string{
						"ref":  "example.com/foo/bar",
						"mode": "min",
					},
				},
			},
		},
		// TODO: test multiple exportCaches (valid for CLI but not supported by solver)

	}
	for _, tc := range testCases {
		ex, err := ParseExportCache(tc.exportCaches)
		if tc.expectedErr == "" {
			require.EqualValues(t, tc.expected, ex)
		} else {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
		}
	}
}
