package daggercmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteCommandList(t *testing.T) {
	var out bytes.Buffer
	err := writeCommandList(&out, []commandListItem{
		{Name: "web", Comment: "Start the web server"},
		{Name: "worker"},
		{Name: "db", Comment: "Start postgres"},
	})
	require.NoError(t, err)

	require.Equal(t, "web      # Start the web server\nworker\ndb       # Start postgres\n", out.String())
}

func TestArtifactAddressesRemainWhole(t *testing.T) {
	var out bytes.Buffer
	err := writeCommandList(&out, []commandListItem{
		{Name: "dag://golang/test/lint", Comment: "Run lint"},
		{Name: "dag://assets/generate/stale", Comment: "Check generated files"},
	})
	require.NoError(t, err)
	require.Regexp(t, `(?m)^dag://golang/test/lint +# Run lint$`, out.String())
	require.Regexp(t, `(?m)^dag://assets/generate/stale +# Check generated files$`, out.String())
}
