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
