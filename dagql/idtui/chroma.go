package idtui

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

var lightStyle, darkStyle *chroma.Style

func init() {
	registerANSIStyles()
}

func TTYStyle() *chroma.Style {
	if HasDarkBackground() {
		return darkStyle
	}
	return lightStyle
}

// taken from chroma's TTY formatter
var ttyMap = map[string]string{
	"30m": "#000000", "31m": "#7f0000", "32m": "#007f00", "33m": "#7f7fe0",
	"34m": "#00007f", "35m": "#7f007f", "36m": "#007f7f", "37m": "#e5e5e5",
	"90m": "#555555", "91m": "#ff0000", "92m": "#00ff00", "93m": "#ffff00",
	"94m": "#0000ff", "95m": "#ff00ff", "96m": "#00ffff", "97m": "#ffffff",
}

func registerANSIStyles() {
	// TTY style matches to hex codes used by the TTY formatter to map them to
	// specific ANSI escape codes.
	base := chroma.StyleEntries{
		chroma.Comment:             ttyMap["95m"] + " italic",
		chroma.CommentPreproc:      ttyMap["90m"],
		chroma.KeywordConstant:     ttyMap["33m"],
		chroma.Keyword:             ttyMap["31m"],
		chroma.KeywordDeclaration:  ttyMap["35m"],
		chroma.NameBuiltin:         ttyMap["31m"],
		chroma.NameBuiltinPseudo:   ttyMap["36m"],
		chroma.NameFunction:        ttyMap["34m"],
		chroma.NameNamespace:       ttyMap["34m"],
		chroma.LiteralNumber:       ttyMap["31m"],
		chroma.LiteralString:       ttyMap["32m"],
		chroma.LiteralStringSymbol: ttyMap["33m"],
		chroma.Operator:            ttyMap["31m"],
		chroma.Punctuation:         ttyMap["90m"],
		chroma.Error:               ttyMap["91m"], // bright red for errors
		chroma.GenericDeleted:      ttyMap["91m"], // bright red for deleted content
		chroma.GenericEmph:         "italic",
		chroma.GenericInserted:     ttyMap["92m"], // bright green for inserted content
		chroma.GenericStrong:       "bold",
		chroma.GenericSubheading:   ttyMap["90m"], // dark gray for subheadings
		chroma.KeywordNamespace:    ttyMap["95m"], // bright magenta for namespace keywords
		chroma.Literal:             ttyMap["94m"], // bright blue for literals
		chroma.LiteralDate:         ttyMap["93m"], // bright yellow for dates
		chroma.LiteralStringEscape: ttyMap["96m"], // bright cyan for string escapes
		chroma.NameAttribute:       ttyMap["92m"], // bright green for attributes
		chroma.NameClass:           ttyMap["92m"], // bright green for classes
		chroma.NameConstant:        ttyMap["94m"], // bright blue for constants
		chroma.NameDecorator:       ttyMap["92m"], // bright green for decorators
		chroma.NameException:       ttyMap["91m"], // bright red for exceptions
		chroma.NameOther:           ttyMap["92m"], // bright green for other names
		chroma.NameTag:             ttyMap["95m"], // bright magenta for tags
		// chroma.Name:             ...,
		// chroma.Text:             ...,
	}

	ansi16dark := base
	ansi16dark[chroma.Name] = ttyMap["97m"]
	ansi16dark[chroma.Text] = ttyMap["97m"]
	darkStyle = styles.Register(chroma.MustNewStyle("ansi16", ansi16dark))

	ansi16light := base
	ansi16light[chroma.Name] = ttyMap["30m"]
	ansi16light[chroma.Text] = ttyMap["30m"]
	lightStyle = styles.Register(chroma.MustNewStyle("ansi16light", ansi16light))
}

func highlightStyle() string {
	if HasDarkBackground() {
		return "ansi16"
	}
	return "ansi16light"
}
