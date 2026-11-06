package daggercmd

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

// embeddedModuleCatalog is the module catalog as of this CLI's build. It is
// the last fallback when https://dagger.io/modules.json and the cached copy
// are both unavailable.
//
//go:embed modules.json
var embeddedModuleCatalog []byte

// Test hooks.
var (
	moduleCatalogURL        = "https://dagger.io/modules.json"
	moduleCatalogHTTPClient = http.DefaultClient
	moduleCatalogCachePath  = func() string {
		return filepath.Join(xdg.CacheHome, "dagger", "modules.json")
	}
)

// loadModuleRegistry loads the module catalog. A cached copy under an hour
// old is used as is, without touching the network. Otherwise it fetches a
// fresh copy from moduleCatalogURL, falling back to the stale cached copy,
// then to the copy embedded at build time. A failed fetch never yields an
// empty catalog. If ctx is done, it returns ctx's error instead of falling
// back.
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
		return cached, nil
	}
	if !errors.Is(cacheErr, os.ErrNotExist) {
		slog.Debug("cached module catalog unusable; using embedded copy", "error", cacheErr)
	}
	return parseModuleCatalog(embeddedModuleCatalog)
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
