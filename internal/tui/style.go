package tui

import "github.com/charmbracelet/lipgloss"

// palette
var (
	colorBackground = lipgloss.Color("0") // black
	colorFailed     = lipgloss.Color("1") // red
	colorCompleted  = lipgloss.Color("2") // green
	colorStarted    = lipgloss.Color("3") // yellow
	colorSelected   = lipgloss.Color("4") // blue
	colorAccent1    = lipgloss.Color("5") // magenta
	colorAccent2    = lipgloss.Color("6") // cyan
	colorForeground = lipgloss.Color("7") // white
	colorFaint      = lipgloss.Color("8") // bright black

	colorLightForeground = lipgloss.AdaptiveColor{Light: "8", Dark: "15"}
	colorLightBackground = lipgloss.AdaptiveColor{Light: "15", Dark: "8"}
)

// status bar
var (
	statusNugget = lipgloss.NewStyle().
			Foreground(colorForeground).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorLightForeground).
			Background(colorLightBackground)

	followMode = lipgloss.NewStyle().
			Inherit(statusBarStyle).
			Background(colorAccent2).
			Foreground(colorBackground).
			Padding(0, 1).
			MarginRight(1).
			SetString("FOLLOW")

	browseMode = followMode.Copy().
			Background(colorAccent1).
			Foreground(colorBackground).
			SetString("BROWSE")

	runningStatus = statusNugget.Copy().
			Background(colorStarted).
			Foreground(colorBackground).
			Align(lipgloss.Right).
			PaddingRight(0).
			SetString("RUNNING ")

	completeStatus = runningStatus.Copy().
			Background(colorAccent2).
			Foreground(colorBackground).
			Align(lipgloss.Right).
			SetString("COMPLETE ")

	statusText = lipgloss.NewStyle().Inherit(statusBarStyle)

	timerStyle = statusNugget.Copy().
			Background(colorStarted).
			Foreground(colorBackground)
)

// tree
var (
	itemTimerStyle = lipgloss.NewStyle().
			Inline(true).
			Foreground(colorFaint)

	selectedStyle = lipgloss.NewStyle().
			Inline(true).
			Foreground(colorBackground).
			Background(colorSelected).
			Bold(false)

	selectedStyleBlur = lipgloss.NewStyle().
				Inline(true).
				Background(colorForeground).
				Foreground(colorBackground)

	completedStatus = lipgloss.NewStyle().
			Inline(true).
			Foreground(colorCompleted).
			SetString("✔")
	failedStatus = lipgloss.NewStyle().
			Inline(true).
			Foreground(colorFailed).
			SetString("✖")
	cachedStatus = lipgloss.NewStyle().
			Inline(true).
			Foreground(colorFaint).
			SetString("●")
)

var (
	borderLeft = func() lipgloss.Border {
		b := lipgloss.RoundedBorder()
		b.Right = "├"
		return b
	}()
	borderRight = func() lipgloss.Border {
		b := lipgloss.RoundedBorder()
		b.Left = "│"
		return b
	}()
)

// details
var (
	titleStyle = lipgloss.NewStyle().
			Border(borderLeft).
			Padding(0, 1).
			Foreground(colorForeground)

	titleBarStyle = lipgloss.NewStyle().
			Foreground(colorForeground)

	infoStyle = lipgloss.NewStyle().
			Border(borderRight).
			Padding(0, 1).
			Foreground(colorForeground)

	errorStyle = lipgloss.NewStyle().Inline(true).Foreground(colorFailed)
)
