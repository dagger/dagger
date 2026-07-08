package sdk

import (
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

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
