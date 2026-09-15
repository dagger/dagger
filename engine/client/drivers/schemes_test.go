package drivers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The catalog is what users read. The registry is what the client dials. They
// must describe the same set, or help and completion teach a value that does
// not work, or hide one that does.
func TestSchemeCatalogCoversRegistry(t *testing.T) {
	documented := map[string]bool{}
	for _, scheme := range SchemeCatalog() {
		require.NotEmpty(t, scheme.Value, scheme.Scheme)
		require.NotEmpty(t, scheme.Group, scheme.Scheme)
		require.False(t, documented[scheme.Scheme], "duplicate scheme %q", scheme.Scheme)
		documented[scheme.Scheme] = true
	}
	require.ElementsMatch(t, RegisteredSchemes(), sortedKeys(documented))
}

func TestSchemeSummaryNamesPrimaryValues(t *testing.T) {
	require.Equal(t,
		"cloud, image://, container://, tcp://, tls://, ssh://, kube-pod://, unix://",
		SchemeSummary())
}

func TestSchemeCompletionsSkipDeprecated(t *testing.T) {
	completions := SchemeCompletions()
	require.Contains(t, completions, "cloud")
	require.Contains(t, completions, "image+nerdctl://")
	require.NotContains(t, completions, "docker-image://")
	for _, value := range completions {
		require.False(t, strings.HasSuffix(value, "IMAGE"), value)
	}
}

func TestSchemeHelpGroupsValues(t *testing.T) {
	help := SchemeHelp()
	require.Contains(t, help, "  Dagger Cloud:\n    cloud\n")
	require.Contains(t, help, "    tcp://HOST:PORT (no authentication)\n")
	require.Contains(t, help, "  Legacy Docker schemes:\n    docker-image://IMAGE\n")
}

// An unusable value must teach the usable ones.
func TestGetDriverUnknownSchemeListsSchemes(t *testing.T) {
	_, err := GetDriver(context.Background(), "")
	require.ErrorContains(t, err, `no driver for scheme "" found`)
	for _, name := range RegisteredSchemes() {
		require.ErrorContains(t, err, name)
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}
