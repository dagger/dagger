package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectNameNormalization(t *testing.T) {
	// Valid "dagger.json" names
	inputs := []string{
		"friendly-bard",
		"Friendly-Bard",
		"FRIENDLY-BARD",
		"friendly.bard",
		"friendly_bard",
		"friendly--bard",
		"friendly-.bard",
		"Friendly-..-bard",
		"friendly--bard",
		"_friendly . bard_",
		"--friendly_bard--",
		" friendly_bard ",
		"friendly bard",
		"Friendly Bard",
		"friendlyBard",
	}
	for _, input := range inputs {
		// require.Equal(t, "friendly-bard",  NormalizeProjectName(input)
		require.Equalf(t, "friendly-bard", NormalizeProjectNameFromModule(input), "input: %s", input)
	}
	require.Equal(t, "friendly-2", NormalizeProjectNameFromModule("friendly2"))
}

func TestPackageNameNormalization(t *testing.T) {
	// Valid "pyproject.toml" names
	inputs := []string{
		"friendly-bard",
		"Friendly-Bard",
		"FRIENDLY-BARD",
		"friendly.bard",
		"friendly_bard",
		"friendly--bard",
		"FrIeNdLy-._.-bArD",
	}
	for _, input := range inputs {
		// require.Equal(t, "friendly-bard",  NormalizeProjectName(input)
		require.Equalf(t, "friendly_bard", NormalizePackageName(input), "input: %s", input)
	}
	require.Equal(t, "friendly_2", NormalizePackageName("friendly-2"))
}

func TestParseSelfCalls(t *testing.T) {
	cases := []struct {
		name string
		json string
		want bool
	}{
		{
			name: "object sdk with SELF_CALLS true",
			json: `{"name":"t","sdk":{"source":"python","experimental":{"SELF_CALLS":true}}}`,
			want: true,
		},
		{
			name: "object sdk with SELF_CALLS false",
			json: `{"name":"t","sdk":{"source":"python","experimental":{"SELF_CALLS":false}}}`,
			want: false,
		},
		{
			name: "object sdk without experimental",
			json: `{"name":"t","sdk":{"source":"python"}}`,
			want: false,
		},
		{
			name: "bare string sdk",
			json: `{"name":"t","sdk":"python"}`,
			want: false,
		},
		{
			name: "malformed json",
			json: `{not json`,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, parseSelfCalls([]byte(tc.json)))
		})
	}
}
