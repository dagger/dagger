package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
	"github.com/spf13/cobra"
)

var dialStdioCmd = &cobra.Command{
	Use:    "dial-stdio",
	Run:    DialStdio,
	Hidden: true,
}

func DialStdio(cmd *cobra.Command, args []string) {
	localDirs := getKVInput(localDirsInput)
	startOpts := &engine.Config{
		LocalDirs:  localDirs,
		Workdir:    workdir,
		ConfigPath: configPath,
	}

	err := engine.Start(context.Background(), startOpts, func(ctx context.Context, r *router.Router) error {
		srv := http.Server{
			Handler:           r,
			ReadHeaderTimeout: 30 * time.Second,
		}

		closeCh := make(chan struct{})

		l := &stdioConnListener{
			closeCh: closeCh,
		}

		go func() {
			err := srv.Serve(l)
			if err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "http serve: error: %v\n", err)
			}
		}()

		<-closeCh

		return srv.Shutdown(ctx)
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

var _ net.Listener = &stdioConnListener{}

type stdioConnListener struct {
	once    sync.Once
	closeCh chan struct{}
}

func (l *stdioConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() {
		conn = &stdioConn{
			closeCh: l.closeCh,
		}
	})
	// Return stdio connection ONLY on the first `Accept()`
	// since we can't multiplex on stdio.
	if conn != nil {
		return conn, nil
	}

	// For the other `Accept()`, block until the server is closed
	<-l.closeCh
	return nil, net.ErrClosed
}

func (l *stdioConnListener) Addr() net.Addr {
	return nil
}

func (l *stdioConnListener) Close() error {
	return nil
}

var _ net.Conn = &stdioConn{}

type stdioConn struct {
	closeCh chan struct{}
}

func (c *stdioConn) Read(b []byte) (n int, err error) {
	return os.Stdin.Read(b)
}

func (c *stdioConn) Write(b []byte) (n int, err error) {
	return os.Stdout.Write(b)
}

func (c *stdioConn) Close() error {
	close(c.closeCh)
	return nil
}

func (c *stdioConn) LocalAddr() net.Addr {
	return &net.IPAddr{}
}

func (c *stdioConn) RemoteAddr() net.Addr {
	return &net.IPAddr{}
}

func (c *stdioConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *stdioConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *stdioConn) SetWriteDeadline(t time.Time) error {
	return nil
}
