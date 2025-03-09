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
	CornerBottomRight   = "╯"
	CornerTopLeft       = "╭"
	CornerTopRight      = "╮"
	CrossBar            = "┼"
	DotEmpty            = "○"
	DotFilled           = "●"
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
	VertDottedBar       = "┊" // ┊┆┇┋╎
	VertLeftBar         = "┤"
	VertLeftBoldBar     = "┫"
	VertRightBar        = "├"
	VertRightBoldBar    = "┣"
	IconSkipped         = "∅"
	IconSuccess         = "✔"
	IconFailure         = "✘"
	IconCached          = "$" // cache money
	Diamond             = "◆"
	LLMPrompt           = "✱"

	// We need a prompt that conveys the unique nature of the Dagger shell. Per gpt4:
	// The ⋈ symbol, known as the bowtie, has deep roots in relational databases and set theory,
	// where it denotes a join operation. This makes it especially fitting for a DAG environment,
	// as it suggests the idea of dependencies, intersections, and points where separate paths
	// or data sets come together.
	ShellPrompt = "⋈"
)
