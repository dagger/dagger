package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// generateConfigTOML builds the .dagger/config.toml content as a string.
// It uses hand-built TOML for precise control over warning comments.
func generateConfigTOML(cfg *LegacyConfig, warnings []warning, aliases []aliasEntry, constructorArgs map[string][]constructorArg) string {
	var b strings.Builder

	// Build warning lookup by toolchain name
	warningsByTC := make(map[string][]warning)
	for _, w := range warnings {
		warningsByTC[w.toolchain] = append(warningsByTC[w.toolchain], w)
	}

	needsBlank := false

	// Project module entry (if there is an SDK)
	if cfg.SDK != nil && cfg.SDK.Source != "" {
		fmt.Fprintf(&b, "[modules.%s]\n", cfg.Name)
		fmt.Fprintf(&b, "source = \"modules/%s\"\n", cfg.Name)
		if len(aliases) > 0 {
			b.WriteString("alias = true\n")
		}
		needsBlank = true
	}

	// Toolchain entries
	for _, tc := range cfg.Toolchains {
		if needsBlank {
			b.WriteString("\n")
		}
		// Add warning comments before the section header
		for _, w := range warningsByTC[tc.Name] {
			b.WriteString(w.tomlComment())
		}
		// Source paths are relative to .dagger/, so prepend ../ to the original path
		fmt.Fprintf(&b, "[modules.%s]\n", tc.Name)
		fmt.Fprintf(&b, "source = \"../%s\"\n", tc.Source)

		// Collect config values from customizations
		var configEntries []string
		for _, cust := range tc.Customizations {
			if cust.IsConstructor() && cust.Default != "" {
				configEntries = append(configEntries, fmt.Sprintf("%s = %q\n", cust.Argument, cust.Default))
			}
		}

		// Collect commented-out constructor arg hints (from introspection)
		var hintLines []string
		if args, ok := constructorArgs[tc.Name]; ok {
			for _, arg := range args {
				if hasCustomization(tc, arg.Name) {
					continue
				}
				if arg.DefaultValue != "" && arg.DefaultValue != "null" {
					hintLines = append(hintLines, fmt.Sprintf("# %s = %s\n", arg.Name, arg.DefaultValue))
				} else {
					hintLines = append(hintLines, fmt.Sprintf("# %s = \"\" # %s\n", arg.Name, arg.TypeName))
				}
			}
		}

		// Write [modules.<name>.config] section if there are config entries or hints
		if len(configEntries) > 0 || len(hintLines) > 0 {
			fmt.Fprintf(&b, "\n[modules.%s.config]\n", tc.Name)
			for _, entry := range configEntries {
				b.WriteString(entry)
			}
			for _, line := range hintLines {
				b.WriteString(line)
			}
		}
		needsBlank = true
	}

	return b.String()
}

// hasCustomization checks if a toolchain already has a customization for the given arg name.
func hasCustomization(tc *LegacyDependency, argName string) bool {
	for _, cust := range tc.Customizations {
		if cust.Argument == argName {
			return true
		}
	}
	return false
}

// warning represents a migration warning for a toolchain customization.
type warning struct {
	toolchain string
	message   string
	original  *LegacyCustomization
}

// tomlComment formats a warning as a TOML comment block.
func (w warning) tomlComment() string {
	var b strings.Builder
	b.WriteString("# WARNING: ")
	b.WriteString(w.message)
	b.WriteString("\n")
	if w.original != nil {
		origJSON, _ := json.Marshal(w.original)
		b.WriteString("# Original: ")
		b.Write(origJSON)
		b.WriteString("\n")
	}
	return b.String()
}

// analyzeCustomizations inspects toolchain customizations and returns
// any config values that can be migrated and warnings for those that can't.
func analyzeCustomizations(toolchains []*LegacyDependency) []warning {
	var warnings []warning
	for _, tc := range toolchains {
		for _, cust := range tc.Customizations {
			if !cust.IsConstructor() {
				// Non-constructor function customization: can't be migrated
				funcName := strings.Join(cust.Function, ".")
				warnings = append(warnings, warning{
					toolchain: tc.Name,
					message: fmt.Sprintf(
						"customization for function %q could not be migrated (non-constructor)",
						funcName,
					),
					original: cust,
				})
				continue
			}
			// Constructor customizations
			if len(cust.Ignore) > 0 || cust.DefaultPath != "" {
				// ignore and defaultPath can't be expressed as config values
				msg := fmt.Sprintf(
					"constructor arg %q has",
					cust.Argument,
				)
				parts := []string{}
				if len(cust.Ignore) > 0 {
					parts = append(parts, "'ignore'")
				}
				if cust.DefaultPath != "" {
					parts = append(parts, "'defaultPath'")
				}
				msg += " " + strings.Join(parts, " and ") + " customization that cannot be expressed as a config value"
				warnings = append(warnings, warning{
					toolchain: tc.Name,
					message:   msg,
					original:  cust,
				})
			}
			// cust.Default is handled by generateConfigTOML (written as config.<arg> = ...)
		}
	}
	return warnings
}
