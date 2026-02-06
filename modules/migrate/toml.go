package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// generateConfigTOML builds the .dagger/config.toml content as a string.
// It uses hand-built TOML for precise control over warning comments.
func generateConfigTOML(cfg *LegacyConfig, warnings []warning) string {
	var b strings.Builder

	// Project module entry (if there is an SDK)
	if cfg.SDK != nil && cfg.SDK.Source != "" {
		fmt.Fprintf(&b, "[modules.%s]\n", cfg.Name)
		fmt.Fprintf(&b, "source = \"modules/%s\"\n", cfg.Name)
		b.WriteString("\n")
	}

	// Toolchain entries
	for _, tc := range cfg.Toolchains {
		fmt.Fprintf(&b, "[modules.%s]\n", tc.Name)
		// Source paths are relative to .dagger/, so prepend ../ to the original path
		fmt.Fprintf(&b, "source = \"../%s\"\n", tc.Source)

		// Add migrated constructor config values
		for _, cust := range tc.Customizations {
			if cust.IsConstructor() && cust.Default != "" {
				fmt.Fprintf(&b, "config.%s = %q\n", cust.Argument, cust.Default)
			}
		}

		// Add warning comments for this toolchain
		for _, w := range warnings {
			if w.toolchain == tc.Name {
				b.WriteString(w.tomlComment())
			}
		}
		b.WriteString("\n")
	}

	// Aliases section (skeleton with TODO)
	b.WriteString("[aliases]\n")
	if cfg.SDK != nil && cfg.SDK.Source != "" {
		fmt.Fprintf(&b, "# TODO: Migrated from project module %q.\n", cfg.Name)
		b.WriteString("# Aliases require module introspection to enumerate functions.\n")
		b.WriteString("# Run `dagger migrate --aliases` after engine support is available.\n")
	}

	return b.String()
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
