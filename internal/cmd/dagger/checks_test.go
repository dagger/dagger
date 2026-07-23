package daggercmd

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

func TestValidateCheckSelection(t *testing.T) {
	t.Run("unfiltered empty selection is allowed", func(t *testing.T) {
		require.NoError(t, validateCheckSelection(nil, 0))
	})

	t.Run("non-empty selection is allowed", func(t *testing.T) {
		require.NoError(t, validateCheckSelection([]string{"lint"}, 1))
	})

	t.Run("single unmatched pattern fails", func(t *testing.T) {
		require.EqualError(t,
			validateCheckSelection([]string{"missing"}, 0),
			`no checks matched pattern "missing"`,
		)
	})

	t.Run("multiple unmatched patterns fail", func(t *testing.T) {
		require.EqualError(t,
			validateCheckSelection([]string{"missing-one", "missing-two"}, 0),
			`no checks matched any of the patterns: "missing-one", "missing-two"`,
		)
	})
}
