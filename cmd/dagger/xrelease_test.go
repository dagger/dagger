package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestXReleaseReleaseRef(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ref         string
		downloadRef string
		engineRef   string
		ok          bool
	}{
		{
			name:        "release with v",
			ref:         "v0.20.8",
			downloadRef: "0.20.8",
			engineRef:   "v0.20.8",
			ok:          true,
		},
		{
			name:        "release without v",
			ref:         "0.20.8",
			downloadRef: "0.20.8",
			engineRef:   "v0.20.8",
			ok:          true,
		},
		{
			name:        "prerelease",
			ref:         "v0.21.0-beta.1",
			downloadRef: "0.21.0-beta.1",
			engineRef:   "v0.21.0-beta.1",
			ok:          true,
		},
		{
			name: "main",
			ref:  "main",
		},
		{
			name: "sha",
			ref:  "74bff7d10fd78dd6935c60c4514558598f216451",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			downloadRef, engineRef, ok := xReleaseReleaseRef(tc.ref)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.downloadRef, downloadRef)
			require.Equal(t, tc.engineRef, engineRef)
		})
	}
}

func TestXReleaseProcessEnvClearsRunnerOverrides(t *testing.T) {
	env := xReleaseProcessEnv([]string{
		daggerXReleaseEnv + "=v0.20.8",
		RunnerHostEnv + "=docker-container://dagger-engine.dev",
		RunnerImageLoaderEnv + "=docker",
		"KEEP=1",
	})

	require.Equal(t, []string{
		"KEEP=1",
		"DAGGER_LEAVE_OLD_ENGINE=1",
	}, env)
}
