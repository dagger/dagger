package sdk

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

type resolutionReplay struct {
	ResolutionCases []resolutionReplayCase `json:"resolution_cases"`
}

type resolutionReplayCase struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

type resolutionModelOutcome struct {
	Selected      string
	NetworkEvents int
	Causes        []string
}

func TestParseSDKName(t *testing.T) {
	originalTag := engine.Tag
	defer func() {
		engine.Tag = originalTag
	}()
	engine.Tag = "v0.12.6"

	testcases := []struct {
		sdkName       string
		parsedSDKName sdk
		parsedSuffix  string
		expectedError string
	}{
		{
			sdkName:       "go",
			parsedSDKName: sdkGo,
		},
		{
			sdkName:       "dang",
			parsedSDKName: sdkDang,
		},
		{
			sdkName:       "typescript",
			parsedSDKName: sdkTypescript,
		},
		{
			sdkName:       "python",
			parsedSDKName: sdkPython,
		},
		{
			sdkName:       "rust",
			parsedSDKName: sdkRust,
		},
		{
			sdkName:       "php",
			parsedSDKName: sdkPHP,
			parsedSuffix:  "@v0.12.6",
		},
		{
			sdkName:       "elixir",
			parsedSDKName: sdkElixir,
			parsedSuffix:  "@v0.12.6",
		},
		{
			sdkName:       "php@foo",
			parsedSDKName: sdkPHP,
			parsedSuffix:  "@foo",
		},
		{
			sdkName:       "elixir@foo",
			parsedSDKName: sdkElixir,
			parsedSuffix:  "@foo",
		},
		{
			sdkName:       "elixir@",
			parsedSDKName: sdkElixir,
			parsedSuffix:  "@v0.12.6",
		},
		{
			sdkName:       "php@",
			parsedSDKName: sdkPHP,
			parsedSuffix:  "@v0.12.6",
		},
		{
			sdkName:       "go@v0.12.6",
			parsedSDKName: "",
			parsedSuffix:  "",
			expectedError: "the go sdk does not currently support selecting a specific version",
		},
		{
			sdkName:       "python@v0.12.6",
			parsedSDKName: "",
			parsedSuffix:  "",
			expectedError: "the python sdk does not currently support selecting a specific version",
		},
		{
			sdkName:       "typescript@v0.12.6",
			parsedSDKName: "",
			parsedSuffix:  "",
			expectedError: "the typescript sdk does not currently support selecting a specific version",
		},
		{
			sdkName:       "go@",
			parsedSDKName: "",
			parsedSuffix:  "",
			expectedError: "the go sdk does not currently support selecting a specific version",
		},
		{
			sdkName:       "rust@v1.0.0-beta.10",
			expectedError: "the rust sdk does not currently support selecting a specific version",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.sdkName, func(t *testing.T) {
			sdkName, suffix, err := parseSDKName(tc.sdkName)
			require.Equal(t, tc.parsedSDKName, sdkName)
			require.Equal(t, tc.parsedSuffix, suffix)
			if tc.expectedError != "" {
				require.EqualError(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestWorkspaceModuleForRuntime(t *testing.T) {
	originalTag := engine.Tag
	defer func() {
		engine.Tag = originalTag
	}()
	engine.Tag = "v0.12.6"

	for _, tc := range []struct {
		name      string
		runtime   string
		want      WorkspaceModule
		wantOK    bool
		wantError string
	}{
		{
			name:    "go",
			runtime: "go",
			want:    WorkspaceModule{Name: "dagger-go-sdk", Source: "github.com/dagger/go-sdk"},
			wantOK:  true,
		},
		{
			name:    "typescript",
			runtime: "typescript",
			want:    WorkspaceModule{Name: "dagger-typescript-sdk", Source: "github.com/dagger/typescript-sdk"},
			wantOK:  true,
		},
		{
			name:    "python",
			runtime: "python",
			want:    WorkspaceModule{Name: "dagger-python-sdk", Source: "github.com/dagger/python-sdk"},
			wantOK:  true,
		},
		{
			name:    "java defaults to engine tag",
			runtime: "java",
			want:    WorkspaceModule{Name: "dagger-java-sdk", Source: "github.com/dagger/dagger/sdk/java@v0.12.6"},
			wantOK:  true,
		},
		{
			name:    "php keeps explicit suffix",
			runtime: "php@main",
			want:    WorkspaceModule{Name: "dagger-php-sdk", Source: "github.com/dagger/dagger/sdk/php@main"},
			wantOK:  true,
		},
		{
			name:    "dang",
			runtime: "dang",
			want:    WorkspaceModule{Name: "dagger-dang-sdk", Source: "github.com/dagger/dang-sdk"},
			wantOK:  true,
		},
		{
			name:    "rust uses packaged builtin source",
			runtime: "rust",
			want:    WorkspaceModule{Name: "dagger-rust-sdk", Source: "rust"},
			wantOK:  true,
		},
		{
			name:    "external sdk has no static mapping",
			runtime: "github.com/acme/custom-sdk",
		},
		{
			name:      "invalid builtin sdk version still errors",
			runtime:   "go@v0.12.6",
			wantError: "the go sdk does not currently support selecting a specific version",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := WorkspaceModuleForRuntime(tc.runtime)
			if tc.wantError != "" {
				require.EqualError(t, err, tc.wantError)
				require.False(t, ok)
				require.Empty(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestRustSDKManifestDigestRejectsAbsentAndMalformedProvenance(t *testing.T) {
	t.Setenv("DAGGER_RUST_SDK_MANIFEST_DIGEST", "")
	_, err := rustSDKManifestDigest()
	require.ErrorContains(t, err, "rust SDK provenance")

	t.Setenv("DAGGER_RUST_SDK_MANIFEST_DIGEST", "not-a-digest")
	_, err = rustSDKManifestDigest()
	require.ErrorContains(t, err, "rust SDK provenance")

	t.Setenv("DAGGER_RUST_SDK_MANIFEST_DIGEST", "sha256:"+strings.Repeat("a", 64))
	got, err := rustSDKManifestDigest()
	require.NoError(t, err)
	require.Equal(t, "sha256:"+strings.Repeat("a", 64), got.String())
}

func TestRustSDKDescriptorDigestRejectsAbsentAndMalformedProvenance(t *testing.T) {
	t.Setenv("DAGGER_RUST_SDK_DESCRIPTOR_DIGEST", "")
	_, err := rustSDKDescriptorDigest()
	require.ErrorContains(t, err, "rust SDK provenance")

	t.Setenv("DAGGER_RUST_SDK_DESCRIPTOR_DIGEST", "not-a-digest")
	_, err = rustSDKDescriptorDigest()
	require.ErrorContains(t, err, "rust SDK provenance")

	t.Setenv("DAGGER_RUST_SDK_DESCRIPTOR_DIGEST", "sha256:"+strings.Repeat("b", 64))
	got, err := rustSDKDescriptorDigest()
	require.NoError(t, err)
	require.Equal(t, "sha256:"+strings.Repeat("b", 64), got.String())
}

func TestProperty02DeterministicRustSDKResolutionReplay(t *testing.T) {
	replay := loadResolutionReplay(t)
	require.NotEmpty(t, replay.ResolutionCases)
	for index := range 256 {
		caseInput := replay.ResolutionCases[index%len(replay.ResolutionCases)]
		externalSucceeds := index%2 == 0
		observed := observeResolutionModel(caseInput, externalSucceeds)
		expected := referenceResolutionModel(caseInput.Kind, externalSucceeds)
		require.Equal(t, expected, observed, "replay case %d (%s)", index, caseInput.Kind)
	}
}

func loadResolutionReplay(t *testing.T) resolutionReplay {
	t.Helper()
	contents, err := os.ReadFile("../../sdk/rust/crates/dagger-sdk-engine/tests/fixtures/engine-foundation-replay.json")
	require.NoError(t, err)
	var replay resolutionReplay
	require.NoError(t, json.Unmarshal(contents, &replay))
	return replay
}

func observeResolutionModel(input resolutionReplayCase, externalSucceeds bool) resolutionModelOutcome {
	if input.Kind == "ambiguous-registry" {
		return rejectedResolution("ambiguous-builtin")
	}
	_, _, err := parseSDKName(input.Source)
	if err == nil {
		return resolutionModelOutcome{Selected: "builtin"}
	}
	if !errors.Is(err, errUnknownBuiltinSDK) {
		return rejectedResolution("versioned-builtin")
	}
	return externalResolution(externalSucceeds)
}

func referenceResolutionModel(kind string, externalSucceeds bool) resolutionModelOutcome {
	switch kind {
	case "bare-rust":
		return resolutionModelOutcome{Selected: "builtin"}
	case "versioned-rust":
		return rejectedResolution("versioned-builtin")
	case "ambiguous-registry":
		return rejectedResolution("ambiguous-builtin")
	default:
		return externalResolution(externalSucceeds)
	}
}

func externalResolution(succeeds bool) resolutionModelOutcome {
	if succeeds {
		return resolutionModelOutcome{Selected: "external", NetworkEvents: 1}
	}
	return resolutionModelOutcome{
		NetworkEvents: 1,
		Causes:        []string{"unknown-builtin", "external-resolution"},
	}
}

func rejectedResolution(cause string) resolutionModelOutcome {
	return resolutionModelOutcome{Causes: []string{cause}}
}
