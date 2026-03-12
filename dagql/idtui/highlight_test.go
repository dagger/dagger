package idtui

import (
	"testing"

	"github.com/muesli/termenv"
)

func TestHighlightANSI(t *testing.T) {
	style := searchHighlight{
		bg: termenv.ANSIYellow,
		fg: termenv.ANSIBlack,
	}
	hlStart := "\x1b[43;30m"
	hlEnd := "\x1b[0m"

	t.Run("plain text", func(t *testing.T) {
		got := highlightANSI("hello world", "world", style)
		want := "hello " + hlStart + "world" + hlEnd
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("no match", func(t *testing.T) {
		input := "hello world"
		got := highlightANSI(input, "xyz", style)
		if got != input {
			t.Errorf("got %q, want %q", got, input)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		got := highlightANSI("Hello World", "hello", style)
		want := hlStart + "Hello" + hlEnd + " World"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("with ANSI formatting preserved", func(t *testing.T) {
		// Faint text: \x1b[2m ... \x1b[0m
		faint := "\x1b[2m"
		reset := "\x1b[0m"
		input := faint + "hello world" + reset
		got := highlightANSI(input, "world", style)
		// The trailing \x1b[0m from the input is inside the highlight
		// (comes after the last visible char but before the loop ends),
		// so it's copied through but NOT accumulated into savedANSI.
		// Post-loop cleanup emits hlEnd + savedANSI(\x1b[2m]).
		// Key: faint IS restored after highlight ends.
		want := faint + "hello " + hlStart + "world" + reset + hlEnd + faint
		if got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})

	t.Run("multiple ANSI sequences restored", func(t *testing.T) {
		// faint + green fg
		faint := "\x1b[2m"
		green := "\x1b[32m"
		reset := "\x1b[0m"
		input := faint + green + "foo bar baz" + reset
		got := highlightANSI(input, "bar", style)
		// After highlight, both faint and green should be replayed
		want := faint + green + "foo " + hlStart + "bar" + hlEnd + faint + green + " baz" + reset
		if got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})

	t.Run("match at end of visible text", func(t *testing.T) {
		reset := "\x1b[0m"
		input := reset + "foobar" + reset
		got := highlightANSI(input, "bar", style)
		want := reset + "foo" + hlStart + "bar" + hlEnd + reset + reset
		if got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})

	t.Run("empty query", func(t *testing.T) {
		input := "hello"
		got := highlightANSI(input, "", style)
		if got != input {
			t.Errorf("got %q, want %q", got, input)
		}
	})

	t.Run("multiple matches", func(t *testing.T) {
		got := highlightANSI("abcabc", "abc", style)
		want := hlStart + "abc" + hlEnd + hlStart + "abc" + hlEnd
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
