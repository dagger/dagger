package daggercmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArtifactPreviewFlagsAreIndependent(t *testing.T) {
	const input = `[{"Module":"app","Path":["app","verify"],"Type":"Check","Collections":[],"Items":[{"Keys":[]}]}]`
	var links, table, diagnostics bytes.Buffer
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &links, &diagnostics, []string{"check", "-la", "-f=link"}))
	require.Equal(t, "dag+check://\n", links.String())
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &table, &diagnostics, []string{"check", "-l"}))
	require.Contains(t, table.String(), "MODULE")
	require.Contains(t, table.String(), "CHECK")
	require.NotContains(t, table.String(), "dag+")
}
