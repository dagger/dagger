package main

import "github.com/iancoleman/strcase"

func NormalizeObjectName(n string) string {
	return strcase.ToCamel(n)
}
