// The external Go SDK's fixture for the golden tests: every SDK fixture has
// the same functions, so their goldens line up.
package viztest_go

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"dagger.io/dagger/core"

	sdkdep "dagger/viztest-go/internal/dagger/clients/sdk-dep"
)

type ViztestGo struct{}

// LogSuccess logs to stdout and stderr, and succeeds.
// +cache="session"
func (*ViztestGo) LogSuccess() string {
	logBoth()
	return "go function result"
}

// FailNative logs to stdout and stderr, then returns an error of its own.
// +cache="session"
func (*ViztestGo) FailNative() error {
	logBoth()
	return errors.New("go function failed natively")
}

// FailExitCode logs to stdout and stderr, then exits the process with a
// custom code, without returning an error.
// +cache="session"
func (*ViztestGo) FailExitCode() {
	logBoth()
	os.Exit(3)
}

// logBoth logs a line to stdout, then one to stderr. The streams are
// captured apart, so the pause keeps their order stable in the goldens.
func logBoth() {
	fmt.Println("go function stdout")
	time.Sleep(100 * time.Millisecond)
	fmt.Fprintln(os.Stderr, "go function stderr")
}

// FailExec runs a command that fails, and returns its error as is.
// +cache="session"
func (*ViztestGo) FailExec(ctx context.Context) (string, error) {
	return failingExec().Stdout(ctx)
}

// FailExecWrapped runs a command that fails, and wraps its error.
// +cache="session"
func (*ViztestGo) FailExecWrapped(ctx context.Context) (string, error) {
	out, err := failingExec().Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("go function ran a failing command: %w", err)
	}
	return out, nil
}

// CallFailingDep calls a function of a dependency whose command fails.
// +cache="session"
func (*ViztestGo) CallFailingDep(ctx context.Context) (string, error) {
	return sdkdep.New().FailExec(ctx)
}

// CallFailingDepLogged logs a line, then calls a function of a dependency
// whose command fails.
// +cache="session"
func (*ViztestGo) CallFailingDepLogged(ctx context.Context) (string, error) {
	fmt.Println("go function stdout")
	return sdkdep.New().FailExec(ctx)
}

func failingExec() *core.Container {
	return core.NewContainer().
		From("alpine").
		WithExec([]string{"sh", "-c", "echo exec stdout; sleep 0.1; echo exec stderr >&2; exit 1"})
}
