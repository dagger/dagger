//go:build windows

package main

import (
	"net"

	"github.com/Microsoft/go-winio"
)

func createListener(sessionId string) (net.Listener, string, func() error, error) {
	pipeName := `\\.\pipe\dagger-session-` + sessionId
	l, err := winio.ListenPipe(pipeName, nil)
	if err != nil {
		return nil, "", nil, err
	}
	return l, pipeName, func() error {
		return nil
	}, nil
}
