package schema

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSDKName(t *testing.T) {
	testcases := []struct {
		sdkName       string
		parsedSDKName string
		parsedSuffix  string
	}{
		{
			sdkName:       SDKGo,
			parsedSDKName: SDKGo,
		},
		{
			sdkName:       SDKTypescript,
			parsedSDKName: SDKTypescript,
		},
		{
			sdkName:       SDKPython,
			parsedSDKName: SDKPython,
		},
		{
			sdkName:       SDKPHP,
			parsedSDKName: SDKPHP,
		},
		{
			sdkName:       SDKElixir,
			parsedSDKName: SDKElixir,
		},
		{
			sdkName:       "php@foo",
			parsedSDKName: "php",
			parsedSuffix:  "@foo",
		},
		{
			sdkName:       "elixir@foo",
			parsedSDKName: "elixir",
			parsedSuffix:  "@foo",
		},
		{
			sdkName:       "elixir@",
			parsedSDKName: "elixir",
		},
		{
			sdkName:       "php@",
			parsedSDKName: "php",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.sdkName, func(t *testing.T) {
			sdkName, suffix := parseSDKName(tc.sdkName)
			require.Equal(t, tc.parsedSDKName, sdkName)
			require.Equal(t, tc.parsedSuffix, suffix)
		})
	}
}

func TestInvalidBuiltinSDKError(t *testing.T) {
	err := getInvalidBuiltinSDKError("foobar")
	expected := fmt.Errorf(`unknown builtin sdk
The "foobar" SDK does not exist. The available SDKs are:
- go
- python
- typescript
- php
- elixir
- any non-bundled SDK from its git ref (e.g. github.com/dagger/dagger/sdk/elixir@main)`)

	require.Equal(t, expected.Error(), err.Error())
	require.True(t, errors.Is(err, errUnknownBuiltinSDK))
}
