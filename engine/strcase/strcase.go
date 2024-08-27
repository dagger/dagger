package strcase

import (
	"github.com/ettle/strcase"
	legacy "github.com/iancoleman/strcase"
)

func init() {
	// this is still required for backward compatibility
	legacy.ConfigureAcronym("JSON", "JSON")
}

type Caser interface {
	ToCamel(inp string) string
	ToPascal(inp string) string
	ToKebab(inp string) string
	ToScreamingSnake(inp string) string
	ToSnake(inp string) string
	ConfigureAcronyms(key, val string)
}

// Caser is backed by github.com/ettle/strcase, which replaces the
// old implementation due to subtle issues.
//
// This customization of strcase.Caser is also required in SDK runtimes.
// But to avoid taking a dependency on engine/strcase, currently this code
// is duplicated in the runtime/strcase.go files. Any changes made here
// should be copied over to those files as well.
type CaserImpl struct {
	impl *strcase.Caser
}

var defaultCaser = NewCaser()

func NewCaser() *CaserImpl {
	var splitFn = strcase.NewSplitFn(
		[]rune{'*', '.', ',', '-', '_'},
		strcase.SplitCase,
		strcase.SplitAcronym,
		strcase.PreserveNumberFormatting,
		strcase.SplitBeforeNumber,
		strcase.SplitAfterNumber,
	)

	return &CaserImpl{impl: strcase.NewCaser(false, nil, splitFn)}
}

// ToPascal returns words in PascalCase (capitalized words concatenated together).
func (c *CaserImpl) ToPascal(inp string) string {
	return c.impl.ToCase(inp, strcase.TitleCase|strcase.PreserveInitialism, '\u0000')
}

// ToCamel returns words in camelCase (capitalized words concatenated together, with first word lower case).
func (c *CaserImpl) ToCamel(inp string) string {
	return c.impl.ToCamel(inp)
}

// ToKebab returns words in kebab-case (lower case words with dashes).
func (c *CaserImpl) ToKebab(inp string) string {
	return c.impl.ToKebab(inp)
}

// ToScreamingSnake returns words in SNAKE_CASE (upper case words with underscores).
func (c *CaserImpl) ToScreamingSnake(inp string) string {
	return c.impl.ToSNAKE(inp)
}

// ToSnake returns words in snake_case (lower case words with underscores).
func (c *CaserImpl) ToSnake(inp string) string {
	return c.impl.ToSnake(inp)
}

// ConfigureAcronyms is no-op in new caser impl.
func (c *CaserImpl) ConfigureAcronyms(key, value string) {
	// no-op
}

// LegacyCaser backed by github.com/iancoleman/strcase
type LegacyCaser struct{}

func NewLegacyCaser() *LegacyCaser {
	return &LegacyCaser{}
}

// ToPascal returns words in PascalCase (capitalized words concatenated together).
func (c *LegacyCaser) ToPascal(inp string) string {
	return legacy.ToCamel(inp)
}

// ToCamel returns words in camelCase (capitalized words concatenated together, with first word lower case).
func (c *LegacyCaser) ToCamel(inp string) string {
	return legacy.ToLowerCamel(inp)
}

// ToKebab returns words in kebab-case (lower case words with dashes).
func (c *LegacyCaser) ToKebab(inp string) string {
	return legacy.ToKebab(inp)
}

// ToScreamingSnake returns words in SNAKE_CASE (upper case words with underscores).
func (c *LegacyCaser) ToScreamingSnake(inp string) string {
	return legacy.ToScreamingSnake(inp)
}

// ToSnake returns words in snake_case (lower case words with underscores).
func (c *LegacyCaser) ToSnake(inp string) string {
	return legacy.ToSnake(inp)
}

// ConfigureAcronyms - this configures the acronym for anything using
// old strcase (github.com/iancoleman/strcase) package not just this instance
func (c *LegacyCaser) ConfigureAcronyms(key, value string) {
	legacy.ConfigureAcronym(key, value)
}

// ToPascal returns words in PascalCase (capitalized words concatenated together).
func ToPascal(inp string) string {
	return defaultCaser.impl.ToCase(inp, strcase.TitleCase|strcase.PreserveInitialism, '\u0000')
}

// ToCamel returns words in camelCase (capitalized words concatenated together, with first word lower case).
func ToCamel(inp string) string {
	return defaultCaser.ToCamel(inp)
}

// ToKebab returns words in kebab-case (lower case words with dashes).
func ToKebab(inp string) string {
	return defaultCaser.ToKebab(inp)
}

// ToScreamingSnake returns words in SNAKE_CASE (upper case words with underscores).
func ToScreamingSnake(inp string) string {
	return defaultCaser.ToScreamingSnake(inp)
}

// ToSnake returns words in snake_case (lower case words with underscores).
func ToSnake(inp string) string {
	return defaultCaser.ToSnake(inp)
}

// ToSnake returns words in snake_case (lower case words with underscores).
func ConfigureAcronyms(key, value string) {
	defaultCaser.ConfigureAcronyms(key, value)
}
