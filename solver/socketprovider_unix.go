//go:build !windows
// +build !windows

package solver

import (
	"errors"
	"net"
	"time"

	"go.dagger.io/dagger/plancontext"
)

func dialService(service *plancontext.Service) (net.Conn, error) {
	if service.Unix() == "" {
		return nil, errors.New("unsupported socket type")
	}

	return net.DialTimeout("unix", service.Unix(), time.Second)
}
