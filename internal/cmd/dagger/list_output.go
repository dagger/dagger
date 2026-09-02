package daggercmd

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

type commandListItem struct {
	Name    string
	Comment string
}

func writeCommandList(w io.Writer, items []commandListItem) error {
	maxNameWidth := 0
	for _, item := range items {
		maxNameWidth = max(maxNameWidth, utf8.RuneCountInString(item.Name))
	}

	for _, item := range items {
		if item.Comment == "" {
			_, err := fmt.Fprintln(w, item.Name)
			if err != nil {
				return err
			}
		} else {
			padding := maxNameWidth - utf8.RuneCountInString(item.Name) + 3
			_, err := fmt.Fprintf(w, "%s%s# %s\n", item.Name, strings.Repeat(" ", padding), item.Comment)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func firstDescriptionLine(description string) string {
	if idx := strings.Index(description, "\n"); idx != -1 {
		description = description[:idx]
	}
	return strings.TrimSpace(description)
}

func generatedCheckComment(description string) string {
	description = stripTrailingPunctuation(firstDescriptionLine(description))
	if description == "" {
		return ""
	}
	return fmt.Sprintf("Did you %q?", lowerFirstRune(description))
}

func stripTrailingPunctuation(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ".:;!?")
}

func lowerFirstRune(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size == 0 {
		return s
	}
	return string(unicode.ToLower(r)) + s[size:]
}
