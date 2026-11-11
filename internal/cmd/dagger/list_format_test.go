package daggercmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestArtifactListFormats(t *testing.T) {
	names := map[string]string{"/go/modules": "go-module", "type:Check": "check", "module": "module"}
	row := func(path, key, name string) listedArtifact {
		return listedArtifact{
			URI:           "dag+check://" + path + "?go-module=" + key,
			Description:   "Check files\nExtra detail",
			Name:          name,
			DimensionKeys: []struct{ Dimension, Key string }{{"module", "go"}, {"/go/modules", key}, {"type:Check", path}},
			Coordinates:   []struct{ Dimension, Key string }{{"module", "go"}, {"/go/modules", key}, {"name", name}},
			DisplayKeys:   map[string]string{"type:Check": strings.TrimPrefix(name, "generate.")},
		}
	}
	items := []listedArtifact{row("go/modules/generate/stale", ".", "generate.stale"), row("go/modules/test", ".", "test")}
	render := func(format string, rows []listedArtifact) string {
		cmd := &cobra.Command{Use: "check"}
		cmd.Flags().String("format", format, "")
		var out bytes.Buffer
		cmd.SetOut(&out)
		require.NoError(t, writeArtifactList(cmd, rows, names))
		return out.String()
	}
	t.Run("table preserves local field path and removes constant columns", func(t *testing.T) {
		out := render("table", items)
		require.Regexp(t, `(?m)^NAME +DESCRIPTION +LINK$`, out)
		require.Regexp(t, `(?m)^generate\.stale +Check files +dag\+check://go/modules/generate/stale`, out)
		require.NotContains(t, out, "GO-MODULE")
		require.NotContains(t, out, "MODULE")
	})
	t.Run("cli emits executable commands with full selectors", func(t *testing.T) {
		require.Equal(t, "dagger check --module=go --go-module=. --check=stale   # Check files\ndagger check --module=go --go-module=. --check=test    # Check files\n", render("cli", items))
	})
	t.Run("link retains identity", func(t *testing.T) {
		require.Equal(t, items[0].URI+"\n"+items[1].URI+"\n", render("link", items))
	})
	t.Run("single row retains its link", func(t *testing.T) {
		out := render("table", items[:1])
		require.Regexp(t, `(?m)^DESCRIPTION +LINK$`, out)
		require.NotContains(t, out, "NAME")
		require.Contains(t, out, items[0].URI)
	})
	t.Run("cli omission does not change table coordinates or links", func(t *testing.T) {
		item := items[0]
		item.OmitCLITypeKey = true
		require.Equal(t, "dagger check --module=go --go-module=.   # Check files\n", render("cli", []listedArtifact{item}))
		require.Equal(t, render("table", items), render("table", []listedArtifact{item, items[1]}))
		require.Equal(t, item.URI+"\n", render("link", []listedArtifact{item}))
	})
	t.Run("duplicate selections print once", func(t *testing.T) {
		require.Equal(t, items[0].URI+"\n", render("link", []listedArtifact{items[0], items[0]}))
	})
	t.Run("absolute output carries workspace and revision", func(t *testing.T) {
		item := items[0]
		item.URI = "dag+check://github.com/acme/ws@abc:go/modules/generate/stale?go-module=."
		require.Contains(t, render("table", []listedArtifact{item}), "github.com/acme/ws@abc")
		require.Equal(t, "dagger check -W github.com/acme/ws@abc --module=go --go-module=. --check=stale   # Check files\n", render("cli", []listedArtifact{item}))
	})
	t.Run("a long listing reaches stdout in a few writes", func(t *testing.T) {
		var rows []listedArtifact
		for i := range 2000 {
			rows = append(rows, row(fmt.Sprintf("go/modules/test/case%04d", i), ".", fmt.Sprintf("case%04d", i)))
		}
		for _, format := range []string{"table", "link", "cli"} {
			cmd := &cobra.Command{Use: "check"}
			cmd.Flags().String("format", format, "")
			var writes recordedWrites
			cmd.SetOut(&writes)
			require.NoError(t, writeArtifactList(cmd, rows, names))
			text := strings.Join(writes, "")
			lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
			if format == "table" {
				require.Regexp(t, `^NAME +DESCRIPTION +LINK$`, lines[0])
				lines = lines[1:]
			}
			require.Len(t, lines, len(rows), format)
			for i, line := range lines {
				require.Contains(t, line, fmt.Sprintf("case%04d", i), format)
			}
			require.LessOrEqual(t, len(writes), len(text)/(32<<10)+1, format)
		}
	})
	t.Run("a long listing is cut only between lines", func(t *testing.T) {
		item := row("go/modules/test", ".", "test")
		item.Description = strings.Repeat("a", 32741) + "é"
		next := row("go/modules/test2", ".", "test2")
		next.Description = item.Description
		cmd := &cobra.Command{Use: "check"}
		cmd.Flags().String("format", "table", "")
		var writes recordedWrites
		cmd.SetOut(&writes)
		require.NoError(t, writeArtifactList(cmd, []listedArtifact{item, next}, names))
		require.Equal(t, render("table", []listedArtifact{item, next}), strings.Join(writes, ""))
		require.Greater(t, len(writes), 1)
		for _, write := range writes {
			require.True(t, utf8.ValidString(write))
			require.True(t, strings.HasSuffix(write, "\n"))
		}
	})
}

func TestArtifactListColumnSelection(t *testing.T) {
	names := map[string]string{"module": "module", "/tools/items": "tool", "/tools/variants": "variant"}
	rows := []listedArtifact{
		{URI: "dag+check://first", Coordinates: []struct{ Dimension, Key string }{{"module", "tools"}, {"/tools/items", ""}, {"/tools/variants", "linux"}, {"name", "generate.stale"}}},
		{URI: "dag+check://second", Coordinates: []struct{ Dimension, Key string }{{"module", "other"}, {"/tools/variants", "linux"}, {"name", "test"}}},
	}
	cmd := &cobra.Command{Use: "check"}
	cmd.Flags().String("format", "table", "")
	var out bytes.Buffer
	cmd.SetOut(&out)
	require.NoError(t, writeArtifactList(cmd, rows, names))
	require.Regexp(t, `(?m)^TOOL +MODULE +NAME +LINK$`, out.String())
	require.Regexp(t, `(?m)^"" +tools +generate\.stale`, out.String())
	require.NotContains(t, out.String(), "VARIANT")
	require.NotContains(t, out.String(), "linux")
	// Rendering must not modify metadata used by the resolver or CLI output.
	require.Equal(t, "", rows[0].Coordinates[1].Key)
	require.Equal(t, "generate.stale", rows[0].Coordinates[3].Key)
}

func TestArtifactListCLICommand(t *testing.T) {
	root := &cobra.Command{Use: "dagger"}
	list := &cobra.Command{Use: "list"}
	collection := &cobra.Command{Use: "go-modules", Annotations: map[string]string{artifactListCollection: "GoModules"}}
	root.AddCommand(list)
	list.AddCommand(collection)
	collection.Flags().String("format", "cli", "")
	var out bytes.Buffer
	collection.SetOut(&out)
	item := listedArtifact{URI: "dag+go-module://?go-module=one", DimensionKeys: []struct{ Dimension, Key string }{{"/go/modules", "one"}}}
	require.NoError(t, writeArtifactList(collection, []listedArtifact{item}, map[string]string{"/go/modules": "go-module"}))
	require.Equal(t, "dagger list go-modules --go-module=one\n", out.String())
}

func TestArtifactMixedTypeListCLI(t *testing.T) {
	root := &cobra.Command{Use: "dagger"}
	list := &cobra.Command{Use: "list"}
	root.AddCommand(list)
	list.Flags().String("format", "cli", "")
	items := []listedArtifact{
		{URI: "dag+container://", Name: "container", DimensionKeys: []struct{ Dimension, Key string }{{"module", "tools"}, {"type:Container", "tools/container"}}},
		{URI: "dag+check://", Name: "verify", DimensionKeys: []struct{ Dimension, Key string }{{"module", "tools"}, {"type:Check", "tools/verify"}}},
	}
	check := items[1]
	var output bytes.Buffer
	list.SetOut(&output)
	require.NoError(t, writeArtifactList(list, items, map[string]string{"type:Check": "check", "type:Container": "container"}))
	require.Equal(t, "dagger list --module=tools --check=tools/verify\ndagger list --module=tools --container=tools/container\n", output.String())
	child := &cobra.Command{Use: "checks", Annotations: map[string]string{artifactListType: "Check"}}
	list.AddCommand(child)
	child.Flags().String("format", "cli", "")
	output.Reset()
	child.SetOut(&output)
	require.NoError(t, writeArtifactList(child, []listedArtifact{check}, map[string]string{"type:Check": "check", "type:Container": "container"}))
	require.Equal(t, "dagger list checks --module=tools --check=tools/verify\n", output.String())
}

func TestArtifactListCLIReplay(t *testing.T) {
	cmd := &cobra.Command{Use: "check"}
	cmd.Flags().String("format", "cli", "")
	cmd.Flags().Bool("generated", true, "")
	cmd.Flags().StringArray("skip", nil, "")
	require.NoError(t, cmd.ParseFlags([]string{"--generated=false", "--skip=other/*"}))
	key := "a'b!c\n$(echo injected)"
	item := listedArtifact{
		URI: "dag+check://verify", OmitCLITypeKey: true,
		DimensionKeys: []struct{ Dimension, Key string }{{"module", "tools"}, {"/tools/items", key}, {"type:Check", "verify"}},
	}
	var output bytes.Buffer
	cmd.SetOut(&output)
	require.NoError(t, writeArtifactList(cmd, []listedArtifact{item}, map[string]string{"/tools/items": "item"}))
	require.NotContains(t, strings.TrimSuffix(output.String(), "\n"), "\n")
	program, err := syntax.NewParser().Parse(strings.NewReader(output.String()), "")
	require.NoError(t, err)
	var argv []string
	shell, err := interp.New(interp.ExecHandler(func(_ context.Context, args []string) error {
		argv = append(argv, args...)
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, shell.Run(t.Context(), program))
	require.Equal(t, []string{"dagger", "check", "--module=tools", "--item=" + key, "--generated=false", "--skip=other/*"}, argv)
}

// recordedWrites keeps each write separately, as stdout emits each write as
// its own log record.
type recordedWrites []string

func (w *recordedWrites) Write(p []byte) (int, error) {
	*w = append(*w, string(p))
	return len(p), nil
}

func TestArtifactListWorkspaceReplay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		uri       string
		workspace string
	}{
		{name: "explicit workspace", args: []string{"-W", "github.com/acme/ws@main"}, workspace: "github.com/acme/ws@main"},
		{name: "literal path", args: []string{"-W", "/tmp/a'b!c\n$(echo injected)"}, workspace: "/tmp/a'b!c\n$(echo injected)"},
		{name: "absolute input", args: []string{"dag+check://github.com/acme/ws@abc:verify"}, workspace: "github.com/acme/ws@abc"},
		{name: "absolute input overrides flag", args: []string{"-W", "other@main", "dag+check://github.com/acme/ws@abc:verify"}, workspace: "github.com/acme/ws@abc"},
		{name: "absolute row supplies pinned workspace once", args: []string{"-W", "github.com/acme/ws@main"}, uri: "dag+check://github.com/acme/ws@abc:verify", workspace: "github.com/acme/ws@abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{Use: "dagger"}
			root.PersistentFlags().StringP("workspace", "W", "", "")
			cmd := &cobra.Command{Use: "check"}
			root.AddCommand(cmd)
			cmd.Flags().String("format", "cli", "")
			require.NoError(t, cmd.ParseFlags(tc.args))
			uri := tc.uri
			if uri == "" {
				uri = "dag+check://verify"
			}
			item := listedArtifact{URI: uri, DimensionKeys: []struct{ Dimension, Key string }{{"type:Check", "verify"}}}
			var output bytes.Buffer
			cmd.SetOut(&output)
			require.NoError(t, writeArtifactList(cmd, []listedArtifact{item}, map[string]string{"type:Check": "check"}))
			require.NotContains(t, strings.TrimSuffix(output.String(), "\n"), "\n")
			program, err := syntax.NewParser().Parse(strings.NewReader(output.String()), "")
			require.NoError(t, err)
			var argv []string
			shell, err := interp.New(interp.ExecHandler(func(_ context.Context, args []string) error {
				argv = append(argv, args...)
				return nil
			}))
			require.NoError(t, err)
			require.NoError(t, shell.Run(t.Context(), program))
			require.Equal(t, []string{"dagger", "check", "-W", tc.workspace, "--check=verify"}, argv)
		})
	}
}
