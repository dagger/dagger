package idtui

import "github.com/muesli/termenv"

const (
	Block0125           = "▏"
	Block0250           = "▎"
	Block0375           = "▍"
	Block0500           = "▌"
	Block0500Right      = "▐"
	Block0625           = "▋"
	Block0750           = "▊"
	Block0875           = "▉"
	Block               = "█"
	BlockFade75         = "▓"
	BlockFade50         = "▒"
	BlockFade25         = "░"
	BorderLeft          = "▕"
	BorderLeftHalf      = Block0500Right
	CaretDownEmpty      = "▽"
	CaretDownFilled     = "▼"
	CaretLeftFilled     = "◀" // "<"
	CaretRightEmpty     = "▷" // ">"
	CaretRightFilled    = "▶" // ">"
	CornerBottomLeft    = "╰"
	CornerBottoRight    = "╯"
	CornerTopLeft       = "╭"
	CornerTopRight      = "╮"
	CrossBar            = "┼"
	DotEmpty            = "○"
	DotHalf             = "◐"
	DotFilled           = "●"
	DotCenter           = "◉"
	DotTiny             = "·"
	HorizBar            = "─"
	HorizBottomBar      = "┬"
	HorizHalfLeftBar    = "╴"
	HorizHalfRightBar   = "╶"
	HorizTopBar         = "┴"
	HorizTopBoldBar     = "┻"
	InactiveGroupSymbol = VertBar
	TaskSymbol          = VertRightBoldBar
	VertBar             = "│"
	VertBoldBar         = "┃"
	VertDash2           = "╎"
	VertDash3           = "┆"
	VertBoldDash3       = "┇"
	VertDash4           = "┊"
	VertBoldDash4       = "┋"
	VertLeftBar         = "┤"
	VertLeftBoldBar     = "┫"
	VertRightBar        = "├"
	VertRightBoldBar    = "┣"
	IconSkipped         = "∅"
	IconSuccess         = "✔"
	IconFailure         = "✘"
	IconCached          = "$" // cache money
	Diamond             = "◆"
	LLMPrompt           = "❯"
	CloudIcon           = "⬢"
	CogIcon             = "⚙"

	// We need a prompt that conveys the unique nature of the Dagger shell. Per gpt4:
	// The ⋈ symbol, known as the bowtie, has deep roots in relational databases and set theory,
	// where it denotes a join operation. This makes it especially fitting for a DAG environment,
	// as it suggests the idea of dependencies, intersections, and points where separate paths
	// or data sets come together.
	ShellPrompt = "⋈"
)

type Prefix struct {
	Symbol string
	Fg, Bg termenv.Color
}

func (p Prefix) Style(out TermOutput) termenv.Style {
	st := out.String(p.Symbol).Foreground(p.Fg)
	if p.Bg != nil {
		st = st.Background(p.Bg)
	}
	return st
}

var LogsPrefix = Prefix{
	Symbol: VertBoldBar,
	Fg:     termenv.ANSIBrightBlack,
}

var LLMUserPrefix = Prefix{
	Symbol: Block,
	Fg:     termenv.ANSIBrightBlue,
}

var LLMThinkingPrefix = Prefix{
	Symbol: VertBoldDash3,
	Fg:     termenv.ANSIBrightBlack,
}

var LLMResponsePrefix = Prefix{
	Symbol: VertBoldBar,
	Fg:     termenv.ANSIBrightBlack,
}

var LLMToolPrefix = Prefix{
	Symbol: CogIcon,
	Fg:     termenv.ANSIBrightBlack,
}
