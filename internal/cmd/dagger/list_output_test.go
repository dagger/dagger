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

func TestGeneratedCheckComment(t *testing.T) {
	require.Equal(t, `Did you "generate assets"?`, generatedCheckComment("Generate assets."))
	require.Equal(t, `Did you "regenerate docs"?`, generatedCheckComment("Regenerate docs:\nwith details"))
	require.Empty(t, generatedCheckComment(""))
}
