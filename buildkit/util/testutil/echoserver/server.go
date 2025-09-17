package echoserver

import (
	"io"
	"net"
)

type TestServer interface {
	io.Closer
	Addr() net.Addr
}

func NewTestServer(response string) (TestServer, error) {
	ln, err := net.Listen("tcp", ":") //nolint:gosec // server only used in tests
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				break
			}
			go handleConnection(conn, response)
		}
	}()
	return ln, nil
}

func handleConnection(c net.Conn, response string) {
	c.Write([]byte(response))
	c.Close()
}
