package daggercmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactListFormats(t *testing.T) {
	names := map[string]string{"go/modules": "go-module", "type:Check": "check", "type:Container": "container"}
	row := func(path, key, name string) listedArtifact {
		return listedArtifact{URI: "dag+check://" + path + "?go-module=" + key, Description: "Check files\nExtra detail", DimensionKeys: []struct{ Dimension, Key string }{{"go/modules", key}, {"type:Check", path}}, DisplayKeys: map[string]string{"type:Check": name}}
	}
	items := []listedArtifact{row("go/modules/generate/stale", ".", "stale"), row("go/modules/test", ".", "test")}
	render := func(format string, rows []listedArtifact) string {
		cmd := &cobra.Command{Use: "check"}
		cmd.Flags().String("format", format, "")
		var out bytes.Buffer
		cmd.SetOut(&out)
		require.NoError(t, writeArtifactList(cmd, rows, names))
		return out.String()
	}
	t.Run("type dimension identifies operations on the same item", func(t *testing.T) {
		out := render("table", items)
		require.Regexp(t, `GO-MODULE +CHECK +DESCRIPTION`, out)
		require.Regexp(t, `\. +stale +Check files`, out)
		require.Regexp(t, `\. +test +Check files`, out)
		require.Regexp(t, `(?m)^GO-MODULE +CHECK +DESCRIPTION$`, out)
	})
	t.Run("cli uses filters", func(t *testing.T) {
		require.Equal(t, "--go-module=. --check=stale   # Check files\n--go-module=. --check=test    # Check files\n", render("cli", items))
	})
	t.Run("link retains canonical identity", func(t *testing.T) {
		require.Equal(t, items[0].URI+"\n"+items[1].URI+"\n", render("link", items))
	})
	t.Run("schema matrices use collection presence", func(t *testing.T) {
		item := items[0]
		item.URI = "dag+check://go/modules/generate/stale"
		item.DimensionKeys = item.DimensionKeys[1:]
		item.Presence = []string{"go/modules"}
		item.PresenceNames = map[string]string{"go/modules": "go-modules"}
		require.Equal(t, "--go-modules --check=stale   # Check files\n", render("cli", []listedArtifact{item}))
		require.NotContains(t, render("table", []listedArtifact{item}), "GO-MODULE")
	})
	t.Run("static artifacts use qualified type keys", func(t *testing.T) {
		item := listedArtifact{URI: "dag+container://backend/container", DimensionKeys: []struct{ Dimension, Key string }{{"type:Container", "backend/container"}}, DisplayKeys: map[string]string{"type:Container": "backend/container"}}
		require.Equal(t, "CONTAINER\nbackend/container\n", render("table", []listedArtifact{item}))
		require.Equal(t, "--container=backend/container\n", render("cli", []listedArtifact{item}))
	})
	t.Run("absolute output carries workspace and revision", func(t *testing.T) {
		item := items[0]
		item.URI = "dag+check://github.com/acme/ws@abc:go/modules/generate/stale?go-module=."
		out := render("table", []listedArtifact{item})
		require.Contains(t, out, "WORKSPACE")
		require.Contains(t, out, "github.com/acme/ws@abc")
		require.Regexp(t, `(?m)^WORKSPACE +GO-MODULE +CHECK +DESCRIPTION$`, out)
		require.Equal(t, "-W github.com/acme/ws@abc --go-module=. --check=stale   # Check files\n", render("cli", []listedArtifact{item}))
	})
	t.Run("collection filters can select descendants", func(t *testing.T) {
		item := items[0]
		item.CollectionItem = true
		item.OmitTypeKey = true
		require.Equal(t, "--go-module=.   # Check files\n", render("cli", []listedArtifact{item}))
	})
	t.Run("cli omission preserves table and link identity", func(t *testing.T) {
		item := items[0]
		item.OmitCLITypeKey = true
		require.Equal(t, "--go-module=.   # Check files\n", render("cli", []listedArtifact{item}))
		require.Regexp(t, `GO-MODULE +CHECK +DESCRIPTION`, render("table", []listedArtifact{item}))
		require.Regexp(t, `\. +stale +Check files`, render("table", []listedArtifact{item}))
		require.Equal(t, item.URI+"\n", render("link", []listedArtifact{item}))
		item.URI = "dag+check://github.com/acme/ws@abc:go/modules/generate/stale?go-module=."
		args, err := artifactCLIArguments(item, names, []string{"--generated=false", "--skip=other/*"})
		require.NoError(t, err)
		require.Equal(t, "-W github.com/acme/ws@abc --go-module=. '--generated=false' '--skip=other/*'", args)
	})
	t.Run("duplicate selections print once", func(t *testing.T) {
		require.Equal(t, items[0].URI+"\n", render("link", []listedArtifact{items[0], items[0]}))
	})
}

func TestArtifactListCollapsedColumns(t *testing.T) {
	names := map[string]string{"module": "module", "/golang/modules": "golang-module", "/golang/modules/packages": "golang-package", "type:Check": "check"}
	for _, tc := range []struct {
		name     string
		args     []string
		expanded bool
		columns  bool
	}{
		{name: "broad"},
		{name: "all explicitly false", args: []string{"--all=false"}},
		{name: "module alias", args: []string{"--golang"}, columns: true},
		{name: "false alias", args: []string{"--golang=false"}},
		{name: "module flag", args: []string{"--module=golang"}, columns: true},
		{name: "module path", args: []string{"golang"}, columns: true},
		{name: "deeper path", args: []string{"golang/modules/packages/test"}, columns: true},
		{name: "module address", args: []string{"dag://?module=golang"}, columns: true},
		{name: "collection key", args: []string{"--golang-module=."}, columns: true},
		{name: "collection presence", args: []string{"--golang-modules"}, columns: true},
		{name: "collection address", args: []string{"dag://?golang-module=."}, columns: true},
		{name: "qualified check flag", args: []string{"--check=golang/modules/packages/test"}, columns: true},
		{name: "qualified check address", args: []string{"dag+check://?check=golang/modules/packages/test"}, columns: true},
		{name: "unqualified check", args: []string{"--check=test"}, columns: true},
		{name: "short field path", args: []string{"packages/test"}, columns: true},
		{name: "short check flag", args: []string{"--check=packages/test"}, columns: true},
		{name: "short check address", args: []string{"dag+check://?check=packages/test"}, columns: true},
		{name: "workspace wide path", args: []string{"**"}},
		{name: "all", args: []string{"--all"}, expanded: true, columns: true},
		{name: "module all", args: []string{"--golang", "--all"}, expanded: true, columns: true},
		{name: "concrete keys are never hidden", expanded: true, columns: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "check"}
			registerCommandArtifactFlags(cmd)
			cmd.Flags().Bool("golang", false, "")
			cmd.Flags().Lookup("golang").Annotations = map[string][]string{artifactDimensionFlag: {"module"}, artifactSelectorKeyFlag: {"golang"}}
			cmd.Flags().StringArray("golang-module", nil, "")
			cmd.Flags().Lookup("golang-module").Annotations = map[string][]string{artifactDimensionFlag: {"/golang/modules"}}
			cmd.Flags().Bool("golang-modules", false, "")
			cmd.Flags().Lookup("golang-modules").Annotations = map[string][]string{artifactDimensionFlag: {"/golang/modules"}, artifactPresenceFlag: {"true"}}
			cmd.Flags().StringArray("check", nil, "")
			cmd.Flags().Lookup("check").Annotations = map[string][]string{artifactDimensionFlag: {"type:Check"}}
			require.NoError(t, cmd.ParseFlags(tc.args))
			row := listedArtifact{
				URI:           "dag+check://golang/modules/packages/test",
				Presence:      []string{"/golang/modules", "/golang/modules/packages"},
				DimensionKeys: []struct{ Dimension, Key string }{{"module", "golang"}, {"type:Check", "golang/modules/packages/test"}},
			}
			if tc.expanded {
				row.Presence = nil
				row.DimensionKeys = append(row.DimensionKeys, struct{ Dimension, Key string }{"/golang/modules", "."}, struct{ Dimension, Key string }{"/golang/modules/packages", "core"})
			}
			var out bytes.Buffer
			cmd.SetOut(&out)
			require.NoError(t, writeArtifactList(cmd, []listedArtifact{row}, names))
			if tc.columns {
				require.Contains(t, out.String(), "GOLANG-MODULE")
				require.Contains(t, out.String(), "GOLANG-PACKAGE")
				if !tc.expanded {
					require.Contains(t, out.String(), "*")
				}
			} else {
				require.NotContains(t, out.String(), "GOLANG-MODULE")
				require.NotContains(t, out.String(), "GOLANG-PACKAGE")
				require.NotContains(t, out.String(), "*")
			}
			require.Contains(t, out.String(), "MODULE")
			require.Contains(t, out.String(), "CHECK")
			require.Equal(t, "dag+check://golang/modules/packages/test", row.URI)
		})
	}
}

func TestArtifactListMainModulePathKeepsColumns(t *testing.T) {
	for _, tc := range []struct{ command, field, typ string }{
		{"check", "verify", "Check"},
		{"agent", "assistant", "Expertise"},
		{"generate", "write", "Generator"},
		{"shell", "broken", "Container"},
		{"start", "serve", "Service"},
	} {
		for _, selector := range []string{"items/" + tc.field, "items", "items/*", "items/**", "item*", "dag://items/**"} {
			t.Run(tc.command+"/"+selector, func(t *testing.T) {
				cmd := &cobra.Command{Use: tc.command}
				registerCommandArtifactFlags(cmd)
				path := "items/" + tc.field
				require.NoError(t, cmd.ParseFlags([]string{selector}))
				row := listedArtifact{
					URI:           "dag://" + path,
					Presence:      []string{"/collections/items"},
					DimensionKeys: []struct{ Dimension, Key string }{{"module", "collections"}, {"type:" + tc.typ, path}},
				}
				names := map[string]string{"module": "module", "/collections/items": "collections-items-item", "type:" + tc.typ: tc.typ}
				var out bytes.Buffer
				cmd.SetOut(&out)
				require.NoError(t, writeArtifactList(cmd, []listedArtifact{row}, names))
				require.Contains(t, out.String(), "COLLECTIONS-ITEMS-ITEM")
				require.Regexp(t, `collections +\* +items/`+tc.field, out.String())
			})
		}
	}
}
