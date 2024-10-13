package main

import (
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
	n = strings.ToLower(n)
	n = canonicalize.ReplaceAllString(n, "-")
	n = disallowed.ReplaceAllString(n, "")
	n = strings.Trim(n, "-")
	return n
}

// NormalizePackageName normalizes the name of the directory where the
// source files will be imported from
func NormalizePackageName(n string) string {
	return strings.ReplaceAll(NormalizeProjectName(n), "-", "_")
}

// NormalizeObjectName normalizes the name of the class that is the main
// Dagger object of the module
func NormalizeObjectName(n string) string {
	return strcase.ToCamel(NormalizeProjectName(n))
}
