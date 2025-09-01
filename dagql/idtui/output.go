package idtui

import (
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// NewOutput returns a termenv.Output that will always use color, regardless of
// whether w is a TTY, unless NO_COLOR is explicitly set.
//
// Progrock is opinionated here. Termenv disables colors by default if
// stdout is not a TTY or if the CI env var is set. We don't want that,
// because folks deserve colorful output in CI too.
//
// To disable colors, set NO_COLOR (https://no-color.org/).
func NewOutput(w io.Writer, opts ...termenv.OutputOption) *termenv.Output {
	return termenv.NewOutput(w, append([]termenv.OutputOption{
		termenv.WithProfile(ColorProfile()),
		termenv.WithTTY(true),
	}, opts...)...)
}

// ColorProfile returns Ascii if, and only if, NO_COLOR or similar is set.
// Otherwise it returns termenv.ANSI, allowing colors to be used.
//
// Note that color profiles beyond simple ANSI are not used by Progrock. 16
// colors is all you need. Anything else disrespects the user's color scheme
// preferences.
func ColorProfile() termenv.Profile {
	if termenv.EnvNoColor() {
		return termenv.Ascii
	} else {
		return termenv.ANSI
	}
}

var bgOnce = &sync.Once{}

type AdaptiveColor struct {
	Light termenv.Color
	Dark  termenv.Color
}

func (c AdaptiveColor) Sequence(bg bool) string {
	if HasDarkBackground() {
		return c.Dark.Sequence(bg)
	}
	return c.Light.Sequence(bg)
}

func HasDarkBackground() bool {
	bgOnce.Do(func() {
		if os.Getenv("FORCE_LIGHT_MODE") != "" ||
			os.Getenv("THEME_MODE") == "light" ||
			os.Getenv("LIGHT") != "" {
			lipgloss.SetHasDarkBackground(false)
		} else if os.Getenv("FORCE_DARK_MODE") != "" ||
			os.Getenv("THEME_MODE") == "dark" ||
			os.Getenv("DARK") != "" {
			lipgloss.SetHasDarkBackground(true)
		}
	})
	return lipgloss.HasDarkBackground()
}

func hl(st termenv.Style) termenv.Style {
	return st.Reverse()
}
