package main

import (
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var dialStdioCommand = cli.Command{
	Name:   "dial-stdio",
	Usage:  "Proxy the stdio stream to the daemon connection. Should not be invoked manually.",
	Hidden: true,
	Action: dialStdioAction,
}

func dialStdioAction(clicontext *cli.Context) error {
	addr := clicontext.GlobalString("addr")
	timeout := time.Duration(clicontext.GlobalInt("timeout")) * time.Second
	conn, err := dialer(addr, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	var connHalfCloser halfCloser
	switch t := conn.(type) {
	case halfCloser:
		connHalfCloser = t
	case halfReadWriteCloser:
		connHalfCloser = &nopCloseReader{t}
	default:
		return errors.New("the raw stream connection does not implement halfCloser")
	}

	stdin2conn := make(chan error, 1)
	conn2stdout := make(chan error, 1)
	go func() {
		stdin2conn <- copier(connHalfCloser, &halfReadCloserWrapper{os.Stdin}, "stdin to stream")
	}()
	go func() {
		conn2stdout <- copier(&halfWriteCloserWrapper{os.Stdout}, connHalfCloser, "stream to stdout")
	}()
	select {
	case err = <-stdin2conn:
		if err != nil {
			return err
		}
		// wait for stdout
		err = <-conn2stdout
	case err = <-conn2stdout:
		// return immediately without waiting for stdin to be closed.
		// (stdin is never closed when tty)
	}
	return err
}

func dialer(address string, timeout time.Duration) (net.Conn, error) {
	addrParts := strings.SplitN(address, "://", 2)
	if len(addrParts) != 2 {
		return nil, errors.Errorf("invalid address %s", address)
	}
	if addrParts[0] != "unix" {
		return nil, errors.Errorf("invalid address %s (expected unix://, got %s://)", address, addrParts[0])
	}
	return net.DialTimeout(addrParts[0], addrParts[1], timeout)
}

func copier(to halfWriteCloser, from halfReadCloser, debugDescription string) error {
	defer func() {
		if err := from.CloseRead(); err != nil {
			bklog.L.Errorf("error while CloseRead (%s): %v", debugDescription, err)
		}
		if err := to.CloseWrite(); err != nil {
			bklog.L.Errorf("error while CloseWrite (%s): %v", debugDescription, err)
		}
	}()
	if _, err := io.Copy(to, from); err != nil {
		return errors.Wrapf(err, "error while Copy (%s)", debugDescription)
	}
	return nil
}

type halfReadCloser interface {
	io.Reader
	CloseRead() error
}

type halfWriteCloser interface {
	io.Writer
	CloseWrite() error
}

type halfCloser interface {
	halfReadCloser
	halfWriteCloser
}

type halfReadWriteCloser interface {
	io.Reader
	halfWriteCloser
}

type nopCloseReader struct {
	halfReadWriteCloser
}

func (x *nopCloseReader) CloseRead() error {
	return nil
}

type halfReadCloserWrapper struct {
	io.ReadCloser
}

func (x *halfReadCloserWrapper) CloseRead() error {
	return x.Close()
}

type halfWriteCloserWrapper struct {
	io.WriteCloser
}

func (x *halfWriteCloserWrapper) CloseWrite() error {
	return x.Close()
}
