package workspace

import (
	"fmt"
	"maps"
	"slices"
)

// EntrypointName returns the installed name selected as the entrypoint, or an
// empty string when none is selected. Callers can pass an effective config.
func EntrypointName(cfg *Config) (string, error) {
	if cfg == nil {
		return "", nil
	}
	var names []string
	for name, entry := range cfg.Modules {
		if entry.Entrypoint {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	slices.Sort(names)
	if len(names) > 1 {
		return "", fmt.Errorf("multiple entrypoint modules: %q; run `dagger ws entrypoint NAME` to select one", names)
	}
	return names[0], nil
}

// SetEntrypoint selects an exact installed name and clears all other entrypoint
// flags. An empty name clears the selection. Validation precedes mutation, so
// callers can write the resulting config in one operation.
func SetEntrypoint(cfg *Config, configDir, name string) error {
	if name != "" {
		if cfg == nil {
			return fmt.Errorf("module %q is not installed; use an installed name from `dagger mod list`", name)
		}
		if _, ok := cfg.Modules[name]; !ok {
			return fmt.Errorf("module %q is not installed; use an installed name from `dagger mod list`", name)
		}
	}
	if cfg == nil {
		return nil
	}
	// Unnamed module scopes inherit the local entrypoint's installed name.
	// Record that name before removing the association, so regeneration cannot
	// fall back to the directory basename and rename an existing module.
	names := map[string][]string{}
	for installed, entry := range cfg.Modules {
		if !entry.Entrypoint || !IsLocalRef(entry.Source, entry.Pin) {
			continue
		}
		path, err := ResolveSDKManagedPath(configDir, entry.Source)
		if err != nil {
			return err
		}
		names[path] = append(names[path], installed)
	}
	sdks := maps.Clone(cfg.SDKs)
	for sdkName, sdk := range sdks {
		sdk.Scopes = maps.Clone(sdk.Scopes)
		for key, scope := range sdk.Scopes {
			if !scope.IsModule || scope.Name != "" {
				continue
			}
			path, err := ResolveSDKManagedPath(configDir, key)
			if err != nil {
				return err
			}
			// Ambiguous entrypoints have no unique inferred name to preserve.
			if previous := names[path]; len(previous) == 1 && previous[0] != name {
				scope.Name = previous[0]
				sdk.Scopes[key] = scope
			}
		}
		sdks[sdkName] = sdk
	}
	cfg.SDKs = sdks
	for installed, entry := range cfg.Modules {
		entry.Entrypoint = name != "" && installed == name
		cfg.Modules[installed] = entry
	}
	return nil
}
