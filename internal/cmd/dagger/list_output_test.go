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

func TestArtifactListArguments(t *testing.T) {
	cmd := &cobra.Command{Use: "check"}
	registerCommandArtifactFlags(cmd)
	defs := artifact.Dimensions{
		{Identifier: "go/tests", Name: "go-test", QualifiedName: "go-tests"},
		{Identifier: "type:Check", Kind: "TYPE", Name: "check", QualifiedName: "artifact-check"},
		{Identifier: "go/all", Name: "all", QualifiedName: "go-all"},
	}
	names := map[string]string{}
	for id, allocated := range artifactDimensionFlagNames(cmd, defs) {
		names[id] = allocated.Key
	}
	args, err := artifactCLIArguments(listedArtifact{DimensionKeys: []struct{ Dimension, Key string }{
		{Dimension: "go/tests", Key: "TestFoo"},
		{Dimension: "go/all", Key: "./app/bar"},
	}}, names, nil)
	require.NoError(t, err)
	require.Equal(t, "--go-test=TestFoo --go-all=./app/bar", args)
	for _, key := range []string{"a b", "$(echo injected)", "x; echo injected", "a'b", "a\nb", "!history", "a'b!c", "", "*.go"} {
		t.Run(key, func(t *testing.T) {
			args, err := artifactCLIArguments(listedArtifact{URI: "dag+check://go/tests", DimensionKeys: []struct{ Dimension, Key string }{{Dimension: "go/tests", Key: key}, {Dimension: "type:Check", Key: "go/tests"}}, DisplayKeys: map[string]string{"type:Check": "tests"}}, names, nil)
			require.NoError(t, err)
			require.NotContains(t, args, "\n")
			// printf receives arguments only. Shell syntax in a key must stay literal.
			program, err := syntax.NewParser().Parse(strings.NewReader("printf '%s\\0' "+args), "")
			require.NoError(t, err)
			var out bytes.Buffer
			shell, err := interp.New(interp.StdIO(nil, &out, nil))
			require.NoError(t, err)
			require.NoError(t, shell.Run(t.Context(), program))
			require.Equal(t, "--go-test="+key+"\x00--check=tests\x00", out.String())
		})
	}
}
