package idtui

const (
	Block               = "█"
	Block75             = "▓"
	Block50             = "▒"
	Block25             = "░"
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
	BorderLeft          = "▕"

	// We need a prompt that conveys the unique nature of the Dagger shell. Per gpt4:
	// The ⋈ symbol, known as the bowtie, has deep roots in relational databases and set theory,
	// where it denotes a join operation. This makes it especially fitting for a DAG environment,
	// as it suggests the idea of dependencies, intersections, and points where separate paths
	// or data sets come together.
	ShellPrompt = "⋈"
)
