// A dependency of every SDK fixture, for errors that cross a module boundary.
package sdk_dep

import (
	"context"

	"dagger.io/dagger/core"
)

type SdkDep struct{}

// FailExec runs a command that logs to stdout and stderr, then fails.
// +cache="session"
func (*SdkDep) FailExec(ctx context.Context) (string, error) {
	return core.NewContainer().
		From("alpine").
		WithExec([]string{"sh", "-c", "echo dep exec stdout; sleep 0.1; echo dep exec stderr >&2; exit 1"}).
		Stdout(ctx)
}
