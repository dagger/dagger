package drivers

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dagger/dagger/util/traceexec"
	telemetry "github.com/dagger/otel-go"
)

// runtimeUnavailableError keeps diagnostic details in the trace while the CLI
// displays only the guidance, using its existing quiet-error convention.
type runtimeUnavailableError struct {
	err     error
	message string
}

func (e *runtimeUnavailableError) Error() string {
	return e.message + "\n\nDetails: " + e.err.Error()
}

func (e *runtimeUnavailableError) Unwrap() error { return e.err }

func (e *runtimeUnavailableError) Extensions() map[string]any {
	return map[string]any{"_quiet": true, "_message": e.message}
}

func containerRuntimeAvailable(ctx context.Context, command string, args ...string) (bool, error) {
	if _, err := exec.LookPath(command); err != nil {
		return false, nil //nolint:nilerr
	}

	cmd := exec.CommandContext(ctx, command, args...)
	if err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated()); err != nil {
		if ctx.Err() != nil {
			return false, err
		}
		message := fmt.Sprintf("Dagger could not start the local engine using %s.\n\n"+
			"Make sure the runtime is running and your user can access it.\n"+
			"Run `%s` to diagnose the problem.\n"+
			"To select another engine, see `dagger help engine`.", command, strings.Join(cmd.Args, " "))
		return false, &runtimeUnavailableError{err: err, message: message}
	}
	return true, nil
}
