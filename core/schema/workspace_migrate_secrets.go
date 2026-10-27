package schema

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
)

// secretSettingCandidate is a module setting whose value could be a secret
// address without a provider scheme. Whether it is one depends on the type of
// the constructor argument it configures.
type secretSettingCandidate struct {
	module string
	source string
	// path addresses the setting in the config file:
	// modules.<m>.settings.<k> or env.<e>.modules.<m>.settings.<k>.
	path  []string
	key   string
	value any
}

// migrateSecretSettings rewrites secret settings that rely on the secret
// address shorthands of earlier API versions, which current versions reject:
// a bare NAME for env://NAME, and <provider>:VALUE for <provider>://VALUE. The
// rewrite keeps the meaning those values had. A setting is a secret when its
// constructor argument is a Secret or [Secret], so the modules that have
// candidate settings are introspected to learn their argument types.
func (s *workspaceSchema) migrateSecretSettings(ctx context.Context, ws *core.Workspace, stage *workspaceMigrationStage) error {
	configPath := filepath.ToSlash(stage.configPath)
	data, err := core.DirectoryReadFile(ctx, stage.staged, configPath)
	if err != nil {
		return err
	}
	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		return fmt.Errorf("migrate %s: %w", stage.configPath, err)
	}
	candidates := secretSettingCandidates(cfg)
	if len(candidates) == 0 {
		return nil
	}

	introspectCtx, srv, err := workspaceSettingsHintIntrospectionContext(ctx, ws)
	if err != nil {
		return err
	}
	configDir := filepath.Dir(stage.configPath)
	modules := map[string]*core.Module{}
	updated := data
	for _, candidate := range candidates {
		mod, ok := modules[candidate.source]
		if !ok {
			// A module that cannot be loaded cannot run either; its settings
			// are left as they are, and fail at runtime and in doctor with
			// the missing scheme error once the module loads.
			if candidate.source != "" {
				mod, _ = introspectWorkspaceModule(introspectCtx, srv, ws, configDir, candidate.source)
			}
			modules[candidate.source] = mod
		}
		if mod == nil {
			continue
		}
		isSecret, isList := secretSettingType(mod, candidate.key)
		if !isSecret {
			continue
		}
		key := workspace.JoinConfigPath(append(candidate.path, candidate.key)...)
		updated, err = rewriteSecretSetting(updated, key, candidate.value, isList)
		if err != nil {
			return fmt.Errorf("migrate secret setting %s: %w", key, err)
		}
	}
	if bytes.Equal(updated, data) {
		return nil
	}

	after, err := withWorkspaceMigrationFile(ctx, stage.staged, stage.configPath, updated, "migrate secret settings")
	if err != nil {
		return err
	}
	changes, err := workspaceMigrationChanges(ctx, after, stage.staged)
	if err != nil {
		return err
	}
	stage.staged = after
	stage.legacy.Steps = append(stage.legacy.Steps, &core.WorkspaceMigrationStep{
		Code:        "secret-setting-schemes",
		Description: "Added provider schemes to secret settings",
		Changes:     changes,
	})
	return nil
}

// secretSettingCandidates lists the settings of every module and env overlay
// that hold a string, or strings, without a provider scheme, in a stable
// order.
func secretSettingCandidates(cfg *workspace.Config) []secretSettingCandidate {
	var candidates []secretSettingCandidate
	collect := func(module, source string, path []string, settings map[string]any) {
		for _, key := range sortedSettingKeys(settings) {
			if !hasSchemelessString(settings[key]) {
				continue
			}
			candidates = append(candidates, secretSettingCandidate{
				module: module,
				source: source,
				path:   path,
				key:    key,
				value:  settings[key],
			})
		}
	}
	for _, name := range sortedSettingKeys(cfg.Modules) {
		entry := cfg.Modules[name]
		collect(name, entry.Source, []string{"modules", name, "settings"}, entry.Settings)
	}
	for _, envName := range sortedSettingKeys(cfg.Env) {
		overlay := cfg.Env[envName]
		for _, name := range sortedSettingKeys(overlay.Modules) {
			module := overlay.Modules[name]
			source := module.Source
			if source == "" {
				source = cfg.Modules[name].Source
			}
			collect(name, source, []string{"env", envName, "modules", name, "settings"}, module.Settings)
		}
	}
	return candidates
}

// hasSchemelessString reports whether value holds a string without a provider
// scheme. A string is also checked element by element, since a list setting
// may be written as one comma-separated string.
func hasSchemelessString(value any) bool {
	switch v := value.(type) {
	case string:
		if v == "" {
			return false
		}
		if !hasSecretScheme(v) {
			return true
		}
		for _, elem := range strings.Split(v, ",") {
			if !hasSecretScheme(strings.TrimSpace(elem)) {
				return true
			}
		}
		return false
	case []any:
		for _, elem := range v {
			if hasSchemelessString(elem) {
				return true
			}
		}
	}
	return false
}

// secretSettingType reports whether key configures a Secret or [Secret]
// constructor argument of mod. Keys match argument names case-insensitively,
// as settings do at runtime.
func secretSettingType(mod *core.Module, key string) (isSecret, isList bool) {
	mainObj, ok := mod.MainObject()
	if !ok || !mainObj.Constructor.Valid || mainObj.Constructor.Value.Self() == nil {
		return false, false
	}
	for _, argRes := range mainObj.Constructor.Value.Self().Args {
		arg := argRes.Self()
		if arg == nil || (!strings.EqualFold(key, arg.OriginalName) && !strings.EqualFold(key, arg.Name)) {
			continue
		}
		typeDef := arg.TypeDef.Self()
		if typeDef == nil {
			return false, false
		}
		if typeDef.Kind == core.TypeDefKindList {
			if !typeDef.AsList.Valid || typeDef.AsList.Value.Self() == nil {
				return false, false
			}
			return isSecretTypeDef(typeDef.AsList.Value.Self().ElementTypeDef.Self()), true
		}
		return isSecretTypeDef(typeDef), false
	}
	return false, false
}

func isSecretTypeDef(typeDef *core.TypeDef) bool {
	return typeDef != nil &&
		typeDef.Kind == core.TypeDefKindObject &&
		typeDef.AsObject.Valid &&
		typeDef.AsObject.Value.Self() != nil &&
		typeDef.AsObject.Value.Self().Name == "Secret"
}

// rewriteSecretSetting writes value back at key with every secret address
// expanded to its full scheme form. A list setting written as one string is
// split the way list settings are, and written back as an array.
func rewriteSecretSetting(data []byte, key string, value any, isList bool) ([]byte, error) {
	if !isList {
		str, ok := value.(string)
		if !ok {
			return data, nil
		}
		rewritten := legacySecretAddress(str)
		if rewritten == str {
			return data, nil
		}
		return workspace.WriteConfigStringValue(data, key, rewritten)
	}
	var elements []string
	switch v := value.(type) {
	case string:
		parsed, err := workspace.ParseListValue(v)
		if err != nil {
			return nil, err
		}
		elements = parsed
	case []any:
		for _, elem := range v {
			str, ok := elem.(string)
			if !ok {
				return data, nil
			}
			elements = append(elements, str)
		}
	default:
		return data, nil
	}
	changed := false
	for i, elem := range elements {
		rewritten := legacySecretAddress(strings.TrimSpace(elem))
		changed = changed || rewritten != elem
		elements[i] = rewritten
	}
	if !changed {
		return data, nil
	}
	return workspace.WriteConfigValues(data, key, elements)
}

func sortedSettingKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
