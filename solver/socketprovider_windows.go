//go:build windows
// +build windows

package solver

import (
	"errors"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"go.dagger.io/dagger/plancontext"
)

func dialSocket(socket *plancontext.Socket) (net.Conn, error) {
	if socket.NPipe() == "" {
		return nil, errors.New("unsupported socket type")
	}

	dur := time.Second
	return winio.DialPipe(socket.NPipe(), &dur)
}
