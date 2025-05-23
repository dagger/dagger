package buildkit

import (
	bkexecutor "github.com/moby/buildkit/executor"
	"github.com/moby/buildkit/solver/llbsolver/errdefs"
	bksolverpb "github.com/moby/buildkit/solver/pb"
)

// ExecError is an error that occurred while executing an `Op_Exec`.
type ExecError struct {
	original error
	Cmd      []string
	ExitCode int
	Stdout   string
	Stderr   string
}

func (e *ExecError) Error() string {
	return e.original.Error()
}

func (e *ExecError) Unwrap() error {
	return e.original
}

func (e *ExecError) Extensions() map[string]any {
	return map[string]any{
		"_type":    "EXEC_ERROR",
		"cmd":      e.Cmd,
		"exitCode": e.ExitCode,
		"stdout":   e.Stdout,
		"stderr":   e.Stderr,
	}
}

// InteractiveError will be returned when an error is encountered when evaluating an op.
//
// TODO: handle non-exec errors here too
type InteractiveError struct {
	*errdefs.ExecError
	Mounts []*bksolverpb.Mount

	// optional info for the execution that failed
	ExecMD    *ExecutionMetadata
	Meta      *bkexecutor.Meta
	Secretenv []*bksolverpb.SecretEnv // XXX: hmm hmm
}

func (e InteractiveError) Unwrap() error {
	return e.ExecError
}
