package main

import "github.com/iancoleman/strcase"

func NormalizeProjectName(n string) string {
	// FIXME: handle non-allowed characters according to spec
	return strcase.ToKebab(n)
}

func NormalizePackageName(n string) string {
	// FIXME: handle non-allowed characters according to spec
	return strcase.ToSnake(n)
}

func NormalizeObjectName(n string) string {
	return strcase.ToCamel(n)
}
