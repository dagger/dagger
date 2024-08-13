package main

import (
	"github.com/ettle/strcase"
)

// This customization of strcase.Caser is copied over from engine/strcase.
// Thie code is duplicated here to avoid taking a dependency on engine/strcase,
// Any changes made here should be made in engine/strcase package as well.
var caser *strcase.Caser

func init() {
	caser = newCaser()
}

func newCaser() *strcase.Caser {
	var splitFn = strcase.NewSplitFn(
		[]rune{'*', '.', ',', '-', '_'},
		strcase.SplitCase,
		strcase.SplitAcronym,
		strcase.PreserveNumberFormatting,
		strcase.SplitBeforeNumber,
		strcase.SplitAfterNumber,
	)

	return strcase.NewCaser(false, nil, splitFn)
}

// ToPascal returns words in PascalCase (capitalized words concatenated together).
func ToPascal(inp string) string {
	return caser.ToCase(inp, strcase.TitleCase|strcase.PreserveInitialism, '\u0000')
}

// ToCamel returns words in camelCase (capitalized words concatenated together, with first word lower case).
func ToCamel(inp string) string {
	return caser.ToCamel(inp)
}

// ToKebab returns words in kebab-case (lower case words with dashes).
func ToKebab(inp string) string {
	return caser.ToKebab(inp)
}

// ToScreamingSnake returns words in SNAKE_CASE (upper case words with underscores).
func ToScreamingSnake(inp string) string {
	return caser.ToSNAKE(inp)
}

// ToSnake returns words in snake_case (lower case words with underscores).
func ToSnake(inp string) string {
	return caser.ToSnake(inp)
}
