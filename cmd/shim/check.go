package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/moby/buildkit/identity"
)

func check(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: check HOST PORT[/NETWORK] [PORT[/NETWORK] ...]")
	}

	logPrefix := fmt.Sprintf("[check %s]", identity.NewID())

	host, ports := args[0], args[1:]

	for _, port := range ports {
		port, network, ok := strings.Cut(port, "/")
		if !ok {
			network = "tcp"
		}

		pollAddr := net.JoinHostPort(host, port)

		fmt.Println(logPrefix, "polling for port", pollAddr)

		reached, err := pollForPort(logPrefix, network, pollAddr)
		if err != nil {
			return fmt.Errorf("poll %s: %w", pollAddr, err)
		}

		fmt.Println(logPrefix, "port is up at", reached)
	}

	return nil
}

func pollForPort(logPrefix, network, addr string) (string, error) {
	retry := backoff.NewExponentialBackOff()
	retry.InitialInterval = 100 * time.Millisecond

	dialer := net.Dialer{
		Timeout: time.Second,
	}

	var reached string
	err := backoff.Retry(func() error {
		// NB(vito): it's a _little_ silly to dial a UDP network to see that it's
		// up, since it'll be a false positive even if they're not listening yet,
		// but it at least checks that we're able to resolve the container address.

		conn, err := dialer.Dial(network, addr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s port not ready: %s; elapsed: %s\n", logPrefix, err, retry.GetElapsedTime())
			return err
		}

		reached = conn.RemoteAddr().String()

		_ = conn.Close()

		return nil
	}, retry)
	if err != nil {
		return "", err
	}

	return reached, nil
}
