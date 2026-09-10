package daggercmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleListCommand(t *testing.T) {
	require.Equal(t, "List installed modules", installedCmd.Short)
	require.Nil(t, installedCmd.Flags().Lookup("source"))
	require.Nil(t, installedCmd.Flags().Lookup("installed"))
	require.Nil(t, installedCmd.Flags().Lookup("sdk"))
}

func TestPrintModuleList(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printModuleList(&out, []moduleListRow{
		{Name: "foo", Source: "./foo"},
		{Name: "bar", Source: "github.com/example/bar@v1.0.0"},
	}))
	require.Equal(t, "NAME  SOURCE\nfoo   ./foo\nbar   github.com/example/bar@v1.0.0\n", out.String())
}

func TestPrintEmptyModuleList(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printModuleList(&out, nil))
	require.Empty(t, out.String())
}
