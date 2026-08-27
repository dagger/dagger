package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectLatestContainerTag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		tags    []string
		want    string
		wantErr string
	}{
		{
			name: "stable release",
			tags: []string{"latest", "edge", "v1.9.0", "2.0.0", "v3.0.0-rc.1"},
			want: "2.0.0",
		},
		{
			name:    "optional v prefix is ambiguous",
			tags:    []string{"1.2.3", "v1.2.3"},
			wantErr: "ambiguous latest release v1.2.3",
		},
		{
			name:    "optional v prefix beats incomplete alias handling",
			tags:    []string{"1.2", "v1.2.0"},
			wantErr: "ambiguous latest release v1.2.0",
		},
		{
			name: "complete version wins over incomplete alias",
			tags: []string{"v1.2", "v1.2.0"},
			want: "v1.2.0",
		},
		{
			name: "incomplete version remains eligible",
			tags: []string{"v1.1.9", "v1.2"},
			want: "v1.2",
		},
		{
			name: "most complete alias wins",
			tags: []string{"1", "1.0", "1.0.0"},
			want: "1.0.0",
		},
		{
			name:    "calver is ambiguous with normalized semver",
			tags:    []string{"24.04", "24.4.0"},
			wantErr: "ambiguous latest release v24.4.0",
		},
		{
			name:    "zero padding is ambiguous",
			tags:    []string{"01.002.0003", "1.2.3"},
			wantErr: "ambiguous latest release v1.2.3",
		},
		{
			name:    "zero-padded trailing component is not an incomplete alias",
			tags:    []string{"1.2", "1.2.00"},
			wantErr: "ambiguous latest release v1.2.0",
		},
		{
			name: "calver release",
			tags: []string{"24.04", "24.04.3", "25.10"},
			want: "25.10",
		},
		{
			name: "only prereleases",
			tags: []string{"edge", "v2.0.0-rc.1"},
			want: "latest",
		},
		{
			name: "no tags",
			want: "latest",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := SelectLatestContainerTag(tc.tags)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestValidateContainerLatestTag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		tag     string
		wantErr string
	}{
		{name: "stable", tag: "3.22.1"},
		{name: "stable with v", tag: "v3.22.1"},
		{name: "incomplete", tag: "v3.22"},
		{name: "calver", tag: "24.04"},
		{name: "latest fallback", tag: "latest"},
		{name: "prerelease", tag: "v4.0.0-rc.1", wantErr: "not a stable semantic version"},
		{name: "non-semver", tag: "edge", wantErr: "not a semantic version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateContainerLatestTag(tc.tag)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
