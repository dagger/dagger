//go:build darwin || windows

package core

import (
	"context"
	"fmt"
)

func mountIntoContainer(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("mountIntoContainer is implemented only on linux")
}
