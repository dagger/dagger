package main

import (
	"strings"

	"github.com/iancoleman/strcase"
)

// NormalizeProjectName normalizes the project name in pyproject.toml
//
// Should return a valid name like described in PEP 508, but instead of erroring
// on non-allowed characters, it's converting to a valid name, because the
// name in `dagger.json` could be anything right now.
//
// That means we allow camelCase and spaces to be converted to `-`, unlike PEP 508
// which ignores casing and removes spaces.
//
// See https://packaging.python.org/en/latest/specifications/name-normalization/
func NormalizeProjectName(n string) string {
	// Since the main object name is the `PascalCase` version of the
	// module's name, let's just convert to `kebab-case` from that to make
	// sure that converting to `PascalCase` from the project name returns
	// the same result.
	return strcase.ToKebab(NormalizeObjectName(n))
}

// NormalizePackageName normalizes the name of the directory where the
// source files will be imported from
func NormalizePackageName(n string) string {
	return strings.ReplaceAll(NormalizeProjectName(n), "-", "_")
}

// NormalizeObjectName normalizes the name of the class that is the main
// Dagger object of the module
func NormalizeObjectName(n string) string {
	return strcase.ToCamel(n)
}
