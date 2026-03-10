package dotenv

import (
	"strings"
	"text/scanner"
)

func Escape(s string) string {
	l := &Lex{}
	return l.ProcessWord(s)
}

type Lex struct {
	scanner scanner.Scanner
}

func (s *Lex) ProcessWord(word string) string {
	var result strings.Builder
	s.scanner.Init(strings.NewReader(word))

outer:
	for {
		ch := s.scanner.Next()
		switch ch {
		case scanner.EOF:
			break outer
		case '"':
			result.WriteString(`\"`)
			str := s.processDoubleQuote()
			result.WriteString(str)
		case '\'':
			result.WriteString(`\'`)
			str := s.processSingleQuote()
			result.WriteString(str)
		default:
			result.WriteRune(ch)
		}
	}

	return result.String()
}

func (s *Lex) processDoubleQuote() string {
	var result strings.Builder
outer:
	for {
		ch := s.scanner.Next()
		if ch == scanner.EOF {
			break
		}
		switch ch {
		case '"':
			result.WriteString(`\"`)
			break outer
		case '\\', '\'', '$', '(', ')':
			result.WriteRune('\\')
			result.WriteRune(ch)
		default:
			result.WriteRune(ch)
		}
	}
	return result.String()
}

func (s *Lex) processSingleQuote() string {
	var result strings.Builder
outer:
	for {
		ch := s.scanner.Next()
		if ch == scanner.EOF {
			break
		}
		switch ch {
		case '\'':
			result.WriteString(`\'`)
			break outer
		case '\\', '"', '$', '(', ')':
			result.WriteRune('\\')
			result.WriteRune(ch)
		default:
			result.WriteRune(ch)
		}
	}
	return result.String()
}
