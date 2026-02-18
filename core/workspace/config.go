package workspace

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	neontoml "github.com/neongreen/mono/lib/toml"
	toml "github.com/pelletier/go-toml"
)

// Config represents a parsed .dagger/config.toml workspace configuration.
type Config struct {
	Modules          map[string]ModuleEntry `toml:"modules"`
	Ignore           []string               `toml:"ignore"`
	DefaultsFromDotEnv bool                 `toml:"defaults_from_dotenv,omitempty"`
}

// ModuleEntry represents a single module entry in the workspace config.
type ModuleEntry struct {
	Source string         `toml:"source"`
	Config map[string]any `toml:"config,omitempty"`
	Blueprint bool        `toml:"blueprint,omitempty"`
}

// ParseConfig parses a config.toml file from raw bytes.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config.toml: %w", err)
	}
	return &cfg, nil
}

// SerializeConfig serializes a Config to TOML bytes.
// Uses section-header format: [modules.<name>] and [modules.<name>.config].
func SerializeConfig(cfg *Config) []byte {
	var b strings.Builder

	if len(cfg.Ignore) > 0 {
		b.WriteString("ignore = [")
		for i, pat := range cfg.Ignore {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", pat)
		}
		b.WriteString("]\n\n")
	}

	if len(cfg.Modules) > 0 {
		// Sort module names for deterministic output
		names := make([]string, 0, len(cfg.Modules))
		for name := range cfg.Modules {
			names = append(names, name)
		}
		sort.Strings(names)

		for i, name := range names {
			if i > 0 {
				b.WriteString("\n")
			}
			entry := cfg.Modules[name]
			fmt.Fprintf(&b, "[modules.%s]\n", name)
			fmt.Fprintf(&b, "source = %q\n", entry.Source)
			if entry.Blueprint {
				b.WriteString("blueprint = true\n")
			}
			if len(entry.Config) > 0 {
				// Sort config keys for deterministic output
				keys := make([]string, 0, len(entry.Config))
				for k := range entry.Config {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				fmt.Fprintf(&b, "\n[modules.%s.config]\n", name)
				for _, k := range keys {
					fmt.Fprintf(&b, "%s = %s\n", k, formatConfigValue(entry.Config[k]))
				}
			}
		}
	}

	return []byte(b.String())
}

// ConstructorArgHint captures information about a module constructor argument
// for generating commented-out hint lines in config.toml.
type ConstructorArgHint struct {
	Name         string // lowerCamelCase arg name (used as config key)
	ExampleValue string // TOML-formatted example value, e.g. `""`, `false`, `"alpine:latest"`
	Configurable bool   // whether this arg type can be set via config.toml
}

// CommentSuffix returns the trailing comment for this hint (empty if configurable).
func (h ConstructorArgHint) CommentSuffix() string {
	if !h.Configurable {
		return " # not configurable via config"
	}
	return ""
}

// SerializeConfigWithHints serializes a Config to TOML bytes, preserving existing
// comments from existingTOML and inserting commented-out hint lines for constructor args.
func SerializeConfigWithHints(cfg *Config, existingTOML []byte, hints map[string][]ConstructorArgHint) ([]byte, error) {
	// Parse existing TOML or create fresh document
	var doc *neontoml.Document
	var err error
	if len(existingTOML) > 0 {
		doc, err = neontoml.Parse(existingTOML)
		if err != nil {
			return nil, fmt.Errorf("parsing existing config: %w", err)
		}
	} else {
		// For fresh documents, use SerializeConfig to generate the initial TOML
		// in section-header format, then parse it so neontoml can preserve that format.
		fresh := SerializeConfig(cfg)
		doc, err = neontoml.Parse(fresh)
		if err != nil {
			return nil, fmt.Errorf("creating fresh document: %w", err)
		}
	}

	// Set module entries via the comment-preserving library
	names := make([]string, 0, len(cfg.Modules))
	for name := range cfg.Modules {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry := cfg.Modules[name]
		if err := doc.Set(fmt.Sprintf("modules.%s.source", name), entry.Source); err != nil {
			return nil, fmt.Errorf("setting modules.%s.source: %w", name, err)
		}
		if entry.Blueprint {
			if err := doc.Set(fmt.Sprintf("modules.%s.blueprint", name), true); err != nil {
				return nil, fmt.Errorf("setting modules.%s.blueprint: %w", name, err)
			}
		}
		if len(entry.Config) > 0 {
			keys := make([]string, 0, len(entry.Config))
			for k := range entry.Config {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if err := doc.Set(fmt.Sprintf("modules.%s.config.%s", name, k), entry.Config[k]); err != nil {
					return nil, fmt.Errorf("setting modules.%s.config.%s: %w", name, k, err)
				}
			}
		}
	}

	// Serialize, then insert hint comments
	tomlStr := doc.String()
	if len(hints) > 0 {
		tomlStr = insertHintComments(tomlStr, cfg, hints)
	}

	return []byte(tomlStr), nil
}

// insertHintComments injects commented-out config hint lines into the serialized
// TOML string, positioned after each module's last real key.
// Handles both dotted-key format (moduleName.source = ...) and section-header
// format ([modules.moduleName] / source = ...).
func insertHintComments(tomlStr string, cfg *Config, hints map[string][]ConstructorArgHint) string {
	// Sort module names for deterministic output
	moduleNames := make([]string, 0, len(hints))
	for name := range hints {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)

	lines := strings.Split(tomlStr, "\n")

	for _, moduleName := range moduleNames {
		argHints := hints[moduleName]
		if len(argHints) == 0 {
			continue
		}

		// Build the set of config keys already set for this module
		existingConfigKeys := make(map[string]bool)
		if entry, ok := cfg.Modules[moduleName]; ok && entry.Config != nil {
			for k := range entry.Config {
				existingConfigKeys[strings.ToLower(k)] = true
			}
		}

		// Find insertion point and determine whether keys are under a section header.
		// If under [modules.name], comment keys should be relative (config.x = ...),
		// so that uncommenting produces the correct fully-qualified path.
		// If under [modules] with dotted keys, use fully-qualified (name.config.x = ...).
		insertIdx, useRelativeKeys := findModuleInsertionPoint(lines, moduleName)
		if insertIdx == -1 {
			continue
		}

		// Build comment lines, skipping args that already have a config entry
		var commentLines []string
		for _, hint := range argHints {
			if existingConfigKeys[strings.ToLower(hint.Name)] {
				continue
			}
			if useRelativeKeys {
				// Under a section header, use config-prefixed key names
				// so uncommenting produces config.<key> under [modules.<name>]
				commentLines = append(commentLines,
					fmt.Sprintf("# config.%s = %s%s", hint.Name, hint.ExampleValue, hint.CommentSuffix()))
			} else {
				commentLines = append(commentLines,
					fmt.Sprintf("# %s.config.%s = %s%s", moduleName, hint.Name, hint.ExampleValue, hint.CommentSuffix()))
			}
		}
		if len(commentLines) == 0 {
			continue
		}

		// Insert comment lines after the insertion point
		newLines := make([]string, 0, len(lines)+len(commentLines))
		newLines = append(newLines, lines[:insertIdx+1]...)
		newLines = append(newLines, commentLines...)
		newLines = append(newLines, lines[insertIdx+1:]...)
		lines = newLines
	}

	return strings.Join(lines, "\n")
}

// findModuleInsertionPoint locates where to insert hint comments for a module.
// Returns the line index to insert after, and whether the module uses a section
// header (meaning comment keys should be relative, not fully qualified).
func findModuleInsertionPoint(lines []string, moduleName string) (insertIdx int, useRelativeKeys bool) {
	// Strategy 1: section header [modules.moduleName.config] or [modules.moduleName]
	// Prefer inserting after [modules.moduleName.config] section if it exists,
	// otherwise after the [modules.moduleName] section.
	configSectionHeader := "[modules." + moduleName + ".config]"
	sectionHeader := "[modules." + moduleName + "]"
	inSection := false
	lastSectionIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == configSectionHeader || trimmed == sectionHeader {
			inSection = true
			lastSectionIdx = i
			continue
		}
		if inSection {
			// Another section header ends this section (but config subsection continues it)
			if strings.HasPrefix(trimmed, "[") {
				if trimmed == configSectionHeader {
					// Config subsection is part of this module, keep going
					lastSectionIdx = i
					continue
				}
				break
			}
			// Track last non-empty, non-comment line as the insertion point
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				lastSectionIdx = i
			}
		}
	}
	if lastSectionIdx != -1 {
		return lastSectionIdx, true
	}

	// Strategy 2: dotted keys under [modules] — lines starting with "moduleName."
	dottedPrefix := moduleName + "."
	lastDottedIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, dottedPrefix) {
			lastDottedIdx = i
		}
	}
	if lastDottedIdx != -1 {
		return lastDottedIdx, false
	}

	return -1, false
}

// formatConfigValue formats a config value for TOML serialization.
func formatConfigValue(v any) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case bool:
		return strconv.FormatBool(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case []any:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = formatConfigValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = fmt.Sprintf("%q", item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%q", fmt.Sprint(v))
	}
}

// ReadConfigValue reads a value from the config TOML at the given key path.
// If key is empty, returns the full config file content.
// For scalar values, returns the raw value string.
// For non-scalar (table) values, returns a flattened dotted-key representation.
func ReadConfigValue(data []byte, key string) (string, error) {
	if key == "" {
		return string(data), nil
	}

	tree, err := toml.LoadBytes(data)
	if err != nil {
		return "", fmt.Errorf("parsing config: %w", err)
	}

	keys := strings.Split(key, ".")
	val := tree.GetPath(keys)
	if val == nil {
		return "", fmt.Errorf("key %q is not set", key)
	}

	switch v := val.(type) {
	case *toml.Tree:
		return flattenTOMLTree("", v), nil
	default:
		return formatScalarOutput(v), nil
	}
}

// flattenTOMLTree recursively flattens a TOML tree into dotted-key format.
func flattenTOMLTree(prefix string, tree *toml.Tree) string {
	var lines []string
	for _, key := range tree.Keys() {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}
		val := tree.Get(key)
		switch v := val.(type) {
		case *toml.Tree:
			lines = append(lines, flattenTOMLTree(fullKey, v))
		default:
			lines = append(lines, fmt.Sprintf("%s = %s", fullKey, formatScalarTOML(v)))
		}
	}
	return strings.Join(lines, "\n")
}

// formatScalarOutput formats a scalar value for stdout output (no TOML quoting).
func formatScalarOutput(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case []any:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = formatScalarOutput(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprint(v)
	}
}

// formatScalarTOML formats a scalar value with TOML quoting (strings are quoted).
func formatScalarTOML(v any) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case bool:
		return strconv.FormatBool(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case []any:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = formatScalarTOML(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprint(v)
	}
}

// WriteConfigValue writes a value to the config TOML at the given key path.
// It validates the key against the config schema, parses the value string
// into the appropriate type, and preserves existing comments and formatting.
func WriteConfigValue(existingData []byte, key string, rawValue string) ([]byte, error) {
	if key == "" {
		return nil, fmt.Errorf("key is required for writing")
	}

	if err := validateConfigKey(key); err != nil {
		return nil, err
	}

	value := parseValueString(key, rawValue)

	var doc *neontoml.Document
	var err error
	if len(existingData) > 0 {
		doc, err = neontoml.Parse(existingData)
		if err != nil {
			return nil, fmt.Errorf("parsing existing config: %w", err)
		}
	} else {
		doc, err = neontoml.ParseString("")
		if err != nil {
			return nil, fmt.Errorf("creating empty document: %w", err)
		}
	}

	if err := doc.Set(key, value); err != nil {
		return nil, fmt.Errorf("setting %q: %w", key, err)
	}

	return doc.Bytes(), nil
}

// validateConfigKey checks that a key path is valid according to the config schema.
//
// Valid paths:
//   - ignore (the ignore list)
//   - modules.<name>.source
//   - modules.<name>.blueprint
//   - modules.<name>.config.<key>
//
// validateConfigKey ensures the given dotted key path corresponds to a valid
// config.toml schema path, derived from Config and ModuleEntry struct tags.
func validateConfigKey(key string) error {
	parts := strings.Split(key, ".")
	if len(parts) == 0 {
		return fmt.Errorf("key is required")
	}
	return validateKeyAgainstType(parts, reflect.TypeOf(Config{}), key)
}

// validateKeyAgainstType recursively validates a key path against a struct type,
// using toml struct tags to determine valid field names and nesting rules.
func validateKeyAgainstType(parts []string, t reflect.Type, fullKey string) error {
	if len(parts) == 0 {
		return nil
	}

	field, ok := findTOMLField(t, parts[0])
	if !ok {
		return fmt.Errorf("unknown config key %q; valid fields at this level: %s",
			fullKey, strings.Join(validTOMLFieldNames(t), ", "))
	}

	rest := parts[1:]
	ft := field.Type

	switch ft.Kind() {
	case reflect.Map:
		// Maps require at least one more level for the map key
		if len(rest) == 0 {
			return fmt.Errorf("cannot set %q directly; specify a sub-key", fullKey)
		}
		// rest[0] is the map key (arbitrary string), skip it
		mapValueRest := rest[1:]
		elemType := ft.Elem()

		if elemType.Kind() == reflect.Struct {
			// map[string]SomeStruct — validate remaining parts against the struct
			if len(mapValueRest) == 0 {
				return fmt.Errorf("cannot set %q directly; specify a field like %s.%s",
					fullKey, fullKey, validTOMLFieldNames(elemType)[0])
			}
			return validateKeyAgainstType(mapValueRest, elemType, fullKey)
		}

		// map[string]any or map[string]primitive — the map key IS the leaf
		if len(mapValueRest) > 0 {
			return fmt.Errorf("invalid key %q; config keys cannot be nested deeper", fullKey)
		}
		return nil

	default:
		// Scalar, slice, or other leaf fields — no sub-keys allowed
		if len(rest) > 0 {
			return fmt.Errorf("invalid key %q; %s does not have sub-keys", fullKey, parts[0])
		}
		return nil
	}
}

// findTOMLField returns the struct field matching the given TOML key name.
func findTOMLField(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("toml")
		tomlName := strings.Split(tag, ",")[0]
		if tomlName == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// validTOMLFieldNames returns the TOML key names for all exported fields of a struct.
func validTOMLFieldNames(t reflect.Type) []string {
	var names []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("toml")
		name := strings.Split(tag, ",")[0]
		if name != "" && name != "-" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// parseValueString converts a raw value string into a typed Go value,
// using the key path to inform type expectations.
func parseValueString(key string, rawValue string) any {
	parts := strings.Split(key, ".")

	// modules.<name>.blueprint is always a bool
	if len(parts) == 3 && parts[0] == "modules" && parts[2] == "blueprint" {
		return rawValue == "true"
	}

	// Try bool
	if rawValue == "true" || rawValue == "false" {
		return rawValue == "true"
	}

	// Try integer
	if n, err := strconv.ParseInt(rawValue, 10, 64); err == nil {
		return n
	}

	// Try float
	if f, err := strconv.ParseFloat(rawValue, 64); err == nil {
		return f
	}

	// Try comma-separated array (only if commas are present)
	if strings.Contains(rawValue, ",") {
		items := strings.Split(rawValue, ",")
		result := make([]string, len(items))
		for i, item := range items {
			result[i] = strings.TrimSpace(item)
		}
		return result
	}

	return rawValue
}
