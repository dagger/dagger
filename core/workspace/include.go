package workspace

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml"
)

// MaxIncludes is how many [[include]] entries a config may carry. The shape is
// an array so several can be expressed, but resolving more than one raises
// questions this feature deliberately does not answer yet — ordering between
// includes, and what happens when two of them disagree. One is enough to share
// a base config, so the limit stays until there is a reason to lift it.
const MaxIncludes = 1

// TooManyIncludesError reports a config with more includes than are supported.
type TooManyIncludesError struct {
	Count int
}

func (e *TooManyIncludesError) Error() string {
	return fmt.Sprintf(
		"workspace config declares %d includes; only %d is supported for now",
		e.Count, MaxIncludes,
	)
}

// NestedIncludeError reports an included config that includes something in
// turn. Includes are one level deep: following the chain would turn a basic
// inheritance feature into config composition.
type NestedIncludeError struct {
	// Include is the source the current config includes.
	Include string
	// Nested is the source that included config names in turn.
	Nested string
}

func (e *NestedIncludeError) Error() string {
	return fmt.Sprintf(
		"included config %q includes %q in turn; nested includes are not supported",
		e.Include, e.Nested,
	)
}

// ValidateIncludes checks a config's own include list before anything is
// resolved, so an unsupported shape is reported against the file the user
// wrote rather than as a failure deeper in loading.
func ValidateIncludes(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	if len(cfg.Include) > MaxIncludes {
		return &TooManyIncludesError{Count: len(cfg.Include)}
	}
	for i, include := range cfg.Include {
		// An [[include]] with nothing to resolve would otherwise be a silent
		// no-op: the block is there, the config it promises is not.
		if strings.TrimSpace(include.Source) == "" {
			return fmt.Errorf("workspace include %d has no source", i+1)
		}
	}
	return nil
}

func MergeIncludedConfig(included, current *Config, currentKeys map[string]bool) (*Config, error) {
	if current == nil {
		current = &Config{}
	}
	if included == nil {
		return cloneConfig(current), nil
	}
	if len(included.Include) > 0 {
		return nil, &NestedIncludeError{
			Include: currentIncludeSource(current),
			Nested:  included.Include[0].Source,
		}
	}

	merged := cloneConfig(included)
	merged.Include = append([]IncludeEntry(nil), current.Include...)
	// SDK declarations are never inherited: an SDK's scopes are paths into the
	// tree its config came from, so one declared over there names project roots
	// that do not exist here. The current workspace's own declarations stand
	// alone rather than merging with them.
	merged.SDKs = cloneSDKs(current.SDKs)

	// legacy-default-path is migration state for a specific local module tree,
	// so it is not inherited either.
	for name, entry := range merged.Modules {
		entry.LegacyDefaultPath = false
		merged.Modules[name] = entry
	}

	if currentKeys[keyIgnore] {
		merged.Ignore = append([]string(nil), current.Ignore...)
	}
	if currentKeys[keyDefaultsFromDotEnv] {
		merged.DefaultsFromDotEnv = current.DefaultsFromDotEnv
	}
	if current.CheckGenerated != nil {
		checkGenerated := *current.CheckGenerated
		merged.CheckGenerated = &checkGenerated
	}

	mergeIncludedModules(merged, current, currentKeys)
	mergeIncludedEnvs(merged, current)
	mergeIncludedPorts(merged, current)

	return merged, nil
}

const (
	keyIgnore             = "ignore"
	keyDefaultsFromDotEnv = "defaults_from_dotenv"
)

func mergeIncludedModules(merged, current *Config, currentKeys map[string]bool) {
	if len(current.Modules) == 0 {
		return
	}
	if merged.Modules == nil {
		merged.Modules = map[string]ModuleEntry{}
	}
	for name, overlay := range current.Modules {
		entry, inherited := merged.Modules[name]
		if !inherited {
			merged.Modules[name] = cloneModuleEntry(overlay)
			continue
		}

		// A source replaces the inherited one and its pin travels with it; a
		// lone pin updates the inherited pin in place. Same coupling env
		// overlays already use.
		if overlay.Source != "" {
			entry.Source = overlay.Source
			entry.Pin = overlay.Pin
		} else if overlay.Pin != "" {
			entry.Pin = overlay.Pin
		}
		if len(overlay.Settings) > 0 {
			if entry.Settings == nil {
				entry.Settings = map[string]any{}
			}
			for key, value := range overlay.Settings {
				entry.Settings[key] = value
			}
		}
		prefix := JoinConfigPath("modules", name)
		if currentKeys[prefix+".main"] {
			entry.Main = overlay.Main
		}
		entry.LegacyDefaultPath = overlay.LegacyDefaultPath
		if currentKeys[prefix+".up.skip"] {
			entry.Up = ModuleSkip{Skip: append([]string(nil), overlay.Up.Skip...)}
		}
		if currentKeys[prefix+".generate.skip"] {
			entry.Generate = ModuleSkip{Skip: append([]string(nil), overlay.Generate.Skip...)}
		}
		if currentKeys[prefix+".check.skip"] {
			entry.Check = ModuleSkip{Skip: append([]string(nil), overlay.Check.Skip...)}
		}
		merged.Modules[name] = entry
	}
}

// mergeIncludedEnvs layers the current config's envs over the inherited ones
// with mergeEnvOverlay, the same merge the user-level overlay uses: per env,
// per module, source/pin coupled and settings key by key — exactly the rules
// the merge table states for env.<name>.modules.<mod>.
func mergeIncludedEnvs(merged, current *Config) {
	if len(current.Env) == 0 {
		return
	}
	if merged.Env == nil {
		merged.Env = map[string]EnvOverlay{}
	}
	for envName, currentEnv := range current.Env {
		merged.Env[envName] = mergeEnvOverlay(merged.Env[envName], currentEnv)
	}
}

// mergeIncludedPorts replaces an inherited mapping wholesale per host port. A
// service and its backend port are one unit, and the config serializer writes
// both keys unconditionally, so a per-field merge would read a written-out
// `backendPort = 0` as a deliberate override.
func mergeIncludedPorts(merged, current *Config) {
	if len(current.Ports) == 0 {
		return
	}
	if merged.Ports == nil {
		merged.Ports = map[string]PortMapping{}
	}
	for host, mapping := range current.Ports {
		merged.Ports[host] = mapping
	}
}

// ValidateEffectiveConfig checks the invariant the file format alone no longer
// guarantees, on the config that will actually be used: every module entry
// names a source.
//
// A module entry may omit its source in the file — that is how a workspace
// overrides one setting of an included module without repeating its ref — so
// the completeness check moved here, and runs for every config, whether or not
// it includes one. Gating it on an include being present would mean removing
// the include silently turns a valid override into an entry that names
// nothing.
//
// Port mappings are deliberately not checked, even though they merge wholesale:
// `dagger workspace config ports.3000.backendService web` writes one key while
// the serializers always write both, so a partial mapping is a state the CLI
// itself produces, and validating it here would make configs that load today
// start failing to load.
func ValidateEffectiveConfig(cfg *Config) error {
	if cfg == nil {
		return nil
	}

	var problems []string
	for _, name := range sortedKeys(cfg.Modules) {
		if cfg.Modules[name].Source == "" {
			problems = append(problems, fmt.Sprintf("module %q has no source", name))
		}
	}
	if len(problems) == 0 {
		return nil
	}

	detail := strings.Join(problems, "; ")
	if source := currentIncludeSource(cfg); source != "" {
		return fmt.Errorf("invalid workspace config after merging include %q: %s (the included config, an env overlay or a user-level override must supply one)", source, detail)
	}
	return fmt.Errorf("invalid workspace config: %s (name a source, or supply one through an [[include]], an env overlay or a user-level override)", detail)
}

func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func currentIncludeSource(cfg *Config) string {
	if cfg == nil || len(cfg.Include) == 0 {
		return ""
	}
	return cfg.Include[0].Source
}

// rewriteIncludeBlocks replaces the document's [[include]] blocks with the ones
// in desired. The document editor declines to edit inside an array of tables,
// so the blocks are removed and written back whole. An unchanged list returns
// the document verbatim, which keeps a comment inside the block, and the
// position its author chose, across an unrelated write.
func rewriteIncludeBlocks(data []byte, existing, desired []IncludeEntry) ([]byte, error) {
	if slices.Equal(existing, desired) {
		return data, nil
	}
	tree, err := toml.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	// The decoder also accepts case variants such as [[INCLUDE]]; the block to
	// replace is the one it consumed.
	key, ok := configDecoderKey(tree, "include")
	if !ok {
		key = "include"
	}
	doc, err := parseConfigText(data)
	if err != nil {
		return nil, err
	}
	data, err = doc.remove([]string{key})
	if err != nil {
		return nil, err
	}
	if len(desired) == 0 {
		return data, nil
	}
	doc, err = parseConfigText(data)
	if err != nil {
		return nil, err
	}

	nl := configNewline(data)
	var block strings.Builder
	for _, include := range desired {
		block.WriteString("[[include]]" + nl + "source = " + strconv.Quote(include.Source) + nl + nl)
	}
	// Bare keys stop being legal at the first table header, so the block goes
	// ahead of it.
	for i, stmt := range doc.statements {
		if !stmt.table {
			continue
		}
		floor := 0
		if i > 0 {
			floor = doc.statements[i-1].end
		}
		at := configAttachedCommentStart(data, floor, stmt.start)
		rendered := block.String()
		if at > 0 && !bytes.HasSuffix(data[:at], []byte(nl+nl)) {
			rendered = nl + rendered
		}
		return replaceConfigText(data, at, at, rendered), nil
	}
	prefix := ""
	if len(data) > 0 {
		if data[len(data)-1] != '\n' {
			prefix += nl
		}
		if !bytes.HasSuffix(data, []byte(nl+nl)) {
			prefix += nl
		}
	}
	return append(data, prefix+strings.TrimSuffix(block.String(), nl)...), nil
}
