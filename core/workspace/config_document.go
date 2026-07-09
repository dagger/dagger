package workspace

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/creachadair/tomledit"
	neontoml "github.com/neongreen/mono/lib/toml"
)

// ConstructorArgHint captures a constructor-backed setting hint for a module.
type ConstructorArgHint struct {
	Name         string
	TypeLabel    string
	Description  string
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
	if configRequiresQuotedPathSegments(cfg) {
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
	if configRequiresQuotedPathSegments(existingCfg) {
		out := SerializeConfig(cfg)
		if len(hints) == 0 {
			return out, nil
		}
		return insertWorkspaceSettingHintComments(out, cfg, hints), nil
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

	out, err := pruneUnwantedEmptySections(doc.Bytes(), keepEmptyConfigSectionHeaders(cfg))
	if err != nil {
		return nil, err
	}
	out, err = rewriteModuleAsSDKSections(out, cfg.Modules)
	if err != nil {
		return nil, err
	}
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
	if cfg.CheckGenerated != nil {
		values["check-generated"] = *cfg.CheckGenerated
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
	// Per-module as-sdk sub-blocks are intentionally NOT included here.
	// The neontoml ApplyMap path can't express array-of-tables (it would
	// emit inline arrays of inline tables and leave any pre-existing
	// [[modules.X.as-sdk.modules]] blocks orphaned). Those sub-blocks are
	// managed surgically by rewriteModuleAsSDKSections after the rest of
	// the document is format-preserved.

	return values
}

func configRequiresQuotedPathSegments(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	for moduleName, module := range cfg.Modules {
		if pathSegmentUnsafeForDocumentUpdate(moduleName) || configMapRequiresQuotedPathSegments(module.Settings) {
			return true
		}
	}
	for envName, env := range cfg.Env {
		if pathSegmentUnsafeForDocumentUpdate(envName) {
			return true
		}
		for moduleName, module := range env.Modules {
			if pathSegmentUnsafeForDocumentUpdate(moduleName) || configMapRequiresQuotedPathSegments(module.Settings) {
				return true
			}
		}
	}
	for host := range cfg.Ports {
		if pathSegmentUnsafeForDocumentUpdate(host) {
			return true
		}
	}
	return false
}

func configMapRequiresQuotedPathSegments(values map[string]any) bool {
	for key := range values {
		if pathSegmentUnsafeForDocumentUpdate(key) {
			return true
		}
	}
	return false
}

// pathSegmentUnsafeForDocumentUpdate reports whether segment cannot be used as a
// dotted-path segment when updating an existing config document in place (via
// neontoml's doc.Set/Delete/ApplyMap). Beyond non-bare characters, an all-digit
// segment — e.g. a port host like "3000" — is unsafe because the dotted-path
// parser reads it as a number ("invalid float"). Configs containing such a
// segment fall back to a full re-serialization, which still writes the segment
// unquoted as a TOML section header (e.g. [ports.3000]).
func pathSegmentUnsafeForDocumentUpdate(segment string) bool {
	return !isBareConfigPathSegment(segment) || isAllDigitConfigPathSegment(segment)
}

func isAllDigitConfigPathSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for i := 0; i < len(segment); i++ {
		if segment[i] < '0' || segment[i] > '9' {
			return false
		}
	}
	return true
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

	for host := range existingCfg.Ports {
		if _, ok := desiredCfg.Ports[host]; ok {
			continue
		}
		if err := doc.Delete("ports." + formatConfigPathSegment(host)); err != nil {
			return fmt.Errorf("delete port %q: %w", host, err)
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

func keepEmptyConfigSectionHeaders(cfg *Config) map[string]bool {
	keep := map[string]bool{}
	for envName, env := range cfg.Env {
		if len(env.Modules) > 0 {
			continue
		}
		keep["[env."+formatConfigPathSegment(envName)+"]"] = true
	}
	return keep
}

// rewriteModuleAsSDKSections drops every existing [[modules.*.as-sdk.*]]
// array-of-tables block from data and appends a canonical rendering of
// each module's AsSDK entries. Format preservation inside an as-sdk
// sub-block is intentionally given up: the block is treated as
// CLI-managed state, not human-curated configuration. Everything outside
// the as-sdk sub-blocks (other module fields, settings tables, comments)
// passes through unchanged.
func rewriteModuleAsSDKSections(data []byte, modules map[string]ModuleEntry) ([]byte, error) {
	doc, err := tomledit.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse config for as-sdk rewrite: %w", err)
	}

	kept := doc.Sections[:0]
	for _, section := range doc.Sections {
		if section.Heading != nil && isModuleAsSDKHeading(section.Heading.Name) {
			continue
		}
		kept = append(kept, section)
	}
	doc.Sections = kept

	names := make([]string, 0, len(modules))
	for name, entry := range modules {
		// Preserve the marker even when empty: presence of AsSDK = "this
		// install is an SDK," whether or not anything is authored yet.
		if entry.AsSDK != nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var rendered strings.Builder
		writeModuleAsSDK(&rendered, "modules."+formatConfigPathSegment(name), modules[name].AsSDK)
		if rendered.Len() == 0 {
			continue
		}
		asSDKDoc, parseErr := tomledit.Parse(strings.NewReader(rendered.String()))
		if parseErr != nil {
			return nil, fmt.Errorf("parse rendered as-sdk for %q: %w", name, parseErr)
		}
		doc.Sections = append(doc.Sections, asSDKDoc.Sections...)
	}

	var buf bytes.Buffer
	var formatter tomledit.Formatter
	if err := formatter.Format(&buf, doc); err != nil {
		return nil, fmt.Errorf("format config after as-sdk rewrite: %w", err)
	}
	return buf.Bytes(), nil
}

// isModuleAsSDKHeading reports whether a section heading targets a module's
// as-sdk sub-block (e.g. [modules.go-sdk.as-sdk], [[modules.go-sdk.as-sdk.modules]]).
func isModuleAsSDKHeading(key []string) bool {
	if len(key) < 3 || key[0] != "modules" {
		return false
	}
	for i := 2; i < len(key); i++ {
		if key[i] == "as-sdk" {
			return true
		}
	}
	return false
}

func pruneUnwantedEmptySections(data []byte, keepEmptySections map[string]bool) ([]byte, error) {
	doc, err := tomledit.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse rewritten config document: %w", err)
	}

	sections := doc.Sections[:0]
	for _, section := range doc.Sections {
		if len(section.Items) == 0 && !keepEmptySections[section.Heading.String()] {
			continue
		}
		sections = append(sections, section)
	}
	doc.Sections = sections

	var buf bytes.Buffer
	var formatter tomledit.Formatter
	if err := formatter.Format(&buf, doc); err != nil {
		return nil, fmt.Errorf("format pruned config document: %w", err)
	}
	return buf.Bytes(), nil
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

		commentLines := make([]string, 0, len(moduleHints)*2)
		for _, hint := range moduleHints {
			if existingSettings[strings.ToLower(hint.Name)] {
				continue
			}
			for _, desc := range hintDescriptionLines(hint.Description) {
				commentLines = append(commentLines, "# "+desc)
			}
			commentLines = append(commentLines, fmt.Sprintf("# %s%s = %s", hintPrefix, formatConfigPathSegment(hint.Name), hint.ExampleValue))
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

// hintDescriptionLines returns the first paragraph of a setting description,
// one line per entry. Doc comments wrap mid-sentence, so a single line would
// truncate the description.
func hintDescriptionLines(description string) []string {
	var lines []string
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func findModuleHintInsertionPoint(lines []string, moduleName string) (insertAfter int, hintPrefix string) {
	formattedModuleName := formatConfigPathSegment(moduleName)
	settingsSection := "[modules." + formattedModuleName + ".settings]"
	if idx := findSectionInsertionPoint(lines, settingsSection); idx != -1 {
		return idx, ""
	}

	moduleSection := "[modules." + formattedModuleName + "]"
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
