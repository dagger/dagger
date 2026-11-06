package daggercmd

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/cmd/dagger/logo"
)

const (
	rootHelpLogoIndent = 2
	rootHelpLogoGap    = 4
)

// Cobra children inherit the root help func, so rootHelp hands every command
// but the root to defaultHelp.
func rootHelp(defaultHelp func(*cobra.Command, []string)) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		out, ok := cmd.OutOrStdout().(*os.File)
		if cmd != rootCmd || !ok || !term.IsTerminal(int(out.Fd())) || idtui.RunningInAgent() {
			defaultHelp(cmd, args)
			return
		}
		profile := termenv.NewOutput(out).EnvColorProfile()
		dark := profile != termenv.Ascii && idtui.HasDarkBackground()
		fmt.Fprint(out, composeRootHelp(cmd.Short, cmd.UsageString(), logo.Render(profile, dark), getViewWidth()))
	}
}

// composeRootHelp measures the usage rendered at the full width instead of
// rendering it again narrower, so getViewWidth needs no mutable override.
// A width too small for the logo, including the non-positive width of a
// zero-column terminal, gets the help without the logo.
func composeRootHelp(short, usage string, logoRows []string, width int) string {
	art := strings.Join(logoRows, "\n")
	if rootHelpLogoIndent+lipgloss.Width(art) > width {
		return short + "\n\n" + usage
	}
	text := short + "\n\n" + strings.TrimSuffix(usage, "\n")
	if lipgloss.Width(text)+rootHelpLogoGap+lipgloss.Width(art) > width {
		return short + "\n\n" + indentLines(art, rootHelpLogoIndent) + "\n\n" + usage
	}

	joined := lipgloss.JoinHorizontal(lipgloss.Center, text, strings.Repeat(" ", rootHelpLogoGap), art)
	lines := strings.Split(joined, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n") + "\n"
}
