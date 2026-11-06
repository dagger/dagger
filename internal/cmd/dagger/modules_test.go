package daggercmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testModuleCatalog = `{
	"version": 1,
	"generatedBy": "a newer generator",
	"modules": [
		{
			"name": "fresh",
			"kind": "module",
			"ref": "dagger.io/fresh",
			"aliases": ["new"],
			"description": "Fresh module",
			"docsUrl": "https://example.com/fresh",
			"publisher": "Dagger",
			"official": true,
			"stars": 5,
			"recommend": {"anyOf": [{"glob": "**/fresh.toml"}]}
		},
		{
			"name": "fresh-sdk",
			"kind": "sdk",
			"ref": "dagger.io/sdk/fresh",
			"aliases": [],
			"description": "Fresh SDK",
			"publisher": "Dagger",
			"official": true
		}
	]
}`

// withModuleCatalogSource points the catalog loader at url and a cache file
// in a temporary directory, and returns the cache path.
func withModuleCatalogSource(t *testing.T, url string) string {
	t.Helper()
	cachePath := filepath.Join(t.TempDir(), "cache", "dagger", "modules.json")
	oldURL, oldCachePath := moduleCatalogURL, moduleCatalogCachePath
	moduleCatalogURL = url
	moduleCatalogCachePath = func() string { return cachePath }
	t.Cleanup(func() {
		moduleCatalogURL, moduleCatalogCachePath = oldURL, oldCachePath
	})
	return cachePath
}

func serveModuleCatalog(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// writeStaleModuleCatalogCache writes a cached catalog older than
// moduleCatalogMaxAge, so the loader fetches before using it.
func writeStaleModuleCatalogCache(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, writeFileAtomic(path, []byte(data)))
	old := time.Now().Add(-2 * moduleCatalogMaxAge)
	require.NoError(t, os.Chtimes(path, old, old))
}

func registryNames(mods []registryModule) []string {
	names := make([]string, len(mods))
	for i, mod := range mods {
		names[i] = mod.Name
	}
	return names
}

func embeddedRegistryNames(t *testing.T) []string {
	t.Helper()
	mods, err := parseModuleCatalog(embeddedModuleCatalog)
	require.NoError(t, err)
	return registryNames(mods)
}

func TestLoadModuleRegistryFetchesAndCaches(t *testing.T) {
	srv := serveModuleCatalog(t, http.StatusOK, testModuleCatalog)
	cachePath := withModuleCatalogSource(t, srv.URL)

	mods, err := loadModuleRegistry(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"fresh", "fresh-sdk"}, registryNames(mods))
	require.Equal(t, "dagger.io/fresh", mods[0].Repo)
	require.Equal(t, []string{"new"}, mods[0].Aliases)
	require.Equal(t, "module", mods[0].Kind)
	require.NotNil(t, mods[0].Recommend)
	require.Equal(t, moduleKindSDK, mods[1].Kind)
	require.Nil(t, mods[1].Recommend)

	cached, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	require.Equal(t, testModuleCatalog, string(cached))
	entries, err := os.ReadDir(filepath.Dir(cachePath))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary files are left behind")
}

func TestLoadModuleRegistryFallsBackToCache(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, testModuleCatalog},
		{"not found", http.StatusNotFound, "not found"},
		{"invalid JSON", http.StatusOK, `{"version": 1, "modules": [`},
		{"empty catalog", http.StatusOK, `{"version": 1, "modules": []}`},
		{"unusable entries", http.StatusOK, `{"version": 1, "modules": [{"name": "noref"}]}`},
		{"newer version", http.StatusOK, `{"version": 2, "modules": [{"name": "x", "ref": "x"}]}`},
		{"missing version", http.StatusOK, `{"modules": [{"name": "x", "ref": "x"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveModuleCatalog(t, tc.status, tc.body)
			cachePath := withModuleCatalogSource(t, srv.URL)
			writeStaleModuleCatalogCache(t, cachePath, testModuleCatalog)

			mods, err := loadModuleRegistry(t.Context())
			require.NoError(t, err)
			require.Equal(t, []string{"fresh", "fresh-sdk"}, registryNames(mods))

			// An unusable response never replaces a good cache.
			cached, err := os.ReadFile(cachePath)
			require.NoError(t, err)
			require.Equal(t, testModuleCatalog, string(cached))
		})
	}
}

func TestLoadModuleRegistryFallsBackToEmbedded(t *testing.T) {
	want := embeddedRegistryNames(t)

	t.Run("no cache", func(t *testing.T) {
		srv := serveModuleCatalog(t, http.StatusOK, `{"version": 1, "modules": []}`)
		withModuleCatalogSource(t, srv.URL)
		mods, err := loadModuleRegistry(t.Context())
		require.NoError(t, err)
		require.Equal(t, want, registryNames(mods))
	})

	t.Run("corrupt cache", func(t *testing.T) {
		srv := serveModuleCatalog(t, http.StatusBadGateway, "")
		cachePath := withModuleCatalogSource(t, srv.URL)
		require.NoError(t, writeFileAtomic(cachePath, []byte(`{"version": 1, "modules": [`)))
		mods, err := loadModuleRegistry(t.Context())
		require.NoError(t, err)
		require.Equal(t, want, registryNames(mods))
	})

	t.Run("unreachable", func(t *testing.T) {
		srv := serveModuleCatalog(t, http.StatusOK, testModuleCatalog)
		srv.Close()
		withModuleCatalogSource(t, srv.URL)
		mods, err := loadModuleRegistry(t.Context())
		require.NoError(t, err)
		require.Equal(t, want, registryNames(mods))
	})

	t.Run("no cache dir", func(t *testing.T) {
		srv := serveModuleCatalog(t, http.StatusInternalServerError, "")
		withModuleCatalogSource(t, srv.URL)
		moduleCatalogCachePath = func() string { return "" }
		mods, err := loadModuleRegistry(t.Context())
		require.NoError(t, err)
		require.Equal(t, want, registryNames(mods))
	})
}

func TestLoadModuleRegistryCanceled(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })
	cachePath := withModuleCatalogSource(t, srv.URL)
	writeStaleModuleCatalogCache(t, cachePath, testModuleCatalog)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	mods, err := loadModuleRegistry(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, mods)
}

// countingModuleCatalog serves body and counts the requests it gets.
func countingModuleCatalog(t *testing.T, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

const otherModuleCatalog = `{"version": 1, "modules": [{"name": "other", "kind": "module", "ref": "dagger.io/other", "description": "Other module"}]}`

func TestLoadModuleRegistryFreshCacheSkipsNetwork(t *testing.T) {
	srv, requests := countingModuleCatalog(t, otherModuleCatalog)
	cachePath := withModuleCatalogSource(t, srv.URL)
	require.NoError(t, writeFileAtomic(cachePath, []byte(testModuleCatalog)))

	mods, err := loadModuleRegistry(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"fresh", "fresh-sdk"}, registryNames(mods))
	require.Zero(t, requests.Load(), "a cache under an hour old is used without fetching")

	// Just under the limit is still fresh.
	almost := time.Now().Add(-moduleCatalogMaxAge + time.Minute)
	require.NoError(t, os.Chtimes(cachePath, almost, almost))
	_, err = loadModuleRegistry(t.Context())
	require.NoError(t, err)
	require.Zero(t, requests.Load())
}

func TestLoadModuleRegistryStaleCacheFetches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mtime time.Time
	}{
		{"older than an hour", time.Now().Add(-moduleCatalogMaxAge - time.Minute)},
		{"modified in the future", time.Now().Add(time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, requests := countingModuleCatalog(t, otherModuleCatalog)
			cachePath := withModuleCatalogSource(t, srv.URL)
			require.NoError(t, writeFileAtomic(cachePath, []byte(testModuleCatalog)))
			require.NoError(t, os.Chtimes(cachePath, tc.mtime, tc.mtime))

			mods, err := loadModuleRegistry(t.Context())
			require.NoError(t, err)
			require.Equal(t, []string{"other"}, registryNames(mods))
			require.Equal(t, int32(1), requests.Load())

			// The fetch refreshed the cache, so the next run stays offline.
			cached, err := os.ReadFile(cachePath)
			require.NoError(t, err)
			require.Equal(t, otherModuleCatalog, string(cached))
			mods, err = loadModuleRegistry(t.Context())
			require.NoError(t, err)
			require.Equal(t, []string{"other"}, registryNames(mods))
			require.Equal(t, int32(1), requests.Load())
		})
	}
}

func TestLoadModuleRegistryFreshCorruptCacheFetches(t *testing.T) {
	srv, requests := countingModuleCatalog(t, otherModuleCatalog)
	cachePath := withModuleCatalogSource(t, srv.URL)
	require.NoError(t, writeFileAtomic(cachePath, []byte(`{"version": 1, "modules": [`)))

	mods, err := loadModuleRegistry(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"other"}, registryNames(mods))
	require.Equal(t, int32(1), requests.Load())
}

func TestParseModuleCatalogMatchers(t *testing.T) {
	mods, err := parseModuleCatalog([]byte(`{
		"version": 1,
		"modules": [
			{"name": "none", "ref": "x/none"},
			{"name": "null", "ref": "x/null", "recommend": null},
			{"name": "glob", "ref": "x/glob", "recommend": {"anyOf": [{"glob": "**/a"}]}},
			{"name": "unknownKey", "ref": "x/unknown-key", "recommend": {"anyOf": [{"glob": "**/a", "yamlKey": "a"}]}},
			{"name": "noGlob", "ref": "x/no-glob", "recommend": {"anyOf": [{"jsonKey": "a"}]}},
			{"name": "twoChecks", "ref": "x/two-checks", "recommend": {"anyOf": [{"glob": "**/a", "jsonKey": "a", "tomlTable": "a"}]}},
			{"name": "emptyCheck", "ref": "x/empty-check", "recommend": {"anyOf": [{"glob": "**/a", "jsonKey": ""}]}},
			{"name": "nullCheck", "ref": "x/null-check", "recommend": {"anyOf": [{"glob": "**/a", "iniSection": null}]}},
			{"name": "badType", "ref": "x/bad-type", "recommend": {"anyOf": [{"glob": ["**/a"]}]}},
			{"name": "badMatcher", "ref": "x/bad-matcher", "recommend": {"anyOf": ["**/a"]}},
			{"name": "badAnyOf", "ref": "x/bad-any-of", "recommend": {"anyOf": {"glob": "**/a"}}},
			{"name": "unknownRule", "ref": "x/unknown-rule", "recommend": {"anyOf": [{"glob": "**/a"}], "allOf": []}},
			{"name": "badRule", "ref": "x/bad-rule", "recommend": "**/a"}
		]
	}`))
	require.NoError(t, err)
	recommends := map[string]bool{}
	for _, mod := range mods {
		recommends[mod.Name] = mod.Recommend != nil
	}
	require.Equal(t, map[string]bool{
		"none":        false,
		"null":        false,
		"glob":        true,
		"unknownKey":  false,
		"noGlob":      false,
		"twoChecks":   false,
		"emptyCheck":  false,
		"nullCheck":   false,
		"badType":     false,
		"badMatcher":  false,
		"badAnyOf":    false,
		"unknownRule": false,
		"badRule":     false,
	}, recommends)
}

func TestParseModuleCatalogMatcherFields(t *testing.T) {
	var rule recommendRule
	require.NoError(t, rule.UnmarshalJSON([]byte(`{"anyOf": [
		{"glob": "**/a"},
		{"glob": "**/package.json", "jsonKey": "mocha"},
		{"glob": "**/pyproject.toml", "tomlTable": "tool.ruff"},
		{"glob": "**/setup.cfg", "iniSection": "tool:pytest"},
		{"glob": "**/b", "future": true}
	]}`)))
	require.False(t, rule.unknown)
	require.Equal(t, []recommendMatcher{
		{Glob: "**/a"},
		{Glob: "**/package.json", JSONKey: "mocha"},
		{Glob: "**/pyproject.toml", TOMLTable: "tool.ruff"},
		{Glob: "**/setup.cfg", INISection: "tool:pytest"},
		{Glob: "**/b", unknown: true},
	}, rule.AnyOf)
}

func TestEmbeddedCatalogSDKsMatchSDKRegistry(t *testing.T) {
	mods, err := parseModuleCatalog(embeddedModuleCatalog)
	require.NoError(t, err)
	sdks, err := loadSDKRegistry()
	require.NoError(t, err)

	type sdk struct {
		Name, Description, Repo string
		Aliases                 []string
	}
	var got, want []sdk
	for _, mod := range mods {
		if mod.Kind != moduleKindSDK {
			continue
		}
		require.Nil(t, mod.Recommend)
		got = append(got, sdk{mod.Name, mod.Description, mod.Repo, mod.Aliases})
	}
	for _, entry := range sdks {
		want = append(want, sdk{entry.Name, entry.Description, "dagger.io/sdk/" + entry.Name, entry.Aliases})
	}
	require.ElementsMatch(t, want, got)
}
