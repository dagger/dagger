package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteCheckListWithGenerateChecks(t *testing.T) {
	var out bytes.Buffer
	err := writeCheckList(&out, []*CheckInfo{
		{
			Name:        "lint",
			Description: "Run lint\nwith details",
			Type:        "check",
		},
		{
			Name:        "assets",
			Description: "Generate assets",
			Type:        "generate",
		},
	})
	require.NoError(t, err)

	text := out.String()
	require.Contains(t, text, "Type")
	require.Regexp(t, `lint\s+check\s+Run lint`, text)
	require.Regexp(t, `assets\s+generate\s+Generate assets`, text)
	require.NotContains(t, text, "with details")
	require.NotContains(t, text, "Generators")
}

func TestWriteCheckListWithoutGenerateChecks(t *testing.T) {
	var out bytes.Buffer
	err := writeCheckList(&out, []*CheckInfo{
		{
			Name:        "lint",
			Description: "Run lint",
			Type:        "check",
		},
	})
	require.NoError(t, err)

	text := out.String()
	require.NotContains(t, text, "Type")
	require.Regexp(t, `lint\s+Run lint`, text)
	require.NotContains(t, text, "Generators")
}
