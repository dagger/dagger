package logo

import (
	_ "embed"
	"strings"

	"github.com/muesli/termenv"
)

// Regenerate both files from the repository root with:
//
//	uv run --with pillow --with numpy python hack/dagger-logo/braille.py hack/dagger-logo/logo.png 37 plain > internal/cmd/dagger/logo/logo.txt
//	uv run --with pillow --with numpy python hack/dagger-logo/braille.py hack/dagger-logo/logo.png 37 classes > internal/cmd/dagger/logo/logo.classes.txt
var (
	//go:embed logo.txt
	art string
	//go:embed logo.classes.txt
	classes string
)

const (
	rocket  = '2'
	petals  = '3'
	flame   = '4'
	wings   = '5'
	exhaust = '6'
	circle  = '7'
)

type classColor struct {
	// dark and light are xterm-256 cube colors: termenv's ANSI256 conversion
	// returns them unchanged, while it turns some other reds into near-black.
	dark, light string
	// ansi is a theme palette slot. Themes keep their normal slots legible on
	// their own background, so one slot serves both backgrounds. Black, white,
	// bright black and the bright slots Solarized turns into greys are avoided.
	ansi termenv.Color
}

var classColors = map[byte]classColor{
	rocket:  {dark: "#ffffff", light: "#00005f", ansi: termenv.NoColor{}},
	petals:  {dark: "#ff005f", light: "#d7005f", ansi: termenv.ANSIRed},
	flame:   {dark: "#ff8700", light: "#d75f00", ansi: termenv.ANSIBrightRed},
	wings:   {dark: "#5fd7d7", light: "#008787", ansi: termenv.ANSICyan},
	exhaust: {dark: "#ffd700", light: "#af8700", ansi: termenv.ANSIYellow},
	circle:  {dark: "#afafff", light: "#5f5fd7", ansi: termenv.ANSIBlue},
}

func (c classColor) color(profile termenv.Profile, dark bool) termenv.Color {
	switch {
	case profile == termenv.ANSI:
		return c.ansi
	case dark:
		return profile.Color(c.dark)
	default:
		return profile.Color(c.light)
	}
}

// Render returns the Dagger logo as braille rows, colored for profile.
// dark selects the colors that stay visible on the terminal background.
func Render(profile termenv.Profile, dark bool) []string {
	artRows := strings.Split(strings.TrimSuffix(art, "\n"), "\n")
	classRows := strings.Split(strings.TrimSuffix(classes, "\n"), "\n")
	rows := make([]string, len(artRows))
	for i, artRow := range artRows {
		cells := []rune(artRow)
		var row strings.Builder
		for start := 0; start < len(cells); {
			class := classRows[i][start]
			end := start + 1
			for end < len(cells) && classRows[i][end] == class {
				end++
			}
			run := string(cells[start:end])
			if c, ok := classColors[class]; ok {
				run = profile.String(run).Foreground(c.color(profile, dark)).String()
			}
			row.WriteString(run)
			start = end
		}
		rows[i] = row.String()
	}
	return rows
}
