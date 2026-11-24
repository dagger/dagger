package daggercmd

import (
	"context"
	"fmt"
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

// loadTestdataModuleCatalog loads testdata/modules.json, a copy of the
// release-pinned catalog dagger.io serves, through the loader: served by a
// test server, with an empty cache.
func loadTestdataModuleCatalog(t *testing.T) []registryModule {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "modules.json"))
	require.NoError(t, err)
	srv := serveModuleCatalog(t, http.StatusOK, string(data))
	withModuleCatalogSource(t, srv.URL)
	mods, err := loadModuleRegistry(t.Context())
	require.NoError(t, err)
	return mods
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

func TestLoadModuleRegistryUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) (url string)
		cache string
		want  []string
	}{
		{
			name: "no cache, server error",
			setup: func(t *testing.T) string {
				return serveModuleCatalog(t, http.StatusBadGateway, "").URL
			},
			want: []string{"couldn't fetch ", "unexpected status 502 Bad Gateway"},
		},
		{
			name: "no cache, unusable response",
			setup: func(t *testing.T) string {
				return serveModuleCatalog(t, http.StatusOK, `{"version": 1, "modules": []}`).URL
			},
			want: []string{"module catalog has no modules"},
		},
		{
			name: "no cache, unreachable",
			setup: func(t *testing.T) string {
				srv := serveModuleCatalog(t, http.StatusOK, testModuleCatalog)
				srv.Close()
				return srv.URL
			},
			want: []string{"connection refused"},
		},
		{
			name: "corrupt cache",
			setup: func(t *testing.T) string {
				return serveModuleCatalog(t, http.StatusBadGateway, "").URL
			},
			cache: `{"version": 1, "modules": [`,
			want:  []string{"unexpected status 502 Bad Gateway", "(cached copy unusable: "},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := tc.setup(t)
			cachePath := withModuleCatalogSource(t, url)
			if tc.cache != "" {
				writeStaleModuleCatalogCache(t, cachePath, tc.cache)
			}
			mods, err := loadModuleRegistry(t.Context())
			require.Nil(t, mods, "never an empty or partial catalog")
			var unavailable *moduleCatalogUnavailableError
			require.ErrorAs(t, err, &unavailable)
			require.Contains(t, err.Error(), "module catalog unavailable: couldn't fetch "+url+": ")
			for _, want := range tc.want {
				require.Contains(t, err.Error(), want)
			}
			require.NotContains(t, unavailable.Reason(), `Get "`, "the cause doesn't repeat the URL")
		})
	}

	t.Run("no cache dir", func(t *testing.T) {
		srv := serveModuleCatalog(t, http.StatusInternalServerError, "")
		withModuleCatalogSource(t, srv.URL)
		moduleCatalogCachePath = func() string { return "" }
		_, err := loadModuleRegistry(t.Context())
		var unavailable *moduleCatalogUnavailableError
		require.ErrorAs(t, err, &unavailable)
		require.NotContains(t, err.Error(), "cached copy")
	})

	t.Run("timeout", func(t *testing.T) {
		err := &moduleCatalogUnavailableError{URL: "https://example.com/modules.json", Err: fmt.Errorf("get: %w", context.DeadlineExceeded), CacheErr: os.ErrNotExist}
		require.Equal(t, "couldn't fetch https://example.com/modules.json: no answer within 3s", err.Reason())
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

// A failing fetch is retried at most hourly: the stale cache it falls back
// to counts as fresh for another hour.
func TestLoadModuleRegistryFailedFetchBacksOff(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	cachePath := withModuleCatalogSource(t, srv.URL)
	writeStaleModuleCatalogCache(t, cachePath, testModuleCatalog)

	for range 3 {
		mods, err := loadModuleRegistry(t.Context())
		require.NoError(t, err)
		require.Equal(t, []string{"fresh", "fresh-sdk"}, registryNames(mods))
	}
	require.Equal(t, int32(1), requests.Load())
	cached, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	require.Equal(t, testModuleCatalog, string(cached), "the cache's contents are kept")
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

func TestCatalogSDKsMatchSDKRegistry(t *testing.T) {
	mods := loadTestdataModuleCatalog(t)
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

func TestWithBuiltinModules(t *testing.T) {
	recommendsSkill := func(mods []registryModule) bool {
		for _, mod := range mods {
			if mod.Repo == "dagger.io/skill" {
				return mod.Recommend != nil
			}
		}
		return false
	}

	t.Run("added when the catalog lacks it", func(t *testing.T) {
		mods := []registryModule{{Name: "go", Repo: "dagger.io/go"}}
		got := withBuiltinModules(mods)
		require.Equal(t, []string{"go", "dagger-skill"}, registryNames(got))
		require.True(t, recommendsSkill(got))
		require.Len(t, mods, 1, "the catalog is not modified")
	})

	t.Run("catalog entry without a rule keeps the check", func(t *testing.T) {
		mods := []registryModule{{Name: "skill", Repo: "dagger.io/skill"}}
		got := withBuiltinModules(mods)
		require.Equal(t, []string{"skill"}, registryNames(got))
		require.True(t, recommendsSkill(got))
		require.Nil(t, mods[0].Recommend, "the catalog is not modified")
	})

	t.Run("catalog entry with a rule wins", func(t *testing.T) {
		rule := SimpleRecommend("**/SKILL.md")
		got := withBuiltinModules([]registryModule{{Name: "skill", Repo: "dagger.io/skill", Recommend: rule}})
		require.Equal(t, []string{"skill"}, registryNames(got))
		require.Equal(t, fmt.Sprintf("%p", rule), fmt.Sprintf("%p", got[0].Recommend))
	})
}
