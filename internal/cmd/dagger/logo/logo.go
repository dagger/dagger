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

// Render returns the Dagger logo as braille rows, colored for profile.
// dark selects the rocket color that stays visible on the terminal background.
func Render(profile termenv.Profile, dark bool) []string {
	rocket := "#151428"
	if dark {
		rocket = "#E8E8F0"
	}
	colors := map[byte]string{
		'2': rocket,
		'3': "#BA1E45",
		'4': "#F07D1A",
		'5': "#40BABF",
		'6': "#FCC009",
		'7': "#9A98B8",
	}

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
			if hex, ok := colors[class]; ok {
				run = profile.String(run).Foreground(profile.Color(hex)).String()
			}
			row.WriteString(run)
			start = end
		}
		rows[i] = row.String()
	}
	return rows
}
