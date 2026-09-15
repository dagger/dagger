package workspace

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml"
)

// MigrateConfigBytes moves beta SDK declarations into the SDK registry. It
// changes only fields with a known replacement; unsupported legacy data aborts
// the migration before any file is written.
func MigrateConfigBytes(data []byte, configDir string) ([]byte, error) {
	cfg, err := ParseConfig(data)
	if err != nil {
		return nil, err
	}
	tree, err := toml.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	// The current schema has no environment-specific SDK registry. Moving an
	// environment's role into the base config would change other environments.
	if envKey, found := configDecoderKey(tree, "env"); found {
		envs, _ := tree.Get(envKey).(*toml.Tree)
		for _, env := range sortedConfigKeys(envs) {
			envTree, _ := envs.GetPath([]string{env}).(*toml.Tree)
			if modulesKey, found := configDecoderKey(envTree, "modules"); found {
				mods, _ := envTree.Get(modulesKey).(*toml.Tree)
				for _, name := range sortedConfigKeys(mods) {
					module, _ := mods.GetPath([]string{name}).(*toml.Tree)
					if asSDKKey, found := configDecoderKey(module, "as-sdk"); found {
						return nil, fmt.Errorf("cannot migrate %s: environment-specific SDK roles have no current replacement; field retained", JoinConfigPath(envKey, env, modulesKey, name, asSDKKey))
					}
				}
			}
		}
	}

	var remove [][]string
	modulesKey, _ := configDecoderKey(tree, "modules")
	modules, _ := tree.Get(modulesKey).(*toml.Tree)
	for _, moduleName := range slices.Sorted(maps.Keys(cfg.Modules)) {
		module, _ := modules.GetPath([]string{moduleName}).(*toml.Tree)
		asSDKKey, found := configDecoderKey(module, "as-sdk")
		if !found {
			continue
		}
		parts := []string{modulesKey, moduleName, asSDKKey}
		legacy, ok := module.Get(asSDKKey).(*toml.Tree)
		if !ok {
			return nil, fmt.Errorf("cannot migrate %s: expected a table; field retained", JoinConfigPath(parts...))
		}
		if err := checkLegacySDKFields(legacy, parts, "name", "modules", "clients"); err != nil {
			return nil, err
		}
		sdkName, err := legacySDKString(legacy, "name", parts, false)
		if err != nil {
			return nil, err
		}
		if sdkName == "" {
			sdkName = ConventionalSDKName(moduleName)
		}
		if cfg.SDKs == nil {
			cfg.SDKs = map[string]SDKEntry{}
		}
		sdk, exists := cfg.SDKs[sdkName]
		if exists && sdk.Module != moduleName {
			return nil, fmt.Errorf("cannot migrate %s: SDK %q already uses module %q; field retained", JoinConfigPath(parts...), sdkName, sdk.Module)
		}
		if name, found := SDKNameForModule(cfg, moduleName); found && name != sdkName {
			return nil, fmt.Errorf("cannot migrate %s: module already provides SDK %q, but legacy SDK name is %q; field retained", JoinConfigPath(parts...), name, sdkName)
		}
		sdk.Module = moduleName
		if err := migrateLegacySDKScopes(tree, legacy, &sdk, configDir, sdkName, parts); err != nil {
			return nil, err
		}
		cfg.SDKs[sdkName] = sdk
		remove = append(remove, parts)
	}
	if len(remove) == 0 {
		return data, nil
	}
	updated, err := UpdateConfigBytes(data, cfg)
	if err != nil {
		return nil, err
	}
	for _, parts := range remove {
		doc, err := parseConfigText(updated)
		if err != nil {
			return nil, err
		}
		updated, err = doc.remove(parts)
		if err != nil {
			return nil, err
		}
	}
	if _, err := ParseConfig(updated); err != nil {
		return nil, fmt.Errorf("validate migrated config: %w", err)
	}
	return updated, nil
}

func migrateLegacySDKScopes(tree, legacy *toml.Tree, sdk *SDKEntry, configDir, sdkName string, parts []string) error {
	if sdk.Scopes == nil {
		sdk.Scopes = map[string]SDKScope{}
	}
	for _, kind := range []string{"modules", "clients"} {
		records, err := legacySDKRecords(legacy, kind, parts)
		if err != nil {
			return err
		}
		for i, record := range records {
			recordPath := append(slices.Clone(parts), kind, fmt.Sprint(i))
			p, err := legacySDKString(record, "path", recordPath, true)
			if err != nil {
				return err
			}
			resolved, err := ResolveSDKManagedPath(configDir, p)
			if err != nil {
				return fmt.Errorf("cannot migrate %s: %w", JoinConfigPath(recordPath...), err)
			}
			key, found, err := SDKScopeKey(*sdk, configDir, resolved)
			if err != nil {
				return err
			}
			if !found {
				key = p // Preserve the recorded path spelling.
			}
			scope := sdk.Scopes[key]
			if kind == "modules" {
				if err := checkLegacySDKFields(record, recordPath, "path"); err != nil {
					return err
				}
				if explicit, ok := configSDKScopeIsModule(tree, sdkName, key); ok && !explicit {
					return fmt.Errorf("cannot migrate %s: SDK %q scope %q explicitly sets is-module = false; field retained", JoinConfigPath(recordPath...), sdkName, key)
				}
				scope.IsModule = true
			} else {
				target, settings, err := legacySDKClient(record, recordPath)
				if err != nil {
					return err
				}
				merged, conflicts := mergeSDKScopes(scope, SDKScope{IsModule: scope.IsModule, Clients: []string{target}, Settings: settings})
				if len(conflicts) > 0 {
					return fmt.Errorf("cannot migrate %s: SDK %q scope %q conflicts in %s; field retained", JoinConfigPath(recordPath...), sdkName, key, strings.Join(conflicts, ", "))
				}
				scope = merged
			}
			sdk.Scopes[key] = scope
		}
	}
	return nil
}

func sortedConfigKeys(tree *toml.Tree) []string {
	keys := tree.Keys()
	slices.Sort(keys)
	return keys
}

func checkLegacySDKFields(tree *toml.Tree, parts []string, allowed ...string) error {
	known := map[string]bool{}
	for _, name := range allowed {
		if key, found := configDecoderKey(tree, name); found {
			known[key] = true
		}
	}
	for _, key := range sortedConfigKeys(tree) {
		if !known[key] {
			return fmt.Errorf("cannot migrate %s: unsupported legacy field %s; field retained", JoinConfigPath(parts...), JoinConfigPath(append(slices.Clone(parts), key)...))
		}
	}
	return nil
}

func legacySDKString(tree *toml.Tree, key string, parts []string, required bool) (string, error) {
	actual, found := configDecoderKey(tree, key)
	var value any
	if found {
		value = tree.Get(actual)
	}
	if value == nil && !required {
		return "", nil
	}
	if str, ok := value.(string); ok && (!required || str != "") {
		return str, nil
	}
	qualifier := ""
	if required {
		qualifier = "non-empty "
	}
	return "", fmt.Errorf("cannot migrate %s: %s must be a %sstring; field retained", JoinConfigPath(parts...), key, qualifier)
}

func legacySDKRecords(tree *toml.Tree, key string, parts []string) ([]*toml.Tree, error) {
	actual, found := configDecoderKey(tree, key)
	var value any
	if found {
		value = tree.Get(actual)
	}
	if value == nil {
		return nil, nil
	}
	if records, ok := value.([]*toml.Tree); ok {
		return records, nil
	}
	// TOML represents an empty array without an element type.
	if records, ok := value.([]any); ok && len(records) == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("cannot migrate %s: %s must be an array of tables; field retained", JoinConfigPath(parts...), key)
}

func legacySDKClient(tree *toml.Tree, parts []string) (string, map[string]any, error) {
	target, err := legacySDKString(tree, "module", parts, true)
	if err != nil {
		return "", nil, err
	}
	pin, err := legacySDKString(tree, "pin", parts, false)
	if err != nil {
		return "", nil, err
	}
	if pin != "" {
		if IsLocalRef(target, "") {
			return "", nil, fmt.Errorf("cannot migrate %s: a local client target has a pin; field retained", JoinConfigPath(parts...))
		}
		// Preserve an immutable pin instead of silently changing the generated
		// client's target. An earlier @ may be SSH userinfo and must remain.
		if i := strings.LastIndex(target, "@"); i > strings.LastIndex(target, "/") {
			target = target[:i]
		}
		target += "@" + pin
	}
	settings := map[string]any{}
	known := map[string]bool{}
	for _, name := range []string{"path", "module", "pin"} {
		if key, found := configDecoderKey(tree, name); found {
			known[key] = true
		}
	}
	for _, key := range sortedConfigKeys(tree) {
		if known[key] {
			continue
		}
		// The beta schema stored client options as extra string fields.
		value, ok := tree.GetPath([]string{key}).(string)
		if !ok {
			return "", nil, fmt.Errorf("cannot migrate %s: unsupported legacy client field %s; field retained", JoinConfigPath(parts...), key)
		}
		settings[key] = value
	}
	return target, settings, nil
}

func configSDKScopeIsModule(tree *toml.Tree, sdkName, scopeKey string) (bool, bool) {
	sdksKey, found := configDecoderKey(tree, "sdks")
	if !found {
		return false, false
	}
	sdks, _ := tree.Get(sdksKey).(*toml.Tree)
	sdk, _ := sdks.GetPath([]string{sdkName}).(*toml.Tree)
	scopesKey, found := configDecoderKey(sdk, "scopes")
	if !found {
		return false, false
	}
	scopes, _ := sdk.Get(scopesKey).(*toml.Tree)
	scope, _ := scopes.GetPath([]string{scopeKey}).(*toml.Tree)
	isModuleKey, found := configDecoderKey(scope, "is-module")
	if !found {
		return false, false
	}
	explicit, ok := scope.Get(isModuleKey).(bool)
	return explicit, ok
}
