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

func dialService(service *plancontext.Service) (net.Conn, error) {
	if service.Npipe == "" {
		return nil, errors.New("unsupported socket type")
	}

	dur := time.Second
	return winio.DialPipe(service.Npipe, &dur)
}
