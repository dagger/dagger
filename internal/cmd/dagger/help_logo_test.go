package daggercmd

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/cmd/dagger/logo"
)

const testRootUsage = `USAGE
  dagger [options] [subcommand | file...]

AVAILABLE COMMANDS
  init            Initialize a workspace and show the next commands
  check           Verify your project
  generate        Generate derived files for your project
  shell, sh       Open a terminal for a container or directory in your project
  start           Run your project's services for local development

  install         Install a module into your workspace
  lock            Manage Dagger lockfiles
  module, mod     Install, use, and develop Dagger modules
  sdk             Inspect and configure SDKs
  settings        Get, set, or unset module settings
  uninstall       Uninstall a module from your workspace

  help            Help about any command
  version         Print dagger version

Use "dagger [command] --help" for more information about a command.
`

func isBraille(r rune) bool {
	return r >= '\u2800' && r <= '\u28ff'
}

func TestRootHelpWideLayoutPutsLogoBesideText(t *testing.T) {
	rows := logo.Render(termenv.TrueColor, true)
	short := "A tool to run composable workflows in containers"
	text := short + "\n\n" + strings.TrimSuffix(testRootUsage, "\n")
	width := lipgloss.Width(text) + rootHelpLogoGap + 37

	help := composeRootHelp(short, testRootUsage, rows, width)

	lines := strings.Split(strings.TrimSuffix(help, "\n"), "\n")
	require.Len(t, lines, strings.Count(text, "\n")+1)
	require.Equal(t, text, strings.Join(trimLogo(lines), "\n"))
	beside := 0
	for _, line := range lines {
		require.LessOrEqual(t, ansi.StringWidth(line), width)
		if strings.ContainsFunc(line, isBraille) {
			beside++
		}
	}
	require.Equal(t, len(rows), beside)
}

func TestRootHelpNarrowLayoutPutsLogoAboveUsage(t *testing.T) {
	rows := logo.Render(termenv.Ascii, true)
	short := "A tool to run composable workflows in containers"
	text := short + "\n\n" + strings.TrimSuffix(testRootUsage, "\n")
	width := lipgloss.Width(text) + rootHelpLogoGap + 37 - 1

	help := composeRootHelp(short, testRootUsage, rows, width)

	require.True(t, strings.HasPrefix(help, short+"\n\n"))
	require.True(t, strings.HasSuffix(help, "\n\n"+testRootUsage))
	usage := strings.Index(help, "USAGE")
	for _, row := range rows {
		at := strings.Index(help, "  "+row+"\n")
		require.NotEqual(t, -1, at)
		require.Less(t, at, usage)
	}
}

func TestRootHelpOmitsLogoThatCannotFit(t *testing.T) {
	rows := logo.Render(termenv.Ascii, true)
	short := "A tool to run composable workflows in containers"

	require.Contains(t, composeRootHelp(short, testRootUsage, rows, 39), "  "+rows[0]+"\n")
	for _, width := range []int{38, 1, 0, -1} {
		require.Equal(t, short+"\n\n"+testRootUsage, composeRootHelp(short, testRootUsage, rows, width), "width %d", width)
	}
}

func TestRootHelpWithoutTerminalHasNoLogo(t *testing.T) {
	help := renderHelp(t, rootCmd)
	require.Contains(t, help, "AVAILABLE COMMANDS")
	require.False(t, strings.ContainsFunc(help, isBraille))
}

func trimLogo(lines []string) []string {
	text := make([]string, len(lines))
	for i, line := range lines {
		if at := strings.IndexFunc(ansi.Strip(line), isBraille); at != -1 {
			line = ansi.Strip(line)[:at]
		}
		text[i] = strings.TrimRight(line, " ")
	}
	return text
}
