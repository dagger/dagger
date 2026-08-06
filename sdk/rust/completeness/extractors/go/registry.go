package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// PathsFromRegistry reads only path selectors from one reviewed authority boundary.
//
// The registry remains an authored Rust contract input. This adapter merely prevents
// Dagger automation from maintaining a second, drifting copy of its Go file list.
func PathsFromRegistry(path, authorityID string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read authority registry: %w", err)
	}
	var registry struct {
		Authorities map[string]struct {
			Include []map[string]struct {
				Path string `json:"path"`
			}
		} `json:"authorities"`
	}
	if err := json.Unmarshal(contents, &registry); err != nil {
		return nil, fmt.Errorf("decode authority registry: %w", err)
	}
	authority, ok := registry.Authorities[authorityID]
	if !ok {
		return nil, fmt.Errorf("authority %q is absent", authorityID)
	}
	paths := make([]string, 0, len(authority.Include))
	for _, selector := range authority.Include {
		pathSelector, ok := selector["path"]
		if !ok || pathSelector.Path == "" || len(selector) != 1 {
			return nil, fmt.Errorf("authority %q contains a non-path include selector", authorityID)
		}
		paths = append(paths, pathSelector.Path)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("authority %q selects no paths", authorityID)
	}
	return paths, nil
}
