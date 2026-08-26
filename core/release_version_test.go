package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectLatestReleaseTagIgnoresLowerAmbiguity(t *testing.T) {
	t.Parallel()

	selected, found, err := selectLatestReleaseTag([]releaseTagCandidate{
		{Name: "v1.2", Version: "v1.2"},
		{Name: "v1.2.0", Version: "v1.2.0"},
		{Name: "v2.0.0", Version: "v2.0.0"},
	})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "v2.0.0", selected.Name)
}

func TestSelectLatestReleaseTagReportsOriginalTags(t *testing.T) {
	t.Parallel()

	_, _, err := selectLatestReleaseTag([]releaseTagCandidate{
		{Name: "24.04", Version: "24.04"},
		{Name: "v24.4.0", Version: "v24.4.0"},
	})
	require.ErrorContains(t, err, `equivalent tags ["24.04" "v24.4.0"]`)
}

func TestParseReleaseTagPreservesPrefixedName(t *testing.T) {
	t.Parallel()

	got, ok := parseReleaseTag(releaseTagCandidate{
		Name:    "module/v24.04",
		Version: "v24.04",
	})
	require.True(t, ok)
	require.Equal(t, "module/v24.04", got.Name)
	require.Equal(t, "v24.04", got.Version)
	require.Equal(t, "v24.4.0", got.Semver.Canonical)
}

func TestPreferCanonicalGitReleaseTag(t *testing.T) {
	t.Parallel()

	const target = "1111111111111111111111111111111111111111"
	for _, tc := range []struct {
		name       string
		candidates []string
		want       string
	}{
		{
			name:       "v prefix",
			candidates: []string{"1.2.0", "v1.2.0"},
			want:       "v1.2.0",
		},
		{
			name:       "no padding before v prefix",
			candidates: []string{"v01.2.0", "1.2.0"},
			want:       "1.2.0",
		},
		{
			name:       "precision before v prefix",
			candidates: []string{"v1.2", "1.2.0"},
			want:       "1.2.0",
		},
		{
			name:       "no padding before precision",
			candidates: []string{"v1.2", "v1.2.00"},
			want:       "v1.2",
		},
		{
			name:       "no build metadata before v prefix",
			candidates: []string{"v1.2.0+linux", "1.2.0"},
			want:       "1.2.0",
		},
		{
			name:       "fewer leading zeroes",
			candidates: []string{"v001.2.0", "v01.2.0"},
			want:       "v01.2.0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			candidates := make([]releaseTagCandidate, len(tc.candidates))
			for i, tag := range tc.candidates {
				candidates[i] = releaseTagCandidate{
					Name:    tag,
					Version: tag,
					Target:  target,
				}
			}
			selected, found, err := selectLatestReleaseTag(candidates)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, tc.want, selected.Name)
		})
	}
}
