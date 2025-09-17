//go:build !windows
// +build !windows

package git

import "syscall"

func init() {
	// Reset umask to zero to match buildkitd and to make sure the tests do not rely on standard umask from the host.
	syscall.Umask(0)
}
