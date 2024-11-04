package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectNameNormalization(t *testing.T) {
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
		require.Equalf(t, "friendly-bard", NormalizeProjectName(input), "input: %s", input)
	}
}
