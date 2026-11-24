package daggercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/adrg/xdg"
	"github.com/dagger/dagger/engine/slog"
)

// registryModule is one entry in the module catalog.
type registryModule struct {
	Name        string
	Kind        string
	Description string
	Repo        string
	Aliases     []string
	Recommend   RecommendFn
}

const (
	// moduleCatalogVersion is the modules.json format this CLI understands.
	// The format only changes version for breaking changes.
	moduleCatalogVersion = 1

	moduleKindSDK = "sdk"

	moduleCatalogFetchTimeout = 3 * time.Second
	moduleCatalogMaxBytes     = 4 << 20
	// moduleCatalogMaxAge is how long a cached catalog is used without
	// fetching: recommend and init stay offline most runs, and new modules
	// still reach them within the hour.
	moduleCatalogMaxAge = time.Hour
)

// Test hooks.
var (
	moduleCatalogURL        = "https://dagger.io/modules.json"
	moduleCatalogHTTPClient = http.DefaultClient
	moduleCatalogCachePath  = func() string {
		return filepath.Join(xdg.CacheHome, "dagger", "modules.json")
	}
)

// moduleCatalogUnavailableError is a catalog that could be neither fetched
// nor read from the cache: a first run offline, or dagger.io unreachable
// with no cached copy. `dagger init` continues without recommendations;
// `dagger module recommend` fails with it.
type moduleCatalogUnavailableError struct {
	URL string
	// Err is why the fetch failed.
	Err error
	// CacheErr is why the cached copy couldn't be used: os.ErrNotExist when
	// there is none.
	CacheErr error
}

func (e *moduleCatalogUnavailableError) Error() string {
	return "module catalog unavailable: " + e.Reason()
}

// Reason says what failed, in a line: the URL and why its fetch failed, and
// why the cached copy couldn't stand in when there is one.
func (e *moduleCatalogUnavailableError) Reason() string {
	cause := e.Err.Error()
	var urlErr *url.Error
	switch {
	case errors.Is(e.Err, context.DeadlineExceeded):
		cause = fmt.Sprintf("no answer within %s", moduleCatalogFetchTimeout)
	case errors.As(e.Err, &urlErr):
		// Its message repeats the method and URL.
		cause = urlErr.Err.Error()
	}
	reason := fmt.Sprintf("couldn't fetch %s: %s", e.URL, cause)
	if e.CacheErr != nil && !errors.Is(e.CacheErr, os.ErrNotExist) {
		reason += fmt.Sprintf(" (cached copy unusable: %s)", e.CacheErr)
	}
	return reason
}

func (e *moduleCatalogUnavailableError) Unwrap() error { return e.Err }

// loadModuleRegistry loads the module catalog. A cached copy under an hour
// old is used as is, without touching the network. Otherwise it fetches a
// fresh copy from moduleCatalogURL, falling back to the stale cached copy
// (which then counts as fresh for another hour, so a failing fetch is retried
// at most hourly). With neither, it returns a *moduleCatalogUnavailableError.
// A failed fetch never yields an empty catalog. If ctx is done, it returns
// ctx's error instead of falling back.
func loadModuleRegistry(ctx context.Context) ([]registryModule, error) {
	cached, fresh, cacheErr := readCachedModuleCatalog()
	if fresh {
		return cached, nil
	}

	mods, err := fetchModuleCatalog(ctx)
	if err == nil {
		return mods, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	slog.Debug("fetch module catalog failed; using fallback", "url", moduleCatalogURL, "error", err)

	if cacheErr == nil {
		// Don't try again for another hour: offline or firewalled, every run
		// would otherwise wait out the fetch timeout.
		if path := moduleCatalogCachePath(); path != "" {
			now := time.Now()
			if err := os.Chtimes(filepath.Clean(path), now, now); err != nil {
				slog.Debug("touch cached module catalog failed", "path", path, "error", err)
			}
		}
		return cached, nil
	}
	return nil, &moduleCatalogUnavailableError{URL: moduleCatalogURL, Err: err, CacheErr: cacheErr}
}

// readCachedModuleCatalog parses the cached catalog, and reports whether it
// was written less than moduleCatalogMaxAge ago. A modification time in the
// future counts as stale, so a skewed clock can't pin an old catalog.
func readCachedModuleCatalog() (mods []registryModule, fresh bool, err error) {
	path := moduleCatalogCachePath()
	if path == "" {
		return nil, false, os.ErrNotExist
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	mods, err = parseModuleCatalog(data)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	age := time.Since(info.ModTime())
	return mods, age >= 0 && age < moduleCatalogMaxAge, nil
}

// fetchModuleCatalog downloads and parses the catalog, and caches it when it
// is valid.
func fetchModuleCatalog(ctx context.Context) ([]registryModule, error) {
	ctx, cancel := context.WithTimeout(ctx, moduleCatalogFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, moduleCatalogURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := moduleCatalogHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, moduleCatalogMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > moduleCatalogMaxBytes {
		return nil, fmt.Errorf("module catalog exceeds %d bytes", moduleCatalogMaxBytes)
	}
	mods, err := parseModuleCatalog(data)
	if err != nil {
		return nil, err
	}
	if path := moduleCatalogCachePath(); path != "" {
		if err := writeFileAtomic(path, data); err != nil {
			slog.Debug("cache module catalog failed", "path", path, "error", err)
		}
	}
	return mods, nil
}

// writeFileAtomic replaces path with data, so concurrent readers see either
// the old or the new contents, never a partial file.
func writeFileAtomic(path string, data []byte) (rerr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if rerr != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// moduleCatalog is the modules.json format. Unknown fields are ignored.
type moduleCatalog struct {
	Version int                  `json:"version"`
	Modules []moduleCatalogEntry `json:"modules"`
}

type moduleCatalogEntry struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Ref         string         `json:"ref"`
	Aliases     []string       `json:"aliases"`
	Description string         `json:"description"`
	Recommend   *recommendRule `json:"recommend"`
}

// parseModuleCatalog decodes modules.json. It rejects catalogs of another
// version and catalogs without usable entries, so callers fall back instead
// of losing every recommendation.
func parseModuleCatalog(data []byte) ([]registryModule, error) {
	var catalog moduleCatalog
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("parse module catalog: %w", err)
	}
	if catalog.Version != moduleCatalogVersion {
		return nil, fmt.Errorf("unsupported module catalog version %d", catalog.Version)
	}
	mods := make([]registryModule, 0, len(catalog.Modules))
	for _, entry := range catalog.Modules {
		if entry.Name == "" || entry.Ref == "" {
			continue
		}
		mods = append(mods, registryModule{
			Name:        entry.Name,
			Kind:        entry.Kind,
			Description: entry.Description,
			Repo:        entry.Ref,
			Aliases:     entry.Aliases,
			Recommend:   entry.Recommend.recommendFn(),
		})
	}
	if len(mods) == 0 {
		return nil, errors.New("module catalog has no modules")
	}
	return mods, nil
}

// builtinModules are recommended by checks modules.json can't express, so
// the CLI carries them itself.
func builtinModules() []registryModule {
	return []registryModule{
		{
			Name:        "dagger-skill",
			Kind:        "module",
			Description: "Install and update the official Dagger agent skill",
			Repo:        "dagger.io/skill",
			Recommend:   recommendDaggerSkill,
		},
	}
}

// withBuiltinModules adds the built-in modules to a catalog. A catalog entry
// with the same ref takes precedence, keeping the built-in check only when
// the entry has no recommend rule of its own.
func withBuiltinModules(mods []registryModule) []registryModule {
	out := slices.Clone(mods)
	for _, builtin := range builtinModules() {
		i := slices.IndexFunc(out, func(m registryModule) bool { return m.Repo == builtin.Repo })
		switch {
		case i < 0:
			out = append(out, builtin)
		case out[i].Recommend == nil:
			out[i].Recommend = builtin.Recommend
		}
	}
	return out
}
