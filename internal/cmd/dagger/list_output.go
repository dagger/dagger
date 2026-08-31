package daggercmd

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/juju/ansiterm/tabwriter"
)

type commandListItem struct {
	Name    string
	Comment string
}

func writeCommandList(w io.Writer, items []commandListItem) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	for _, item := range items {
		if item.Comment == "" {
			fmt.Fprintln(tw, item.Name)
			continue
		}
		fmt.Fprintf(tw, "%s\t# %s\n", item.Name, item.Comment)
	}
	return tw.Flush()
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
