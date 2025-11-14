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
