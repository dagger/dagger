package workspace

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml"
)

// Config represents a parsed .dagger/config.toml workspace configuration.
type Config struct {
	Modules            map[string]ModuleEntry `toml:"modules"`
	Ignore             []string               `toml:"ignore"`
	DefaultsFromDotEnv bool                   `toml:"defaults_from_dotenv,omitempty"`
	Env                map[string]EnvOverlay  `toml:"env"`
}

// ModuleEntry represents a single module entry in the workspace config.
type ModuleEntry struct {
	Source            string         `toml:"source"`
	Config            map[string]any `toml:"config,omitempty"`
	Entrypoint        bool           `toml:"entrypoint,omitempty"`
	LegacyDefaultPath bool           `toml:"legacy-default-path,omitempty"`
}

// EnvOverlay is a named workspace environment overlay.
// It intentionally supports only a constrained subset of the root schema.
type EnvOverlay struct {
	Modules map[string]EnvModuleOverlay `toml:"modules"`
}

// EnvModuleOverlay is the environment-specific overlay for one installed module.
type EnvModuleOverlay struct {
	Config map[string]any `toml:"config,omitempty"`
}

// ResolveModuleEntrySource converts a workspace-config module source into the
// path that should actually be loaded or displayed from the workspace root.
// Relative local sources are resolved from the config directory; absolute local
// sources are preserved as-is.
func ResolveModuleEntrySource(configDir, source string) string {
	if source == "" || !isLocalRef(source, "") {
		return source
	}
	if filepath.IsAbs(source) {
		return filepath.Clean(source)
	}
	if configDir == "" {
		return filepath.Clean(source)
	}
	return filepath.Clean(filepath.Join(configDir, source))
}

// ParseConfig parses config.toml bytes into a workspace config.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config.toml: %w", err)
	}
	return &cfg, nil
}

// ApplyEnvOverlay returns a copy of cfg with the named environment overlay
// applied on top of the base module config.
//
// In v1, environments may only override [modules.<name>.config] values.
func ApplyEnvOverlay(cfg *Config, envName string) (*Config, error) {
	if cfg == nil {
		if envName == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("workspace env %q requires .dagger/config.toml", envName)
	}

	applied := cloneConfig(cfg)
	if envName == "" {
		return applied, nil
	}

	env, ok := cfg.Env[envName]
	if !ok {
		return nil, fmt.Errorf("workspace env %q is not defined", envName)
	}

	for moduleName, overlay := range env.Modules {
		entry, ok := applied.Modules[moduleName]
		if !ok {
			return nil, fmt.Errorf("workspace env %q references unknown module %q", envName, moduleName)
		}
		if entry.Config == nil {
			entry.Config = map[string]any{}
		}
		for key, value := range overlay.Config {
			entry.Config[key] = value
		}
		applied.Modules[moduleName] = entry
	}

	return applied, nil
}

// SerializeConfig serializes a workspace config into deterministic TOML.
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

	if cfg.DefaultsFromDotEnv {
		b.WriteString("defaults_from_dotenv = true\n\n")
	}

	if writeModuleEntries(&b, cfg.Modules) && len(cfg.Env) > 0 {
		b.WriteString("\n")
	}
	writeEnvEntries(&b, cfg.Env)

	return []byte(b.String())
}

func cloneConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}

	cloned := &Config{
		Ignore:             append([]string(nil), cfg.Ignore...),
		DefaultsFromDotEnv: cfg.DefaultsFromDotEnv,
	}
	if len(cfg.Modules) > 0 {
		cloned.Modules = make(map[string]ModuleEntry, len(cfg.Modules))
		for name, entry := range cfg.Modules {
			cloned.Modules[name] = ModuleEntry{
				Source:            entry.Source,
				Config:            cloneConfigMap(entry.Config),
				Entrypoint:        entry.Entrypoint,
				LegacyDefaultPath: entry.LegacyDefaultPath,
			}
		}
	}
	if len(cfg.Env) > 0 {
		cloned.Env = make(map[string]EnvOverlay, len(cfg.Env))
		for envName, env := range cfg.Env {
			clonedEnv := EnvOverlay{}
			if len(env.Modules) > 0 {
				clonedEnv.Modules = make(map[string]EnvModuleOverlay, len(env.Modules))
				for moduleName, overlay := range env.Modules {
					clonedEnv.Modules[moduleName] = EnvModuleOverlay{
						Config: cloneConfigMap(overlay.Config),
					}
				}
			}
			cloned.Env[envName] = clonedEnv
		}
	}
	return cloned
}

func cloneConfigMap(config map[string]any) map[string]any {
	if len(config) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(config))
	for key, value := range config {
		cloned[key] = value
	}
	return cloned
}

func writeModuleEntries(b *strings.Builder, modules map[string]ModuleEntry) bool {
	if len(modules) == 0 {
		return false
	}

	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, name := range names {
		if i > 0 {
			b.WriteString("\n")
		}

		entry := modules[name]
		fmt.Fprintf(b, "[modules.%s]\n", name)
		fmt.Fprintf(b, "source = %q\n", entry.Source)
		if entry.Entrypoint {
			b.WriteString("entrypoint = true\n")
		}
		if entry.LegacyDefaultPath {
			b.WriteString("legacy-default-path = true\n")
		}
		writeConfigTable(b, "modules."+name+".config", entry.Config, true)
	}

	return true
}

func writeEnvEntries(b *strings.Builder, envs map[string]EnvOverlay) bool {
	if len(envs) == 0 {
		return false
	}

	names := make([]string, 0, len(envs))
	for name := range envs {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, name := range names {
		if i > 0 {
			b.WriteString("\n")
		}

		env := envs[name]
		if len(env.Modules) == 0 {
			fmt.Fprintf(b, "[env.%s]\n", name)
			continue
		}

		moduleNames := make([]string, 0, len(env.Modules))
		for moduleName := range env.Modules {
			moduleNames = append(moduleNames, moduleName)
		}
		sort.Strings(moduleNames)

		for j, moduleName := range moduleNames {
			if j > 0 {
				b.WriteString("\n")
			}
			writeConfigTable(b, "env."+name+".modules."+moduleName+".config", env.Modules[moduleName].Config, false)
		}
	}

	return true
}

func writeConfigTable(b *strings.Builder, tablePath string, config map[string]any, leadingBlankLine bool) {
	if len(config) == 0 {
		return
	}

	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	if leadingBlankLine {
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "[%s]\n", tablePath)
	for _, key := range keys {
		fmt.Fprintf(b, "%s = %s\n", key, formatConfigValue(config[key]))
	}
}

// ReadConfigValue reads a value from config TOML at the given dotted key.
// When key is empty, it returns the full config contents.
func ReadConfigValue(data []byte, key string) (string, error) {
	if key == "" {
		return string(data), nil
	}

	tree, err := toml.LoadBytes(data)
	if err != nil {
		return "", fmt.Errorf("parse config: %w", err)
	}

	value := tree.GetPath(strings.Split(key, "."))
	if value == nil {
		return "", fmt.Errorf("key %q is not set", key)
	}

	switch v := value.(type) {
	case *toml.Tree:
		return flattenTOMLTree("", v), nil
	default:
		return formatScalarOutput(v), nil
	}
}

// WriteConfigValue writes a typed value to config TOML at the given dotted key.
func WriteConfigValue(existingData []byte, key string, rawValue string) ([]byte, error) {
	if key == "" {
		return nil, fmt.Errorf("key is required for writing")
	}
	if err := validateConfigKey(key); err != nil {
		return nil, err
	}

	var (
		tree *toml.Tree
		err  error
	)
	if len(existingData) > 0 {
		tree, err = toml.LoadBytes(existingData)
		if err != nil {
			return nil, fmt.Errorf("parse existing config: %w", err)
		}
	} else {
		tree, err = toml.TreeFromMap(map[string]interface{}{})
		if err != nil {
			return nil, fmt.Errorf("create config tree: %w", err)
		}
	}

	tree.SetPath(strings.Split(key, "."), parseValueString(key, rawValue))

	out, err := tree.ToTomlString()
	if err != nil {
		return nil, fmt.Errorf("serialize updated config: %w", err)
	}
	return []byte(out), nil
}

func flattenTOMLTree(prefix string, tree *toml.Tree) string {
	var lines []string
	for _, key := range tree.Keys() {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		switch value := tree.Get(key).(type) {
		case *toml.Tree:
			lines = append(lines, flattenTOMLTree(fullKey, value))
		default:
			lines = append(lines, fmt.Sprintf("%s = %s", fullKey, formatScalarTOML(value)))
		}
	}
	return strings.Join(lines, "\n")
}

func formatConfigValue(v any) string {
	switch value := v.(type) {
	case string:
		return fmt.Sprintf("%q", value)
	case bool:
		return strconv.FormatBool(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case int:
		return strconv.Itoa(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case []any:
		parts := make([]string, len(value))
		for i, item := range value {
			parts[i] = formatConfigValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		parts := make([]string, len(value))
		for i, item := range value {
			parts[i] = fmt.Sprintf("%q", item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%q", fmt.Sprint(v))
	}
}

func formatScalarOutput(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case []any:
		parts := make([]string, len(value))
		for i, item := range value {
			parts[i] = formatScalarOutput(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprint(v)
	}
}

func formatScalarTOML(v any) string {
	switch value := v.(type) {
	case string:
		return fmt.Sprintf("%q", value)
	case bool:
		return strconv.FormatBool(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case []any:
		parts := make([]string, len(value))
		for i, item := range value {
			parts[i] = formatScalarTOML(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprint(v)
	}
}

func validateConfigKey(key string) error {
	parts := strings.Split(key, ".")
	if len(parts) == 0 {
		return fmt.Errorf("key is required")
	}
	return validateKeyAgainstType(parts, reflect.TypeOf(Config{}), key)
}

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
	fieldType := field.Type

	switch fieldType.Kind() {
	case reflect.Map:
		if len(rest) == 0 {
			return fmt.Errorf("cannot set %q directly; specify a sub-key", fullKey)
		}

		mapValueRest := rest[1:]
		elemType := fieldType.Elem()
		if elemType.Kind() == reflect.Struct {
			if len(mapValueRest) == 0 {
				valid := validTOMLFieldNames(elemType)
				return fmt.Errorf("cannot set %q directly; specify a field like %s.%s", fullKey, fullKey, valid[0])
			}
			return validateKeyAgainstType(mapValueRest, elemType, fullKey)
		}
		if len(mapValueRest) > 0 {
			return fmt.Errorf("invalid key %q; config keys cannot be nested deeper", fullKey)
		}
		return nil
	default:
		if len(rest) > 0 {
			return fmt.Errorf("invalid key %q; %s does not have sub-keys", fullKey, parts[0])
		}
		return nil
	}
}

func findTOMLField(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("toml")
		tomlName := strings.Split(tag, ",")[0]
		if tomlName == name {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

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

func parseValueString(key string, rawValue string) any {
	parts := strings.Split(key, ".")
	leaf := parts[len(parts)-1]

	if leaf == "entrypoint" || leaf == "legacy-default-path" || key == "defaults_from_dotenv" {
		return rawValue == "true"
	}

	if rawValue == "true" || rawValue == "false" {
		return rawValue == "true"
	}
	if value, err := strconv.ParseInt(rawValue, 10, 64); err == nil {
		return value
	}
	if value, err := strconv.ParseFloat(rawValue, 64); err == nil {
		return value
	}
	if strings.Contains(rawValue, ",") {
		items := strings.Split(rawValue, ",")
		values := make([]string, len(items))
		for i, item := range items {
			values[i] = strings.TrimSpace(item)
		}
		return values
	}

	return rawValue
}
