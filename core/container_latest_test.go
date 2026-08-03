package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectLatestContainerTag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		tags []string
		want string
	}{
		{
			name: "stable release",
			tags: []string{"latest", "edge", "v1.9.0", "2.0.0", "v3.0.0-rc.1"},
			want: "2.0.0",
		},
		{
			name: "equivalent versions deterministic",
			tags: []string{"1.2.3", "v1.2.3"},
			want: "v1.2.3",
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
			require.Equal(t, tc.want, SelectLatestContainerTag(tc.tags))
		})
	}
}

func TestParseContainerLatestPin(t *testing.T) {
	t.Parallel()

	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name    string
		pin     string
		wantErr string
	}{
		{name: "stable", pin: "docker.io/library/alpine:3.22.1@" + digest},
		{name: "stable with v", pin: "docker.io/library/alpine:v3.22.1@" + digest},
		{name: "latest fallback", pin: "docker.io/library/alpine:latest@" + digest},
		{name: "missing digest", pin: "docker.io/library/alpine:3.22.1", wantErr: "has no digest"},
		{name: "missing tag", pin: "docker.io/library/alpine@" + digest, wantErr: "has no tag"},
		{name: "wrong repository", pin: "docker.io/library/busybox:1.0.0@" + digest, wantErr: "does not match"},
		{name: "prerelease", pin: "docker.io/library/alpine:v4.0.0-rc.1@" + digest, wantErr: "not a stable semantic version"},
		{name: "non-semver", pin: "docker.io/library/alpine:edge@" + digest, wantErr: "not a stable semantic version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ref, err := ParseContainerLatestPin(tc.pin, "docker.io/library/alpine")
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.pin, ref.String())
		})
	}
}
