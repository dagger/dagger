//go:build !unix
// +build !unix

package main

import (
	"github.com/gorilla/websocket"
)

func (ts *terminalSession) listenForResize(wsconn *websocket.Conn) {
}
