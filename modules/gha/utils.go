package main

import (
	"regexp"
	"strings"

	"dario.cat/mergo"
	"github.com/iancoleman/strcase"
)

func idify(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	name = strings.ToLower(re.ReplaceAllString(name, "-"))
	return strcase.ToKebab(name)
}

func setDefault[T comparable](value *T, defaultValue T) {
	var empty T
	if *value == empty {
		*value = defaultValue
	}
}
func mergeDefault[T any](value *T, defaultValue T) {
	err := mergo.Merge(value, defaultValue)
	if err != nil {
		panic(err)
	}
}
