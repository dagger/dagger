package idtui

import (
	"io"
	"os"
	"sync"

	"charm.land/lipgloss/v2"
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

var (
	bgOnce    = &sync.Once{}
	hasDarkBG bool
)

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
			hasDarkBG = false
		} else if os.Getenv("FORCE_DARK_MODE") != "" ||
			os.Getenv("THEME_MODE") == "dark" ||
			os.Getenv("DARK") != "" {
			hasDarkBG = true
		} else {
			hasDarkBG = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
		}
	})
	return hasDarkBG
}

func hl(st termenv.Style) termenv.Style {
	return st.Foreground(AdaptiveColor{
		Dark:  termenv.ANSI256Color(255),
		Light: termenv.ANSI256Color(0),
	})
}
