package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var (
	addr           string
	timeoutSeconds int
)

func init() {
	syscall.Umask(0)

	rootCmd.PersistentFlags().StringVar(&addr, "addr", engine.DefaultEngineSockAddr, "The address serving the grpc api")
	rootCmd.PersistentFlags().IntVar(&timeoutSeconds, "timeout", 5, "The timeout in seconds for connecting to the grpc api")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:  "dial-stdio",
	Long: "Proxy the stdio stream to the daemon connection. Should not be invoked manually.",
	RunE: dialStdio,
}

func dialStdio(cmd *cobra.Command, args []string) error {
	timeout := time.Duration(timeoutSeconds) * time.Second
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

// dialRetryInterval is how often dialer tries again while the engine starts.
const dialRetryInterval = 10 * time.Millisecond

// dialer connects to the engine's socket. A client that starts the engine
// container dials as soon as the container runs, before the engine listens:
// the socket is missing then, or left from an earlier run with nobody
// listening. So it tries again until timeout, and the client is answered as
// soon as the engine listens, rather than failing and retrying on its own
// schedule, which waits a second or more between attempts.
func dialer(address string, timeout time.Duration) (net.Conn, error) {
	addrParts := strings.SplitN(address, "://", 2)
	if len(addrParts) != 2 {
		return nil, errors.Errorf("invalid address %s", address)
	}
	if addrParts[0] != "unix" {
		return nil, errors.Errorf("invalid address %s (expected unix://, got %s://)", address, addrParts[0])
	}
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout(addrParts[0], addrParts[1], timeout)
		if err == nil || !notListening(err) || time.Now().Add(dialRetryInterval).After(deadline) {
			return conn, err
		}
		time.Sleep(dialRetryInterval)
	}
}

// notListening reports whether a dial failed because nothing listens on the
// socket yet.
func notListening(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

func copier(to halfWriteCloser, from halfReadCloser, debugDescription string) error {
	defer func() {
		if err := from.CloseRead(); err != nil {
			fmt.Printf("error while CloseRead (%s): %v\n", debugDescription, err)
		}
		if err := to.CloseWrite(); err != nil {
			fmt.Printf("error while CloseWrite (%s): %v\n", debugDescription, err)
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
