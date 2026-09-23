package daggercmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
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

func TestArtifactListArguments(t *testing.T) {
	cmd := &cobra.Command{Use: "check"}
	registerCommandArtifactFlags(cmd)
	defs := artifact.Dimensions{
		{Identifier: "Go.tests", Name: "go-test", QualifiedName: "go-tests"},
		{Identifier: "Go.all", Name: "all", QualifiedName: "go-all"},
	}
	names := map[string]string{}
	for _, def := range defs {
		names[def.Identifier] = artifactDimensionFlagName(cmd, defs, def)
	}
	args, err := artifactCLIArguments(listedArtifact{DimensionKeys: []struct{ Dimension, Key string }{
		{Dimension: "Go.tests", Key: "TestFoo"},
		{Dimension: "Go.all", Key: "./app/bar"},
	}}, names, nil)
	require.NoError(t, err)
	require.Equal(t, "--go-test=TestFoo --go-all=./app/bar", args)
	for _, key := range []string{"a b", "$(echo injected)", "x; echo injected", "a'b", "a\nb", "!history", "a'b!c", "", "*.go"} {
		t.Run(key, func(t *testing.T) {
			args, err := artifactCLIArguments(listedArtifact{URI: "dag+check://go/tests", DimensionKeys: []struct{ Dimension, Key string }{{Dimension: "Go.tests", Key: key}}}, names, nil)
			require.NoError(t, err)
			require.NotContains(t, args, "\n")
			// printf receives arguments only. Shell syntax in a key must stay literal.
			program, err := syntax.NewParser().Parse(strings.NewReader("printf '%s\\0' "+args), "")
			require.NoError(t, err)
			var out bytes.Buffer
			shell, err := interp.New(interp.StdIO(nil, &out, nil))
			require.NoError(t, err)
			require.NoError(t, shell.Run(t.Context(), program))
			require.Equal(t, "--go-test="+key+"\x00dag+check://go/tests\x00", out.String())
		})
	}
}
