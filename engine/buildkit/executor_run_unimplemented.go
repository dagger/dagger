//go:build darwin || windows

package buildkit

import "context"

func (w *Worker) runContainer(ctx context.Context, state *execState) error {
	panic("implemented only on linux")
}
