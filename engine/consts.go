package engine

const (
	StdinPrefix  = "\x00,"
	StdoutPrefix = "\x01,"
	StderrPrefix = "\x02,"
	ResizePrefix = "resize,"
	ExitPrefix   = "exit,"
)
