package logo

import (
	"fmt"
	"math"
	"strconv"
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
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.ANSI} {
		for _, dark := range []bool{true, false} {
			for _, row := range Render(profile, dark) {
				require.Equal(t, 37, ansi.StringWidth(row))
			}
		}
	}
}

func TestLogoRocketFollowsBackground(t *testing.T) {
	dark := strings.Join(Render(termenv.TrueColor, true), "\n")
	light := strings.Join(Render(termenv.TrueColor, false), "\n")
	require.Contains(t, dark, "38;2;255;255;255m")
	require.NotContains(t, light, "38;2;255;255;255m")
	require.Contains(t, light, "38;2;0;0;95m")
	require.Equal(t, ansi.Strip(dark), ansi.Strip(light))
}

func TestLogoColorsContrastWithBackground(t *testing.T) {
	backgrounds := map[bool][]string{
		true:  {"#1e1e1e"},
		false: {"#fdf6e3", "#ffffff"},
	}
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256} {
		for dark, bgs := range backgrounds {
			used := map[string]byte{}
			for class, c := range classColors {
				fg := fmt.Sprint(c.color(profile, dark))
				for _, bg := range bgs {
					require.GreaterOrEqual(t, contrast(fg, bg), 3.0, "class %c (%s) on %s, profile %d", class, fg, bg, profile)
				}
				require.NotContains(t, used, fg, "class %c, profile %d, dark %t", class, profile, dark)
				used[fg] = class
			}
		}
	}
}

func TestLogoANSIUsesLegibleThemeSlots(t *testing.T) {
	avoided := []termenv.ANSIColor{
		termenv.ANSIBlack, termenv.ANSIWhite, termenv.ANSIBrightBlack, termenv.ANSIBrightWhite,
		termenv.ANSIBrightGreen, termenv.ANSIBrightYellow, termenv.ANSIBrightBlue, termenv.ANSIBrightCyan,
	}
	used := map[termenv.ANSIColor]byte{}
	for class, c := range classColors {
		if _, ok := c.ansi.(termenv.NoColor); ok {
			require.Equal(t, byte(rocket), class)
			continue
		}
		slot, ok := c.ansi.(termenv.ANSIColor)
		require.True(t, ok, "class %c", class)
		require.NotContains(t, avoided, slot, "class %c", class)
		require.NotContains(t, used, slot, "class %c", class)
		used[slot] = class
	}
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

func luminance(hex string) float64 {
	rgb, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil {
		panic(err)
	}
	channel := func(shift uint) float64 {
		c := float64(rgb>>shift&0xff) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(16) + 0.7152*channel(8) + 0.0722*channel(0)
}
