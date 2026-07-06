package workspace

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	toml "github.com/pelletier/go-toml"
)

// Config represents a parsed dagger.toml workspace configuration.
type Config struct {
	Modules            map[string]ModuleEntry `json:"modules,omitempty" toml:"modules"`
	Ignore             []string               `json:"ignore,omitempty" toml:"ignore"`
	DefaultsFromDotEnv bool                   `json:"defaults_from_dotenv,omitempty" toml:"defaults_from_dotenv,omitempty"`
	// CheckGenerated controls whether `dagger check` runs generate-as-checks,
	// which fail when generated files are stale. Defaults to true; set false to
	// skip them (like --no-generate). CLI flags override it.
	CheckGenerated *bool                  `json:"check-generated,omitempty" toml:"check-generated,omitempty"`
	Env            map[string]EnvOverlay  `json:"env,omitempty" toml:"env"`
	Ports          map[string]PortMapping `json:"ports,omitempty" toml:"ports,omitempty"`
}

// PortMapping declares a host port that forwards to a workspace service.
// The map key on Config.Ports is the host port (string for TOML key shape:
// `[ports.3000]`). BackendService is the service path scoped under a workspace
// module (e.g. "hello-with-services:web").
type PortMapping struct {
	BackendService string `json:"backendService" toml:"backendService"`
	BackendPort    int    `json:"backendPort" toml:"backendPort"`
}

// runtimeSettingNamesBySDK maps a builtin SDK name to the module setting keys
// consumed by that SDK's engine-side runtime install (via the module source's
// SDK config) rather than by the module's constructor. They share the
// [modules.<name>.settings] namespace with constructor-arg settings, so a key
// is only reserved for the SDK that consumes it: other SDKs keep the same
// name free for their own constructor args.
var runtimeSettingNamesBySDK = map[string][]string{"go": {"goprivate"}}

// RuntimeSettingNamesForSDK returns the runtime setting keys consumed by the
// given SDK source ref (builtin name, optionally version-suffixed).
func RuntimeSettingNamesForSDK(sdkSource string) []string {
	name, _, _ := strings.Cut(sdkSource, "@")
	return runtimeSettingNamesBySDK[name]
}

// RuntimeSettingsJSON extracts the runtime settings consumed by the given SDK
// from a module settings map, JSON-encoded for the module source's
// _withRuntimeSettings field. Returns "" when the SDK consumes none.
func RuntimeSettingsJSON(sdkSource string, settings map[string]any) (string, error) {
	runtimeSettings := map[string]any{}
	for _, name := range RuntimeSettingNamesForSDK(sdkSource) {
		if value, ok := settings[name]; ok {
			runtimeSettings[name] = value
		}
	}
	if len(runtimeSettings) == 0 {
		return "", nil
	}
	out, err := json.Marshal(runtimeSettings)
	if err != nil {
		return "", fmt.Errorf("encoding runtime settings: %w", err)
	}
	return string(out), nil
}

// ModuleEntry represents a single module entry in the workspace config.
type ModuleEntry struct {
	Source            string         `json:"source" toml:"source"`
	Pin               string         `json:"pin,omitempty" toml:"pin,omitempty"`
	Settings          map[string]any `json:"settings,omitempty" toml:"settings,omitempty"`
	Entrypoint        bool           `json:"entrypoint,omitempty" toml:"entrypoint,omitempty"`
	LegacyDefaultPath bool           `json:"legacy-default-path,omitempty" toml:"legacy-default-path,omitempty"`
	Up                ModuleSkip     `json:"up,omitempty" toml:"up,omitempty"`
	Generate          ModuleSkip     `json:"generate,omitempty" toml:"generate,omitempty"`
	Check             ModuleSkip     `json:"check,omitempty" toml:"check,omitempty"`

	// AsSDK is the SDK-role data for module entries that serve as SDKs in
	// this workspace. Its presence (any populated sub-field) marks the
	// module as installed *as* an SDK; absence means it's a plain installed
	// module. The role data — which authored modules and generated clients
	// this SDK manages locally — lives nested rather than in a parallel
	// top-level section so settings, install, and SDK metadata all
	// converge on a single [modules.<name>.*] entry.
	AsSDK *ModuleAsSDK `json:"as-sdk,omitempty" toml:"as-sdk,omitempty"`
}

// ModuleAsSDK carries the per-module SDK-role data: which authored modules
// and clients this SDK manages in the workspace. Serialized under
// [modules.<name>.as-sdk] with array-of-tables sub-blocks.
type ModuleAsSDK struct {
	// Name is the user-facing SDK name used by `dagger module init <sdk>` and
	// `dagger api client init <sdk>`. When empty, the module entry name is used.
	Name string `json:"name,omitempty" toml:"name,omitempty"`

	// Modules lists the workspace-local modules this SDK authors and
	// manages. Each entry becomes a [[modules.<name>.as-sdk.modules]] block.
	Modules []SDKManagedModule `json:"modules,omitempty" toml:"modules,omitempty"`

	// Clients lists generated typed bindings this SDK produces in the
	// workspace. Each entry becomes a [[modules.<name>.as-sdk.clients]]
	// block. Shape is intentionally minimal until concrete client SDKs
	// (TypeScript, Go) take shape.
	Clients []SDKManagedClient `json:"clients,omitempty" toml:"clients,omitempty"`
}

// SDKManagedModule is a workspace-relative path to a module that an SDK
// authors and manages here. The path is the only required field; the
// module's own engine state lives in <path>/dagger-module.toml.
type SDKManagedModule struct {
	Path string `json:"path" toml:"path"`
}

// SDKManagedClient is a workspace-relative path to a generated client
// produced by an SDK, bound to one module. Module accepts a
// workspace-relative path or canonical ref, same resolution as
// [modules.X].source. Shape will evolve as concrete client SDKs implement.
type SDKManagedClient struct {
	Path    string            `json:"path" toml:"path"`
	Module  string            `json:"module" toml:"module"`
	Pin     string            `json:"pin,omitempty" toml:"pin,omitempty"`
	Options map[string]string `json:"options,omitempty" toml:"-"`
}

// ModuleSkip carries the per-action skip patterns for a module entry.
// Patterns may be exact names or globs and apply to the action's leaf nodes
// scoped under the module (e.g. "redis", "infra:database", "other-generators:*").
type ModuleSkip struct {
	Skip []string `json:"skip,omitempty" toml:"skip,omitempty"`
}

// EnvOverlay is a named workspace environment overlay.
// It intentionally supports only a constrained subset of the root schema.
type EnvOverlay struct {
	Modules map[string]EnvModuleOverlay `json:"modules,omitempty" toml:"modules"`
}

// EnvModuleOverlay is the environment-specific overlay for one installed module.
type EnvModuleOverlay struct {
	Settings map[string]any `json:"settings,omitempty" toml:"settings,omitempty"`
}

// ResolveModuleEntrySource converts a workspace-config module source into the
// path that should actually be loaded or displayed from the workspace root.
// Relative local sources are resolved from the config directory; absolute local
// sources are preserved as-is.
func ResolveModuleEntrySource(configDir, source string) string {
	if source == "" || !IsLocalRef(source, "") {
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

// ParseConfig parses dagger.toml bytes into a workspace config.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse dagger.toml: %w", err)
	}
	if err := populateClientOptions(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func populateClientOptions(data []byte, cfg *Config) error {
	if len(data) == 0 || cfg == nil || len(cfg.Modules) == 0 {
		return nil
	}
	tree, err := toml.LoadBytes(data)
	if err != nil {
		return fmt.Errorf("parse dagger.toml client options: %w", err)
	}

	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK == nil || len(entry.AsSDK.Clients) == 0 {
			continue
		}
		rawClients := tree.GetPath([]string{"modules", moduleName, "as-sdk", "clients"})
		clientTrees, ok := rawClients.([]*toml.Tree)
		if !ok {
			continue
		}
		for i := range entry.AsSDK.Clients {
			if i >= len(clientTrees) {
				break
			}
			options := clientOptionsFromTree(clientTrees[i])
			if len(options) > 0 {
				entry.AsSDK.Clients[i].Options = options
			}
		}
		cfg.Modules[moduleName] = entry
	}
	return nil
}

func clientOptionsFromTree(tree *toml.Tree) map[string]string {
	if tree == nil {
		return nil
	}
	options := map[string]string{}
	for _, key := range tree.Keys() {
		switch key {
		case "path", "module", "pin":
			continue
		}
		value, ok := tree.GetPath([]string{key}).(string)
		if !ok {
			continue
		}
		options[key] = value
	}
	if len(options) == 0 {
		return nil
	}
	return options
}

// ApplyEnvOverlay returns a copy of cfg with the named environment overlay
// applied on top of the base module settings.
//
// In v1, environments may only override [modules.<name>.settings] values.
func ApplyEnvOverlay(cfg *Config, envName string) (*Config, error) {
	if cfg == nil {
		if envName == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("workspace env %q requires dagger.toml", envName)
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
		if entry.Settings == nil {
			entry.Settings = map[string]any{}
		}
		for key, value := range overlay.Settings {
			entry.Settings[key] = value
		}
		applied.Modules[moduleName] = entry
	}

	return applied, nil
}

// EnvNames returns the configured environment names in deterministic order.
func EnvNames(cfg *Config) []string {
	if cfg == nil || len(cfg.Env) == 0 {
		return nil
	}

	names := make([]string, 0, len(cfg.Env))
	for name := range cfg.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// EnsureEnv makes sure the named environment exists.
// It returns true when the config was changed.
func EnsureEnv(cfg *Config, envName string) bool {
	if cfg.Env == nil {
		cfg.Env = map[string]EnvOverlay{}
	}
	if _, ok := cfg.Env[envName]; ok {
		return false
	}
	cfg.Env[envName] = EnvOverlay{}
	return true
}

// RemoveEnv removes the named environment from the config.
func RemoveEnv(cfg *Config, envName string) error {
	if cfg == nil || len(cfg.Env) == 0 {
		return fmt.Errorf("workspace env %q is not defined", envName)
	}
	if _, ok := cfg.Env[envName]; !ok {
		return fmt.Errorf("workspace env %q is not defined", envName)
	}
	delete(cfg.Env, envName)
	if len(cfg.Env) == 0 {
		cfg.Env = nil
	}
	return nil
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

	if cfg.CheckGenerated != nil {
		fmt.Fprintf(&b, "check-generated = %t\n\n", *cfg.CheckGenerated)
	}

	wroteModules := writeModuleEntries(&b, cfg.Modules)
	if wroteModules && (len(cfg.Env) > 0 || len(cfg.Ports) > 0) {
		b.WriteString("\n")
	}
	if writeEnvEntries(&b, cfg.Env) && len(cfg.Ports) > 0 {
		b.WriteString("\n")
	}
	writePortEntries(&b, cfg.Ports)

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
	if cfg.CheckGenerated != nil {
		checkGenerated := *cfg.CheckGenerated
		cloned.CheckGenerated = &checkGenerated
	}
	if len(cfg.Modules) > 0 {
		cloned.Modules = make(map[string]ModuleEntry, len(cfg.Modules))
		for name, entry := range cfg.Modules {
			cloned.Modules[name] = ModuleEntry{
				Source:            entry.Source,
				Pin:               entry.Pin,
				Settings:          cloneConfigMap(entry.Settings),
				Entrypoint:        entry.Entrypoint,
				LegacyDefaultPath: entry.LegacyDefaultPath,
				Up:                ModuleSkip{Skip: append([]string(nil), entry.Up.Skip...)},
				Generate:          ModuleSkip{Skip: append([]string(nil), entry.Generate.Skip...)},
				Check:             ModuleSkip{Skip: append([]string(nil), entry.Check.Skip...)},
				AsSDK:             cloneModuleAsSDK(entry.AsSDK),
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
						Settings: cloneConfigMap(overlay.Settings),
					}
				}
			}
			cloned.Env[envName] = clonedEnv
		}
	}
	if len(cfg.Ports) > 0 {
		cloned.Ports = make(map[string]PortMapping, len(cfg.Ports))
		for host, pm := range cfg.Ports {
			cloned.Ports[host] = pm
		}
	}
	return cloned
}

// cloneModuleAsSDK deep-copies the AsSDK sub-table. Preserves an empty
// (non-nil with no Modules and no Clients) AsSDK — its mere presence is the
// "this install IS an SDK" marker, even before any module or client is
// authored under it. A nil input means "this install is not an SDK"; the
// clone passes that through unchanged.
func cloneModuleAsSDK(in *ModuleAsSDK) *ModuleAsSDK {
	if in == nil {
		return nil
	}
	return &ModuleAsSDK{
		Name:    in.Name,
		Modules: append([]SDKManagedModule(nil), in.Modules...),
		Clients: cloneSDKManagedClients(in.Clients),
	}
}

func cloneSDKManagedClients(in []SDKManagedClient) []SDKManagedClient {
	if len(in) == 0 {
		return nil
	}
	out := make([]SDKManagedClient, len(in))
	for i, client := range in {
		out[i] = client
		if len(client.Options) > 0 {
			out[i].Options = make(map[string]string, len(client.Options))
			for key, value := range client.Options {
				out[i].Options[key] = value
			}
		}
	}
	return out
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
		modulePath := "modules." + formatConfigPathSegment(name)
		fmt.Fprintf(b, "[%s]\n", modulePath)
		fmt.Fprintf(b, "source = %q\n", entry.Source)
		if entry.Pin != "" {
			fmt.Fprintf(b, "pin = %q\n", entry.Pin)
		}
		if entry.Entrypoint {
			b.WriteString("entrypoint = true\n")
		}
		if entry.LegacyDefaultPath {
			b.WriteString("legacy-default-path = true\n")
		}
		if len(entry.Up.Skip) > 0 {
			fmt.Fprintf(b, "up.skip = %s\n", formatConfigValue(entry.Up.Skip))
		}
		if len(entry.Generate.Skip) > 0 {
			fmt.Fprintf(b, "generate.skip = %s\n", formatConfigValue(entry.Generate.Skip))
		}
		if len(entry.Check.Skip) > 0 {
			fmt.Fprintf(b, "check.skip = %s\n", formatConfigValue(entry.Check.Skip))
		}
		writeConfigTable(b, modulePath+".settings", entry.Settings, true)
		writeModuleAsSDK(b, modulePath, entry.AsSDK)
	}

	return true
}

// writeModuleAsSDK renders the [[modules.<name>.as-sdk.modules]] and
// [[modules.<name>.as-sdk.clients]] array-of-tables blocks under a module's
// section. No-op when the module isn't installed as an SDK.
func writeModuleAsSDK(b *strings.Builder, modulePath string, asSDK *ModuleAsSDK) {
	if asSDK == nil {
		return
	}
	// Emit the parent section when it carries scalar metadata or when the
	// otherwise-empty section is the "this install IS an SDK" marker.
	if asSDK.Name != "" || len(asSDK.Modules) == 0 && len(asSDK.Clients) == 0 {
		b.WriteString("\n")
		fmt.Fprintf(b, "[%s.as-sdk]\n", modulePath)
		if asSDK.Name != "" {
			fmt.Fprintf(b, "name = %q\n", asSDK.Name)
		}
	}
	for _, mod := range asSDK.Modules {
		b.WriteString("\n")
		fmt.Fprintf(b, "[[%s.as-sdk.modules]]\n", modulePath)
		fmt.Fprintf(b, "path = %q\n", mod.Path)
	}
	for _, client := range asSDK.Clients {
		b.WriteString("\n")
		fmt.Fprintf(b, "[[%s.as-sdk.clients]]\n", modulePath)
		fmt.Fprintf(b, "path = %q\n", client.Path)
		fmt.Fprintf(b, "module = %q\n", client.Module)
		if client.Pin != "" {
			fmt.Fprintf(b, "pin = %q\n", client.Pin)
		}
		keys := make([]string, 0, len(client.Options))
		for key := range client.Options {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(b, "%s = %s\n", formatConfigPathSegment(key), formatConfigValue(client.Options[key]))
		}
	}
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
			fmt.Fprintf(b, "[env.%s]\n", formatConfigPathSegment(name))
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
			tablePath := strings.Join([]string{
				"env",
				formatConfigPathSegment(name),
				"modules",
				formatConfigPathSegment(moduleName),
				"settings",
			}, ".")
			writeConfigTable(b, tablePath, env.Modules[moduleName].Settings, false)
		}
	}

	return true
}

func writePortEntries(b *strings.Builder, ports map[string]PortMapping) bool {
	if len(ports) == 0 {
		return false
	}

	hosts := make([]string, 0, len(ports))
	for host := range ports {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)

	for i, host := range hosts {
		if i > 0 {
			b.WriteString("\n")
		}
		pm := ports[host]
		fmt.Fprintf(b, "[ports.%s]\n", formatConfigPathSegment(host))
		fmt.Fprintf(b, "backendService = %q\n", pm.BackendService)
		fmt.Fprintf(b, "backendPort = %d\n", pm.BackendPort)
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
		fmt.Fprintf(b, "%s = %s\n", formatConfigPathSegment(key), formatConfigValue(config[key]))
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

	parts, err := splitConfigPath(key)
	if err != nil {
		return "", err
	}
	value := tree.GetPath(parts)
	if value == nil {
		if defaultValue, ok := readMissingConfigDefault(tree, parts); ok {
			return defaultValue, nil
		}
		return "", fmt.Errorf("key %q is not set", key)
	}

	switch v := value.(type) {
	case *toml.Tree:
		return flattenTOMLTree("", v), nil
	default:
		return formatScalarOutput(v), nil
	}
}

func readMissingConfigDefault(tree *toml.Tree, parts []string) (string, bool) {
	if len(parts) == 1 && parts[0] == "defaults_from_dotenv" {
		return "false", true
	}
	if len(parts) == 1 && parts[0] == "check-generated" {
		return "true", true
	}
	if len(parts) == 3 && parts[0] == "modules" && (parts[2] == "entrypoint" || parts[2] == "legacy-default-path") {
		if tree.GetPath(parts[:2]) != nil {
			return "false", true
		}
	}
	return "", false
}

// WriteConfigValue writes a typed value to config TOML at the given dotted key.
func WriteConfigValue(existingData []byte, key string, rawValue string) ([]byte, error) {
	if key == "" {
		return nil, fmt.Errorf("key is required for writing")
	}
	parts, err := splitConfigPath(key)
	if err != nil {
		return nil, err
	}
	if err := validateConfigKeyParts(parts, key); err != nil {
		return nil, err
	}

	cfg, err := ParseConfig(existingData)
	if err != nil && len(existingData) > 0 {
		return nil, fmt.Errorf("parse existing config: %w", err)
	}
	if cfg == nil {
		cfg = &Config{}
	}

	value := parseValueString(parts, rawValue)
	if err := setConfigValue(cfg, parts, value); err != nil {
		return nil, err
	}

	return UpdateConfigBytes(existingData, cfg)
}

func flattenTOMLTree(prefix string, tree *toml.Tree) string {
	var lines []string
	for _, key := range tree.Keys() {
		fullKey := formatConfigPathSegment(key)
		if prefix != "" {
			fullKey = prefix + "." + fullKey
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

// SplitConfigPath parses a TOML dotted key path into its logical path segments.
func SplitConfigPath(key string) ([]string, error) {
	return splitConfigPath(key)
}

func validateConfigKeyParts(parts []string, key string) error {
	if len(parts) == 0 {
		return fmt.Errorf("key is required")
	}
	return validateKeyAgainstType(parts, reflect.TypeOf(Config{}), key)
}

// JoinConfigPath formats logical path segments as a TOML dotted key path.
func JoinConfigPath(parts ...string) string {
	formatted := make([]string, len(parts))
	for i, part := range parts {
		formatted[i] = FormatConfigPathSegment(part)
	}
	return strings.Join(formatted, ".")
}

func splitConfigPath(key string) ([]string, error) {
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}

	parts := []string{}
	for i := 0; i < len(key); {
		if key[i] == '.' {
			return nil, fmt.Errorf("invalid key %q: empty path segment", key)
		}

		part, next, err := parseConfigPathSegment(key, i)
		if err != nil {
			return nil, err
		}
		if part == "" {
			return nil, fmt.Errorf("invalid key %q: empty path segment", key)
		}
		parts = append(parts, part)
		i = next

		if i == len(key) {
			break
		}
		if key[i] != '.' {
			return nil, fmt.Errorf("invalid key %q: expected dot separator", key)
		}
		i++
		if i == len(key) {
			return nil, fmt.Errorf("invalid key %q: empty path segment", key)
		}
	}

	return parts, nil
}

func parseConfigPathSegment(key string, start int) (string, int, error) {
	switch key[start] {
	case '"':
		return parseBasicConfigPathSegment(key, start+1)
	case '\'':
		return parseLiteralConfigPathSegment(key, start+1)
	default:
		return parseBareConfigPathSegment(key, start)
	}
}

func parseBasicConfigPathSegment(key string, start int) (string, int, error) {
	var b strings.Builder
	for i := start; i < len(key); {
		ch := key[i]
		i++
		switch ch {
		case '\\':
			r, next, err := parseConfigPathEscape(key, i)
			if err != nil {
				return "", 0, err
			}
			b.WriteRune(r)
			i = next
		case '"':
			return b.String(), i, nil
		default:
			if ch < 0x20 || ch == 0x7f {
				return "", 0, fmt.Errorf("invalid key %q: unescaped control character in quoted path segment", key)
			}
			b.WriteByte(ch)
		}
	}
	return "", 0, fmt.Errorf("invalid key %q: unterminated quoted path segment", key)
}

func parseConfigPathEscape(key string, start int) (rune, int, error) {
	if start >= len(key) {
		return 0, 0, fmt.Errorf("invalid key %q: trailing escape in quoted path segment", key)
	}

	escaped := key[start]
	next := start + 1
	switch escaped {
	case 'b':
		return '\b', next, nil
	case 't':
		return '\t', next, nil
	case 'n':
		return '\n', next, nil
	case 'f':
		return '\f', next, nil
	case 'r':
		return '\r', next, nil
	case '"':
		return '"', next, nil
	case '\\':
		return '\\', next, nil
	case 'u', 'U':
		digits := 4
		if escaped == 'U' {
			digits = 8
		}
		return parseConfigPathUnicodeEscape(key, next, digits)
	default:
		return 0, 0, fmt.Errorf("invalid key %q: invalid escape in quoted path segment", key)
	}
}

func parseConfigPathUnicodeEscape(key string, start, digits int) (rune, int, error) {
	if start+digits > len(key) {
		return 0, 0, fmt.Errorf("invalid key %q: incomplete unicode escape in quoted path segment", key)
	}
	v, err := strconv.ParseInt(key[start:start+digits], 16, 32)
	if err != nil || v > utf8.MaxRune || v >= 0xD800 && v <= 0xDFFF {
		return 0, 0, fmt.Errorf("invalid key %q: invalid unicode escape in quoted path segment", key)
	}
	return rune(v), start + digits, nil
}

func parseLiteralConfigPathSegment(key string, start int) (string, int, error) {
	i := start
	for i < len(key) && key[i] != '\'' {
		i++
	}
	if i >= len(key) {
		return "", 0, fmt.Errorf("invalid key %q: unterminated quoted path segment", key)
	}
	return key[start:i], i + 1, nil
}

func parseBareConfigPathSegment(key string, start int) (string, int, error) {
	i := start
	for i < len(key) && key[i] != '.' {
		i++
	}
	part := key[start:i]
	if !isBareConfigPathSegment(part) {
		return "", 0, fmt.Errorf("invalid key %q: path segment %q must be quoted", key, part)
	}
	return part, i, nil
}

func setConfigValue(cfg *Config, parts []string, value any) error { //nolint:gocyclo
	if len(parts) == 0 {
		return fmt.Errorf("key is required")
	}

	switch parts[0] {
	case "ignore":
		if len(parts) != 1 {
			return fmt.Errorf("invalid key %q; ignore does not have sub-keys", strings.Join(parts, "."))
		}
		switch v := value.(type) {
		case []string:
			cfg.Ignore = append([]string(nil), v...)
		case []any:
			cfg.Ignore = make([]string, 0, len(v))
			for _, item := range v {
				cfg.Ignore = append(cfg.Ignore, fmt.Sprint(item))
			}
		default:
			cfg.Ignore = []string{fmt.Sprint(v)}
		}
		return nil
	case "defaults_from_dotenv":
		if len(parts) != 1 {
			return fmt.Errorf("invalid key %q; defaults_from_dotenv does not have sub-keys", strings.Join(parts, "."))
		}
		boolValue, ok := value.(bool)
		if !ok {
			return fmt.Errorf("defaults_from_dotenv must be a boolean")
		}
		cfg.DefaultsFromDotEnv = boolValue
		return nil
	case "check-generated":
		if len(parts) != 1 {
			return fmt.Errorf("invalid key %q; check-generated does not have sub-keys", strings.Join(parts, "."))
		}
		boolValue, ok := value.(bool)
		if !ok {
			return fmt.Errorf("check-generated must be a boolean")
		}
		cfg.CheckGenerated = &boolValue
		return nil
	case "modules":
		if len(parts) < 3 {
			return fmt.Errorf("cannot set %q directly; specify a field like %s.settings", strings.Join(parts, "."), strings.Join(parts, "."))
		}
		if cfg.Modules == nil {
			cfg.Modules = map[string]ModuleEntry{}
		}
		moduleName := parts[1]
		entry := cfg.Modules[moduleName]
		switch parts[2] {
		case "source":
			entry.Source = fmt.Sprint(value)
		case "entrypoint":
			boolValue, ok := value.(bool)
			if !ok {
				return fmt.Errorf("modules.%s.entrypoint must be a boolean", moduleName)
			}
			entry.Entrypoint = boolValue
		case "legacy-default-path":
			boolValue, ok := value.(bool)
			if !ok {
				return fmt.Errorf("modules.%s.legacy-default-path must be a boolean", moduleName)
			}
			entry.LegacyDefaultPath = boolValue
		case "settings":
			if len(parts) < 4 {
				return fmt.Errorf("cannot set modules.%s.settings directly; specify a setting key", moduleName)
			}
			if entry.Settings == nil {
				entry.Settings = map[string]any{}
			}
			entry.Settings[parts[3]] = value
		case "up", "generate", "check":
			if len(parts) != 4 || parts[3] != "skip" {
				return fmt.Errorf("invalid key %q; expected modules.%s.%s.skip", strings.Join(parts, "."), moduleName, parts[2])
			}
			skip := []string{fmt.Sprint(value)}
			if s, ok := value.([]string); ok {
				skip = append([]string(nil), s...)
			}
			switch parts[2] {
			case "up":
				entry.Up.Skip = skip
			case "generate":
				entry.Generate.Skip = skip
			case "check":
				entry.Check.Skip = skip
			}
		case "as-sdk":
			if len(parts) != 4 || parts[3] != "name" {
				return fmt.Errorf("invalid key %q; expected modules.%s.as-sdk.name", strings.Join(parts, "."), moduleName)
			}
			if entry.AsSDK == nil {
				entry.AsSDK = &ModuleAsSDK{}
			}
			entry.AsSDK.Name = fmt.Sprint(value)
		default:
			return fmt.Errorf("unknown config key %q", strings.Join(parts, "."))
		}
		cfg.Modules[moduleName] = entry
		return nil
	case "env":
		if len(parts) < 5 || parts[2] != "modules" || parts[4] != "settings" || len(parts) < 6 {
			return fmt.Errorf("unknown config key %q", strings.Join(parts, "."))
		}
		if cfg.Env == nil {
			cfg.Env = map[string]EnvOverlay{}
		}
		envName := parts[1]
		env := cfg.Env[envName]
		if env.Modules == nil {
			env.Modules = map[string]EnvModuleOverlay{}
		}
		moduleName := parts[3]
		module := env.Modules[moduleName]
		if module.Settings == nil {
			module.Settings = map[string]any{}
		}
		module.Settings[parts[5]] = value
		env.Modules[moduleName] = module
		cfg.Env[envName] = env
		return nil
	case "ports":
		if len(parts) != 3 {
			return fmt.Errorf("invalid key %q; expected ports.<host>.backendService or ports.<host>.backendPort", strings.Join(parts, "."))
		}
		if cfg.Ports == nil {
			cfg.Ports = map[string]PortMapping{}
		}
		host := parts[1]
		pm := cfg.Ports[host]
		switch parts[2] {
		case "backendService":
			pm.BackendService = fmt.Sprint(value)
		case "backendPort":
			port, ok := value.(int64)
			if !ok {
				return fmt.Errorf("ports.%s.backendPort must be an integer", host)
			}
			pm.BackendPort = int(port)
		default:
			return fmt.Errorf("unknown config key %q", strings.Join(parts, "."))
		}
		cfg.Ports[host] = pm
		return nil
	default:
		return fmt.Errorf("unknown config key %q", strings.Join(parts, "."))
	}
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
	for fieldType.Kind() == reflect.Ptr {
		fieldType = fieldType.Elem()
	}

	switch fieldType.Kind() {
	case reflect.Map:
		if len(rest) == 0 {
			return fmt.Errorf("cannot set %q directly; specify a sub-key", fullKey)
		}

		mapValueRest := rest[1:]
		elemType := fieldType.Elem()
		if elemType.Kind() == reflect.Struct {
			if len(mapValueRest) == 0 {
				return fmt.Errorf("cannot set %q directly; specify a field like %s.%s",
					fullKey, fullKey, preferredExampleFieldName(elemType))
			}
			return validateKeyAgainstType(mapValueRest, elemType, fullKey)
		}
		if len(mapValueRest) > 0 {
			return fmt.Errorf("invalid key %q; config keys cannot be nested deeper", fullKey)
		}
		return nil
	case reflect.Struct:
		if len(rest) == 0 {
			return fmt.Errorf("cannot set %q directly; specify a field like %s.%s",
				fullKey, fullKey, preferredExampleFieldName(fieldType))
		}
		return validateKeyAgainstType(rest, fieldType, fullKey)
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

func preferredExampleFieldName(t reflect.Type) string {
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("toml")
		name := strings.Split(tag, ",")[0]
		if name == "settings" {
			return name
		}
	}

	names := validTOMLFieldNames(t)
	if len(names) == 0 {
		return "value"
	}
	return names[0]
}

func parseValueString(parts []string, rawValue string) any {
	leaf := parts[len(parts)-1]

	if leaf == "entrypoint" || leaf == "legacy-default-path" ||
		(len(parts) == 1 && (parts[0] == "defaults_from_dotenv" || parts[0] == "check-generated")) {
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
