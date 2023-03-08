package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"golang.org/x/sync/errgroup"
)

func proxy(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: proxy HOST PORT[/NETWORK]")
	}

	host, portNetwork := args[0], args[1]

	port, network, ok := strings.Cut(portNetwork, "/")
	if !ok {
		network = "tcp"
	}

	addr := net.JoinHostPort(host, port)

	conn, err := net.Dial(network, addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}

	eg := new(errgroup.Group)

	eg.Go(func() error {
		_, err := io.Copy(os.Stdout, conn)
		return err
	})

	eg.Go(func() error {
		// NB: if os.Stdin closes that means the upstream connection has gone away,
		// so interrupt the other side
		defer conn.Close()

		_, err := io.Copy(conn, os.Stdin)
		return err
	})

	return eg.Wait()
}
