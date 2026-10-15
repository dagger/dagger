package naming

import (
	"fmt"
	"strings"
)

// Casing is a convention for joining words into an identifier.
type Casing int

const (
	// Pascal capitalizes every word: HTTPClient.
	Pascal Casing = iota
	// Camel lowercases the first word and capitalizes the rest: httpClient.
	Camel
	// Snake lowercases every word and joins them with "_": http_client.
	Snake
	// ScreamingSnake uppercases every word and joins them with "_":
	// HTTP_CLIENT.
	ScreamingSnake
	// Kebab lowercases every word and joins them with "-": http-client.
	Kebab
	// Flat lowercases every word and drops the boundaries: httpclient. It is
	// for Go package and Python module names, and doesn't round-trip.
	Flat
)

// Casings lists every Casing.
var Casings = []Casing{Pascal, Camel, Snake, ScreamingSnake, Kebab, Flat}

func (c Casing) String() string {
	switch c {
	case Pascal:
		return "PASCAL"
	case Camel:
		return "CAMEL"
	case Snake:
		return "SNAKE"
	case ScreamingSnake:
		return "SCREAMING_SNAKE"
	case Kebab:
		return "KEBAB"
	case Flat:
		return "FLAT"
	default:
		return fmt.Sprintf("Casing(%d)", int(c))
	}
}

// AcronymStyle is how acronyms and terms are written where a word starts
// with a capital. It only affects Pascal and Camel.
type AcronymStyle int

const (
	// Uppercase writes HTTPClient, IPv6Address, GitHubRepo.
	Uppercase AcronymStyle = iota
	// Capitalized writes HttpClient, Ipv6Address, GitHubRepo.
	Capitalized
)

// AcronymStyles lists every AcronymStyle.
var AcronymStyles = []AcronymStyle{Uppercase, Capitalized}

func (s AcronymStyle) String() string {
	switch s {
	case Uppercase:
		return "UPPERCASE"
	case Capitalized:
		return "CAPITALIZED"
	default:
		return fmt.Sprintf("AcronymStyle(%d)", int(s))
	}
}

// Format formats id's words in a casing.
func Format(id Identifier, casing Casing, style AcronymStyle) string {
	return id.Format(casing, style)
}

// Format formats the identifier's words in a casing.
func (id Identifier) Format(casing Casing, style AcronymStyle) string {
	var b strings.Builder
	for i, w := range id.Words {
		switch casing {
		case Pascal:
			b.WriteString(w.capitalized(style))
		case Camel:
			if i == 0 {
				b.WriteString(w.lower())
			} else {
				b.WriteString(w.capitalized(style))
			}
		case Snake:
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteString(w.lower())
		case ScreamingSnake:
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteString(strings.ToUpper(w.Text + w.Suffix))
		case Kebab:
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteString(w.lower())
		case Flat:
			b.WriteString(w.lower())
		default:
			panic(fmt.Sprintf("naming: unknown casing %d", int(casing)))
		}
	}
	return b.String()
}

// Convert parses name with the Latest dictionary and formats it in a casing.
func Convert(name string, casing Casing, style AcronymStyle) (string, error) {
	id, err := Parse(name)
	if err != nil {
		return "", err
	}
	return id.Format(casing, style), nil
}

func (w Word) lower() string {
	return strings.ToLower(w.Text + w.Suffix)
}

// capitalized is the word's form where it starts with a capital.
func (w Word) capitalized(style AcronymStyle) string {
	var text string
	switch {
	case w.Kind == KindWord:
		text = upperFirst(strings.ToLower(w.Text))
	case style == Capitalized && w.Term != nil:
		text = w.Term.Capitalized
	case style == Capitalized:
		text = upperFirst(strings.ToLower(w.Text))
	case w.Kind == KindTerm:
		text = upperFirst(w.Text)
	default:
		text = w.Text
	}
	return text + w.Suffix
}

func upperFirst(s string) string {
	if s == "" || !isLower(s[0]) {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
