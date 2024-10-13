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
		"FrIeNdLy-..-bArD",
		"friendly--bard",
		"_friendly . bard_",
	}
	for _, input := range inputs {
		// require.Equal(t, "friendly-bard",  NormalizeProjectName(input)
		require.Equalf(t, "friendly-bard", NormalizeProjectName(input), "input: %s", input)
	}
}
