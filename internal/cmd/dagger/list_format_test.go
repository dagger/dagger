package daggercmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactListFormats(t *testing.T) {
	names := map[string]string{"Go.modules": "go-module", "type:Check": "check", "type:Container": "container"}
	row := func(path, key, name string) listedArtifact {
		return listedArtifact{URI: "dag+check://" + path + "?go-module=" + key, Description: "Check files\nExtra detail", DimensionKeys: []struct{ Dimension, Key string }{{"Go.modules", key}, {"type:Check", path}}, DisplayKeys: map[string]string{"type:Check": name}}
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
		require.NotContains(t, out, "LINK")
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
		item.Presence = []string{"Go.modules"}
		item.PresenceNames = map[string]string{"Go.modules": "go-modules"}
		require.Equal(t, "--go-modules --check=stale   # Check files\n", render("cli", []listedArtifact{item}))
		require.Regexp(t, `\* +stale`, render("table", []listedArtifact{item}))
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
		require.NotContains(t, out, "LINK")
		require.Equal(t, "-W github.com/acme/ws@abc --go-module=. --check=stale   # Check files\n", render("cli", []listedArtifact{item}))
	})
	t.Run("collection filters can select descendants", func(t *testing.T) {
		item := items[0]
		item.CollectionItem = true
		item.OmitTypeKey = true
		require.Equal(t, "--go-module=.   # Check files\n", render("cli", []listedArtifact{item}))
	})
	t.Run("duplicate selections print once", func(t *testing.T) {
		require.Equal(t, items[0].URI+"\n", render("link", []listedArtifact{items[0], items[0]}))
	})
}
