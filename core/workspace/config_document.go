package workspace

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// UpdateConfigBytes applies the differences between the old and new typed
// config to the original document. Fields absent from both typed configs are
// left alone, including unknown fields and explicitly written defaults.
func UpdateConfigBytes(existingData []byte, cfg *Config) ([]byte, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if err := ValidateSDKs(cfg); err != nil {
		return nil, err
	}
	if len(existingData) == 0 {
		return SerializeConfig(cfg), nil
	}
	existing, err := ParseConfig(existingData)
	if err != nil {
		return nil, err
	}
	updated, err := updateConfigTable(existingData, nil, configDocumentMap(existing), configDocumentMap(cfg))
	if err != nil {
		return nil, err
	}
	if _, err := ParseConfig(updated); err != nil {
		return nil, fmt.Errorf("validate edited config: %w", err)
	}
	return updated, nil
}

func updateConfigTable(data []byte, prefix []string, before, after map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(before)+len(after))
	for key := range before {
		keys = append(keys, key)
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		old, had := before[key]
		value, has := after[key]
		if had == has && configValuesEqual(old, value) {
			continue
		}
		parts := append(slices.Clone(prefix), key)
		var err error
		oldMap, oldTable := old.(map[string]any)
		newMap, newTable := value.(map[string]any)
		if oldTable && !has && !configEntryPath(parts) {
			// Removing the last known field does not remove unknown siblings
			// in the same table. Whole-entry removal is explicit below.
			data, err = updateConfigTable(data, parts, oldMap, nil)
		} else if newTable && (!had || oldTable) {
			data, err = updateConfigTable(data, parts, oldMap, newMap)
			if err == nil && len(newMap) == 0 {
				var doc *configText
				doc, err = parseConfigText(data)
				if err == nil {
					data, err = doc.ensureTable(parts)
				}
			}
		} else {
			var doc *configText
			doc, err = parseConfigText(data)
			if err == nil {
				if !has {
					data, err = doc.remove(parts)
				} else {
					data, err = doc.set(parts, value)
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("edit config %q: %w", JoinConfigPath(parts...), err)
		}
	}
	return data, nil
}

func configEntryPath(parts []string) bool {
	if len(parts) == 2 {
		return parts[0] == "modules" || parts[0] == "sdks" || parts[0] == "env" || parts[0] == "ports"
	}
	return len(parts) == 4 && (parts[0] == "sdks" && parts[2] == "scopes" || parts[0] == "env" && parts[2] == "modules")
}

func deleteConfigDocumentPath(data []byte, parts, collapsed []string) ([]byte, error) {
	doc, err := parseConfigText(data)
	if err != nil {
		return nil, err
	}
	data, err = doc.remove(collapsed)
	if err != nil {
		return nil, err
	}
	if parts[0] == "env" {
		doc, err = parseConfigText(data)
		if err != nil {
			return nil, err
		}
		data, err = doc.ensureTable(parts[:2])
		if err != nil {
			return nil, err
		}
	}
	if _, err := ParseConfig(data); err != nil {
		return nil, fmt.Errorf("validate edited config: %w", err)
	}
	return data, nil
}

func configDocumentMap(cfg *Config) map[string]any {
	values := make(map[string]any)

	if len(cfg.Ignore) > 0 {
		values["ignore"] = append([]string(nil), cfg.Ignore...)
	}
	if cfg.DefaultsFromDotEnv {
		values["defaults_from_dotenv"] = true
	}
	if cfg.CheckGenerated != nil {
		values["check-generated"] = *cfg.CheckGenerated
	}
	if len(cfg.Modules) > 0 {
		modules := make(map[string]any, len(cfg.Modules))
		for name, entry := range cfg.Modules {
			module := map[string]any{
				"source": entry.Source,
			}
			if entry.Pin != "" {
				module["pin"] = entry.Pin
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
			if len(entry.Up.Skip) > 0 {
				module["up"] = map[string]any{"skip": append([]string(nil), entry.Up.Skip...)}
			}
			if len(entry.Generate.Skip) > 0 {
				module["generate"] = map[string]any{"skip": append([]string(nil), entry.Generate.Skip...)}
			}
			if len(entry.Check.Skip) > 0 {
				module["check"] = map[string]any{"skip": append([]string(nil), entry.Check.Skip...)}
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
					if overlay.Source != "" {
						module["source"] = overlay.Source
					}
					if overlay.Pin != "" {
						module["pin"] = overlay.Pin
					}
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
	if len(cfg.Ports) > 0 {
		ports := make(map[string]any, len(cfg.Ports))
		for host, pm := range cfg.Ports {
			ports[host] = map[string]any{
				"backendService": pm.BackendService,
				"backendPort":    int64(pm.BackendPort),
			}
		}
		values["ports"] = ports
	}
	if len(cfg.SDKs) > 0 {
		sdks := make(map[string]any, len(cfg.SDKs))
		for name, entry := range cfg.SDKs {
			sdk := map[string]any{"module": entry.Module}
			if len(entry.Scopes) > 0 {
				scopes := make(map[string]any, len(entry.Scopes))
				for name, entry := range entry.Scopes {
					scope := map[string]any{}
					if entry.IsModule {
						scope["is-module"] = true
					}
					if entry.Name != "" {
						scope["name"] = entry.Name
					}
					if len(entry.Clients) > 0 {
						scope["clients"] = slices.Clone(entry.Clients)
					}
					if len(entry.Settings) > 0 {
						scope["settings"] = cloneConfigMap(entry.Settings)
					}
					scopes[name] = scope
				}
				sdk["scopes"] = scopes
			}
			sdks[name] = sdk
		}
		values["sdks"] = sdks
	}
	return values
}

// FormatConfigPathSegment formats one TOML dotted-key path segment.
func FormatConfigPathSegment(segment string) string {
	if isBareConfigPathSegment(segment) {
		return segment
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range segment {
		switch r {
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func formatConfigPathSegment(segment string) string {
	return FormatConfigPathSegment(segment)
}

func isBareConfigPathSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for i := 0; i < len(segment); i++ {
		if !isBareConfigPathChar(segment[i]) {
			return false
		}
	}
	return true
}

func isBareConfigPathChar(c byte) bool {
	return 'A' <= c && c <= 'Z' ||
		'a' <= c && c <= 'z' ||
		'0' <= c && c <= '9' ||
		c == '_' ||
		c == '-'
}
