package core

import (
	"errors"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestBuiltinModuleCatalogLookup(t *testing.T) {
	dgst := digest.FromString("python-runtime")
	catalog := NewBuiltinModuleCatalog([]BuiltinModuleCatalogEntry{
		{
			Name:           "python-runtime",
			Description:    "Python runtime",
			Source:         "python",
			ManifestDigest: dgst,
			Subpath:        "runtime",
		},
	})

	entry, err := catalog.Lookup("python-runtime")
	require.NoError(t, err)
	require.Equal(t, "python-runtime", entry.Name)
	require.Equal(t, dgst, entry.ManifestDigest)
	require.Equal(t, "runtime", entry.Subpath)
}

func TestBuiltinModuleCatalogUnknownName(t *testing.T) {
	catalog := NewBuiltinModuleCatalog(nil)

	_, err := catalog.Lookup("missing")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrBuiltinModuleNotFound))
}

func TestBuiltinModuleCatalogAlias(t *testing.T) {
	catalog := NewBuiltinModuleCatalog([]BuiltinModuleCatalogEntry{
		{
			Name:           "typescript-runtime",
			Source:         "typescript",
			ManifestDigest: digest.FromString("typescript-runtime"),
			Subpath:        "runtime",
			Aliases:        []string{"ts"},
		},
	})

	entry, err := catalog.Lookup("ts")
	require.NoError(t, err)
	require.Equal(t, "typescript-runtime", entry.Name)
}

func TestBuiltinModuleCatalogRejectsEmptySource(t *testing.T) {
	catalog := NewBuiltinModuleCatalog([]BuiltinModuleCatalogEntry{
		{
			Name:    "python-runtime",
			Subpath: "runtime",
		},
	})

	_, err := catalog.Lookup("python-runtime")
	require.ErrorContains(t, err, "empty source")
}

func TestBuiltinModuleCatalogRejectsMalformedDigest(t *testing.T) {
	catalog := NewBuiltinModuleCatalog([]BuiltinModuleCatalogEntry{
		{
			Name:           "python-runtime",
			Source:         "python",
			ManifestDigest: digest.Digest("not-a-digest"),
			Subpath:        "runtime",
		},
	})

	_, err := catalog.Lookup("python-runtime")
	require.ErrorContains(t, err, "invalid manifest digest")
}

func TestBuiltinModuleCatalogListHidesInternalAndUnavailableEntries(t *testing.T) {
	catalog := NewBuiltinModuleCatalog([]BuiltinModuleCatalogEntry{
		{
			Name:           "python-runtime",
			Source:         "python",
			ManifestDigest: digest.FromString("python-runtime"),
			Subpath:        "runtime",
		},
		{
			Name:   "java-runtime",
			Source: "github.com/dagger/dagger/sdk/java",
		},
		{
			Name:           "internal",
			Source:         "internal",
			ManifestDigest: digest.FromString("internal-runtime"),
			Subpath:        "runtime",
			Internal:       true,
		},
		{
			Name:     "unavailable",
			Source:   "unavailable",
			Internal: true,
		},
	})

	entries, err := catalog.List()
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "python-runtime", entries[0].Name)
	require.Equal(t, "java-runtime", entries[1].Name)
}
