package logo

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func TestLogoClassesMatchArt(t *testing.T) {
	artRows := strings.Split(strings.TrimSuffix(art, "\n"), "\n")
	classRows := strings.Split(strings.TrimSuffix(classes, "\n"), "\n")
	require.Len(t, artRows, 19)
	require.Len(t, classRows, len(artRows))
	for i, artRow := range artRows {
		cells := []rune(artRow)
		require.Len(t, cells, 37)
		require.Len(t, classRows[i], len(cells))
		for j, cell := range cells {
			blank := cell == '\u2800'
			require.Equal(t, blank, classRows[i][j] == '.', "row %d column %d", i, j)
		}
	}
}

func TestLogoAsciiIsPlainArt(t *testing.T) {
	rows := Render(termenv.Ascii, true)
	require.Equal(t, strings.TrimSuffix(art, "\n"), strings.Join(rows, "\n"))
	require.NotContains(t, strings.Join(rows, ""), "\x1b")
}

func TestLogoColorKeepsWidth(t *testing.T) {
	for _, row := range Render(termenv.TrueColor, true) {
		require.Equal(t, 37, ansi.StringWidth(row))
	}
}

func TestLogoRocketFollowsBackground(t *testing.T) {
	dark := strings.Join(Render(termenv.TrueColor, true), "\n")
	light := strings.Join(Render(termenv.TrueColor, false), "\n")
	require.Contains(t, dark, "38;2;232;232;240m")
	require.NotContains(t, light, "38;2;232;232;240m")
	require.Contains(t, light, "38;2;21;20;40m")
	require.Equal(t, ansi.Strip(dark), ansi.Strip(light))
}
