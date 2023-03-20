package main

import "github.com/charmbracelet/lipgloss"

// status bar
var (
	statusNugget = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "8", Dark: "15"}).
			Background(lipgloss.AdaptiveColor{Light: "15", Dark: "8"})

	followMode = lipgloss.NewStyle().
			Inherit(statusBarStyle).
			Background(lipgloss.Color("6")).
			Foreground(lipgloss.Color("0")).
			Padding(0, 1).
			MarginRight(1).
			SetString("FOLLOW")

	browseMode = followMode.Copy().
			Background(lipgloss.Color("5")).
			Foreground(lipgloss.Color("0")).
			SetString("BROWSE")

	runningStatus = statusNugget.Copy().
			Background(lipgloss.Color("3")).
			Foreground(lipgloss.Color("0")).
			Align(lipgloss.Right).
			PaddingRight(0).
			SetString("RUNNING")

	completeStatus = runningStatus.Copy().
			Background(lipgloss.Color("6")).
			Foreground(lipgloss.Color("0")).
			Align(lipgloss.Right).
			SetString("COMPLETE")

	statusText = lipgloss.NewStyle().Inherit(statusBarStyle)

	timerStyle = statusNugget.Copy().
			Background(lipgloss.Color("3")).
			Foreground(lipgloss.Color("0"))
)

// tree
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

	itemTimerStyle = lipgloss.NewStyle().
			Foreground(colorFaint)

	selectedStyle = lipgloss.NewStyle().
			Foreground(colorBackground).
			Background(colorSelected).
			Bold(false)

	selectedStyleBlur = lipgloss.NewStyle().
				Background(colorForeground).
				Foreground(colorBackground)

	completedStatus = lipgloss.NewStyle().Foreground(colorCompleted).SetString("✔")
	failedStatus    = lipgloss.NewStyle().Foreground(colorFailed).SetString("✖")
	cachedStatus    = lipgloss.NewStyle().Foreground(colorFaint).SetString("●")
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
			Background(lipgloss.Color("0")).
			Foreground(lipgloss.Color("7"))

	titleBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("0")).
			Foreground(lipgloss.Color("7"))

	infoStyle = lipgloss.NewStyle().
			Border(borderRight).
			Padding(0, 1).
			Background(lipgloss.Color("0")).
			Foreground(lipgloss.Color("7"))
)
