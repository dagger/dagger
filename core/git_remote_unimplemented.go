//go:build darwin || windows

package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func runWithStandardUmaskAndNetOverride(_ context.Context, _ *exec.Cmd, _, _ string, _ *os.File) error {
	return fmt.Errorf("runWithStandardUmaskAndNetOverride is implemented only on linux")
}
