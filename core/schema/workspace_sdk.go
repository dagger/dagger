package schema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dagger/dagger/core/workspace"
)

func installedSDKSource(cfg *workspace.Config, name string) (string, workspace.ModuleEntry, string, error) {
	if cfg == nil || cfg.Modules == nil {
		return "", workspace.ModuleEntry{}, "", fmt.Errorf("%q is not installed as an SDK in this workspace; run `dagger sdk install %s` first", name, name)
	}
	if entry, ok := cfg.Modules[name]; ok && entry.AsSDK != nil {
		return installedSDKSourceForModule(name, entry)
	}

	var matches []string
	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK == nil || entry.AsSDK.Name != name {
			continue
		}
		matches = append(matches, moduleName)
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", workspace.ModuleEntry{}, "", fmt.Errorf("%q is not installed as an SDK in this workspace; run `dagger sdk install %s` first", name, name)
	case 1:
		entry := cfg.Modules[matches[0]]
		return installedSDKSourceForModule(matches[0], entry)
	default:
		return "", workspace.ModuleEntry{}, "", fmt.Errorf("SDK name %q is ambiguous: matches modules.%s.as-sdk; choose a unique as-sdk.name", name, strings.Join(matches, ".as-sdk, modules."))
	}
}

func installedSDKSourceForModule(moduleName string, entry workspace.ModuleEntry) (string, workspace.ModuleEntry, string, error) {
	source := moduleEntrySourceWithPin(entry)
	if source == "" {
		return "", workspace.ModuleEntry{}, "", fmt.Errorf("SDK module %q has no source", moduleName)
	}
	return moduleName, entry, source, nil
}

func moduleEntrySourceWithPin(entry workspace.ModuleEntry) string {
	if entry.Pin == "" || strings.Contains(entry.Source, "@") {
		return entry.Source
	}
	return entry.Source + "@" + entry.Pin
}
