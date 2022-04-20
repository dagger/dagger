//go:build !windows
// +build !windows

package solver

import (
	"errors"
	"net"
	"time"

	"go.dagger.io/dagger/plancontext"
)

func dialSocket(socket *plancontext.Socket) (net.Conn, error) {
	if socket.Unix() == "" {
		return nil, errors.New("unsupported socket type")
	}

	return net.DialTimeout("unix", socket.Unix(), time.Second)
}
