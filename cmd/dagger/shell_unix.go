//go:build unix
// +build unix

package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/gorilla/websocket"
)

func (ts *terminalSession) listenForResize(wsconn *websocket.Conn) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	defer signal.Stop(sig)
	for range sig {
		ts.sendSize(wsconn)
	}
}
