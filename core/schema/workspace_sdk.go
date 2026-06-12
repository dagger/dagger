package schema

import (
	"fmt"
	"strings"

	"github.com/dagger/dagger/core/workspace"
)

func installedSDKSource(cfg *workspace.Config, name string) (workspace.ModuleEntry, string, error) {
	if cfg == nil || cfg.Modules == nil {
		return workspace.ModuleEntry{}, "", fmt.Errorf("%q is not installed as an SDK in this workspace; run `dagger sdk install %s` first", name, name)
	}
	entry, ok := cfg.Modules[name]
	if !ok || entry.AsSDK == nil {
		return workspace.ModuleEntry{}, "", fmt.Errorf("%q is not installed as an SDK in this workspace; run `dagger sdk install %s` first", name, name)
	}
	source := moduleEntrySourceWithPin(entry)
	if source == "" {
		return workspace.ModuleEntry{}, "", fmt.Errorf("SDK module %q has no source", name)
	}
	return entry, source, nil
}

func moduleEntrySourceWithPin(entry workspace.ModuleEntry) string {
	if entry.Pin == "" || strings.Contains(entry.Source, "@") {
		return entry.Source
	}
	return entry.Source + "@" + entry.Pin
}
