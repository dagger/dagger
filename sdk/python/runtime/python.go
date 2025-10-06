package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/iancoleman/strcase"
)

var (
	canonicalize = regexp.MustCompile(`[._-]+`)
	disallowed   = regexp.MustCompile(`[^a-z0-9-]+`)
)

// NormalizeProjectName normalizes the project name in pyproject.toml
//
// Additionally to PEP 508, non-allowed characters are simply removed
// instead of raising an error.
//
// See https://packaging.python.org/en/latest/specifications/name-normalization/
func NormalizeProjectName(n string) string {
	// project name is case insensitive
	n = strings.ToLower(n)
	// valid non alphanumeric chars, even if repeated, should be replaced
	// with a single "-"
	n = canonicalize.ReplaceAllString(n, "-")
	// instead of erroring, remove any other disallowed characters
	n = disallowed.ReplaceAllString(n, "")
	// remove leading and trailing dashes
	return strings.Trim(n, "-")
}

// NormalizeProjectNameFromModule normalizes the project name in `pyproject.toml`
// from the module name in `dagger.json`.
//
// Since the name in `dagger.json` currently allows more than what's valid for
// `pyproject.toml`, we allow `camelCase` and convert spaces to `-` before
// normalizing the name to PEP 508 standard.
func NormalizeProjectNameFromModule(n string) string {
	// Since the main object name is the `PascalCase` version of the
	// module's name, let's just convert to `kebab-case` from that.
	n = NormalizeObjectName(n)
	n = strcase.ToKebab(n)
	return NormalizeProjectName(n)
}

// NormalizePackageName normalizes the name of the directory where the
// source files will be imported from
//
// Assumes input is the normalized project name.
func NormalizePackageName(n string) string {
	return strings.ReplaceAll(n, "-", "_")
}

// NormalizeObjectName normalizes the name of the class that is the main
// Dagger object of the module
func NormalizeObjectName(n string) string {
	return strcase.ToCamel(n)
}

// VendorConfig appends to a pyproject.toml the config to vendor the client library
func VendorConfig(cfg, path string) string {
	if path == "" {
		return cfg
	}
	return fmt.Sprintf(
		"%s\n\n[tool.uv.sources]\ndagger-io = { path = %q, editable = true }\n",
		strings.TrimSpace(cfg),
		path,
	)
}
