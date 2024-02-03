package buildkit

// ExecError is an error that occurred while executing an `Op_Exec`.
type ExecError struct {
	original       error
	Cmd            []string
	ExitCode       int
	CombinedOutput string
}

func (e *ExecError) Error() string {
	return e.original.Error()
}

func (e *ExecError) Unwrap() error {
	return e.original
}

func (e *ExecError) Extensions() map[string]interface{} {
	return map[string]interface{}{
		"_type":          "EXEC_ERROR",
		"cmd":            e.Cmd,
		"exitCode":       e.ExitCode,
		"combinedOutput": e.CombinedOutput,
	}
}
