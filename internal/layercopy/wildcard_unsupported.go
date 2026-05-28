//go:build !linux

package layercopy

import (
	"fmt"
	"runtime"
)

func ResolveWildcards(string, string, bool) ([]string, error) {
	return nil, fmt.Errorf("layercopy wildcard resolution is only implemented on linux, not %s", runtime.GOOS)
}
