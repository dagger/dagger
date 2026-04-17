package workspace

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	neontoml "github.com/neongreen/mono/lib/toml"
)

// ConstructorArgHint captures a constructor-backed setting hint for a module.
type ConstructorArgHint struct {
	Name         string
	TypeLabel    string
	ExampleValue string
}

// UpdateConfigBytes rewrites config bytes while preserving existing comments
// and formatting when a prior file exists.
func UpdateConfigBytes(existingData []byte, cfg *Config) ([]byte, error) {
	return UpdateConfigBytesWithHints(existingData, cfg, nil)
}

// UpdateConfigBytesWithHints rewrites config bytes while preserving existing
// comments and formatting, then injects commented-out setting hints for the
// specified modules.
func UpdateConfigBytesWithHints(
	existingData []byte,
	cfg *Config,
	hints map[string][]ConstructorArgHint,
) ([]byte, error) {
	if cfg == nil {
		cfg = &Config{}
	}

	if len(existingData) == 0 {
		out := SerializeConfig(cfg)
		if len(hints) == 0 {
			return out, nil
		}
		return insertWorkspaceSettingHintComments(out, cfg, hints), nil
	}

	doc, err := neontoml.Parse(existingData)
	if err != nil {
		return nil, fmt.Errorf("parse existing config: %w", err)
	}

	existingCfg, err := ParseConfig(existingData)
	if err != nil {
		return nil, fmt.Errorf("parse existing config state: %w", err)
	}

	desiredValues := configDocumentMap(cfg)
	if err := deleteRemovedManagedConfigPaths(doc, configDocumentMap(existingCfg), desiredValues); err != nil {
		return nil, err
	}
	if err := deleteRemovedConfigRoots(doc, existingCfg, cfg); err != nil {
		return nil, err
	}
	if err := doc.ApplyMap(desiredValues); err != nil {
		return nil, fmt.Errorf("rewrite config document: %w", err)
	}
	if err := ensureEmptyEnvSections(doc, cfg.Env); err != nil {
		return nil, err
	}

	out := doc.Bytes()
	if len(hints) == 0 {
		return out, nil
	}
	return insertWorkspaceSettingHintComments(out, cfg, hints), nil
}

func configDocumentMap(cfg *Config) map[string]any {
	values := make(map[string]any)

	if len(cfg.Ignore) > 0 {
		values["ignore"] = append([]string(nil), cfg.Ignore...)
	}
	if cfg.DefaultsFromDotEnv {
		values["defaults_from_dotenv"] = true
	}
	if len(cfg.Modules) > 0 {
		modules := make(map[string]any, len(cfg.Modules))
		for name, entry := range cfg.Modules {
			module := map[string]any{
				"source": entry.Source,
			}
			if entry.Entrypoint {
				module["entrypoint"] = true
			}
			if entry.LegacyDefaultPath {
				module["legacy-default-path"] = true
			}
			if len(entry.Settings) > 0 {
				module["settings"] = cloneConfigMap(entry.Settings)
			}
			modules[name] = module
		}
		values["modules"] = modules
	}
	if len(cfg.Env) > 0 {
		envs := make(map[string]any, len(cfg.Env))
		for envName, env := range cfg.Env {
			envValue := map[string]any{}
			if len(env.Modules) > 0 {
				modules := make(map[string]any, len(env.Modules))
				for moduleName, overlay := range env.Modules {
					module := map[string]any{}
					if len(overlay.Settings) > 0 {
						module["settings"] = cloneConfigMap(overlay.Settings)
					}
					modules[moduleName] = module
				}
				envValue["modules"] = modules
			}
			envs[envName] = envValue
		}
		values["env"] = envs
	}

	return values
}

func deleteRemovedManagedConfigPaths(doc *neontoml.Document, existingValues, desiredValues map[string]any) error {
	existingFlat := flattenConfigValues(existingValues)
	desiredFlat := flattenConfigValues(desiredValues)

	for path := range existingFlat {
		if _, ok := desiredFlat[path]; ok {
			continue
		}
		if err := doc.Delete(path); err != nil {
			return fmt.Errorf("delete config path %q: %w", path, err)
		}
	}

	return nil
}

func flattenConfigValues(values map[string]any) map[string]any {
	flat := map[string]any{}
	flattenConfigValuesInto(flat, "", values)
	return flat
}

func flattenConfigValuesInto(flat map[string]any, prefix string, values map[string]any) {
	for key, value := range values {
		fullKey := formatConfigPathSegment(key)
		if prefix != "" {
			fullKey = prefix + "." + fullKey
		}

		nested, ok := value.(map[string]any)
		if ok {
			flattenConfigValuesInto(flat, fullKey, nested)
			continue
		}

		flat[fullKey] = value
	}
}

func deleteRemovedConfigRoots(doc *neontoml.Document, existingCfg, desiredCfg *Config) error {
	for name := range existingCfg.Modules {
		if _, ok := desiredCfg.Modules[name]; ok {
			continue
		}
		if err := doc.Delete("modules." + formatConfigPathSegment(name)); err != nil {
			return fmt.Errorf("delete module %q: %w", name, err)
		}
	}

	for envName := range existingCfg.Env {
		if _, ok := desiredCfg.Env[envName]; ok {
			continue
		}
		if err := doc.Delete("env." + formatConfigPathSegment(envName)); err != nil {
			return fmt.Errorf("delete env %q: %w", envName, err)
		}
	}

	return nil
}

func ensureEmptyEnvSections(doc *neontoml.Document, envs map[string]EnvOverlay) error {
	for envName, env := range envs {
		if len(env.Modules) > 0 {
			continue
		}

		placeholderPath := "env." + formatConfigPathSegment(envName) + ".__dagger_empty_section__"
		if err := doc.Set(placeholderPath, true); err != nil {
			return fmt.Errorf("create env %q section: %w", envName, err)
		}
		if err := doc.Delete(placeholderPath); err != nil {
			return fmt.Errorf("finalize env %q section: %w", envName, err)
		}
	}

	return nil
}

func insertWorkspaceSettingHintComments(data []byte, cfg *Config, hints map[string][]ConstructorArgHint) []byte {
	moduleNames := make([]string, 0, len(hints))
	for name := range hints {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)

	lines := strings.Split(string(data), "\n")
	for _, moduleName := range moduleNames {
		moduleHints := hints[moduleName]
		if len(moduleHints) == 0 {
			continue
		}

		insertAfter, hintPrefix := findModuleHintInsertionPoint(lines, moduleName)
		if insertAfter == -1 {
			continue
		}

		existingSettings := map[string]bool{}
		if entry, ok := cfg.Modules[moduleName]; ok {
			for key := range entry.Settings {
				existingSettings[strings.ToLower(key)] = true
			}
		}

		commentLines := make([]string, 0, len(moduleHints))
		for _, hint := range moduleHints {
			if existingSettings[strings.ToLower(hint.Name)] {
				continue
			}
			commentLines = append(commentLines,
				fmt.Sprintf("# %s%s = %s # %s", hintPrefix, hint.Name, hint.ExampleValue, hint.TypeLabel))
		}
		if len(commentLines) == 0 {
			continue
		}

		updated := make([]string, 0, len(lines)+len(commentLines))
		updated = append(updated, lines[:insertAfter+1]...)
		updated = append(updated, commentLines...)
		updated = append(updated, lines[insertAfter+1:]...)
		lines = updated
	}

	return []byte(strings.Join(lines, "\n"))
}

func findModuleHintInsertionPoint(lines []string, moduleName string) (insertAfter int, hintPrefix string) {
	settingsSection := "[modules." + moduleName + ".settings]"
	if idx := findSectionInsertionPoint(lines, settingsSection); idx != -1 {
		return idx, ""
	}

	moduleSection := "[modules." + moduleName + "]"
	if idx := findSectionInsertionPoint(lines, moduleSection); idx != -1 {
		return idx, "settings."
	}

	return -1, ""
}

func findSectionInsertionPoint(lines []string, sectionHeader string) int {
	inSection := false
	lastLine := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == sectionHeader {
			inSection = true
			lastLine = i
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			break
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			lastLine = i
		}
	}

	return lastLine
}

func formatConfigPathSegment(segment string) string {
	if isBareConfigPathSegment(segment) {
		return segment
	}
	return `"` + strings.ReplaceAll(segment, `"`, `\"`) + `"`
}

func isBareConfigPathSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return false
		}
	}
	return true
}
