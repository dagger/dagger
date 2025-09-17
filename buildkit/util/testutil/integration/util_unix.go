//go:build !windows
// +build !windows

package integration

import (
	"net"

	"github.com/pkg/errors"
)

var socketScheme = "unix://"

// abstracted function to handle pipe dialing on unix.
// some simplification has been made to discard
// laddr for unix -- left as nil.
func dialPipe(address string) (net.Conn, error) {
	addr, err := net.ResolveUnixAddr("unix", address)
	if err != nil {
		return nil, errors.Wrapf(err, "failed resolving unix addr: %s", address)
	}
	return net.DialUnix("unix", nil, addr)
}
