package daggercmd

import (
	"bytes"
	"slices"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactListFormats(t *testing.T) {
	items := []listedArtifact{
		{URI: "dag+test://go/modules/tests?go-module=.%2Fapi&go-test=TestHealth", Description: "Check health\nExtra detail", DimensionKeys: []struct{ Dimension, Key string }{{"Go.modules", "./api"}, {"GoModule.tests", "TestHealth"}}},
		{URI: "dag+test://go/modules/tests?go-module=.%2Fworker&go-test=TestHealth", DimensionKeys: []struct{ Dimension, Key string }{{"Go.modules", "./worker"}, {"GoModule.tests", "TestHealth"}}},
	}
	names := map[string]string{"Go.modules": "go-module", "GoModule.tests": "go-test"}
	render := func(t *testing.T, format string, rows []listedArtifact) string {
		t.Helper()
		cmd := &cobra.Command{}
		cmd.Flags().String("format", format, "")
		var out bytes.Buffer
		cmd.SetOut(&out)
		require.NoError(t, writeArtifactList(cmd, rows, names))
		return out.String()
	}
	t.Run("parent keys distinguish duplicate child keys", func(t *testing.T) {
		out := render(t, "table", items)
		require.Contains(t, out, "GO-MODULE")
		require.Contains(t, out, "GO-TEST")
		require.NotContains(t, out, "VARIANT")
		require.Regexp(t, `\./api +TestHealth +Check health`, out)
		require.Regexp(t, `\./worker +TestHealth`, out)
	})
	t.Run("different paths with the same keys need a variant", func(t *testing.T) {
		other := items[0]
		other.URI = "dag+test://other/tests?go-module=.%2Fapi&go-test=TestHealth"
		out := render(t, "table", []listedArtifact{items[0], other})
		require.Regexp(t, `DESCRIPTION +VARIANT\n`, out)
		require.Contains(t, out, "modules/tests")
		require.Contains(t, out, "other/tests")
	})
	t.Run("static artifacts need a variant", func(t *testing.T) {
		out := render(t, "table", []listedArtifact{{URI: "dag+container://dev", Description: "Development"}})
		require.Regexp(t, `DESCRIPTION +VARIANT\nDevelopment +dev\n`, out)
	})
	t.Run("empty keys differ from missing dimensions", func(t *testing.T) {
		out := render(t, "table", []listedArtifact{
			{URI: "dag+test://one?go-test=", DimensionKeys: []struct{ Dimension, Key string }{{"GoModule.tests", ""}}},
			{URI: "dag+test://two", DimensionKeys: nil},
		})
		require.Regexp(t, `(?m)^"" +one$`, out)
		require.Regexp(t, `(?m)^ +two$`, out)
	})

	t.Run("links retain all keys", func(t *testing.T) {
		require.Equal(t, items[0].URI+"\n"+items[1].URI+"\n", render(t, "link", items))
	})
	t.Run("cli keeps the path and protects shell arguments", func(t *testing.T) {
		item := listedArtifact{URI: "dag+test://go/modules/tests?go-test=hello", Description: "Check health\nnot another command", DimensionKeys: []struct{ Dimension, Key string }{{"GoModule.tests", "hello; echo surprise!"}}}
		out := render(t, "cli", []listedArtifact{item})
		require.Equal(t, "--go-test='hello; echo surprise!' dag+test://go/modules/tests   # Check health\n", out)
	})
	t.Run("duplicate selections print once", func(t *testing.T) {
		require.Equal(t, items[0].URI+"\n", render(t, "link", []listedArtifact{items[0], items[0]}))
	})
}

func TestArtifactVariantLabels(t *testing.T) {
	paths := []string{"go/modules/test", "python/modules/test", "engine/dev", "go/modules/test", "go/modules/lint"}
	want := map[string]string{
		"go/modules/test":     "go/modules/test",
		"python/modules/test": "python/modules/test",
		"engine/dev":          "dev",
		"go/modules/lint":     "lint",
	}
	require.Equal(t, want, artifactVariantLabels(paths))
	slices.Reverse(paths)
	require.Equal(t, want, artifactVariantLabels(paths))
}
