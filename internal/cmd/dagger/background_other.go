//go:build !unix

package daggercmd

import (
	"errors"
	"os"
)

func startBackground([]string) (*os.File, string, error) {
	return nil, "", errors.New("--detach is not supported on this platform")
}
