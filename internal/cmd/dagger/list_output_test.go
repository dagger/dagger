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

	text := out.String()
	require.Regexp(t, `(?m)^web\s+# Start the web server$`, text)
	require.Regexp(t, `(?m)^worker$`, text)
	require.Regexp(t, `(?m)^db\s+# Start postgres$`, text)
	require.NotContains(t, text, "Description")
}

func TestGeneratedCheckComment(t *testing.T) {
	require.Equal(t, `Did you "generate assets"?`, generatedCheckComment("Generate assets."))
	require.Equal(t, `Did you "regenerate docs"?`, generatedCheckComment("Regenerate docs:\nwith details"))
	require.Empty(t, generatedCheckComment(""))
}
