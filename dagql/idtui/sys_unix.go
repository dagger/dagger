//go:build unix

package idtui

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func sigquit() {
	syscall.Kill(syscall.Getpid(), syscall.SIGQUIT)
}

func openInputTTY() (*os.File, error) {
	f, err := os.Open("/dev/tty")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.ENODEV) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not open a new TTY: %w", err)
	}
	return f, nil
}
