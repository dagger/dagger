package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/telemetry"
)

type portHealthChecker struct {
	bk        *buildkit.Client
	ns        buildkit.Namespaced
	host      string
	ports     []Port
	logWriter io.Writer
}

func newHealth(bk *buildkit.Client, ns buildkit.Namespaced, host string, ports []Port, logWriter io.Writer) *portHealthChecker {
	return &portHealthChecker{
		bk:        bk,
		ns:        ns,
		host:      host,
		ports:     ports,
		logWriter: logWriter,
	}
}

func (d *portHealthChecker) Check(ctx context.Context) (rerr error) {
	ports := make([]Port, 0, len(d.ports))
	portStrs := make([]string, 0, len(d.ports))
	for _, port := range d.ports {
		if !port.ExperimentalSkipHealthcheck {
			ports = append(ports, port)
			portStrs = append(portStrs, fmt.Sprintf("%d/%s", port.Port, port.Protocol.Network()))
		}
	}
	if len(ports) == 0 {
		return nil
	}

	// always show health checks
	ctx, span := Tracer().Start(ctx, strings.Join(portStrs, " "))
	defer telemetry.End(span, func() error { return rerr })

	dialer := net.Dialer{
		Timeout: time.Second,
	}

	for _, port := range ports {
		retry := backoff.NewExponentialBackOff(backoff.WithInitialInterval(100 * time.Millisecond))
		endpoint, err := backoff.RetryWithData(func() (string, error) {
			return buildkit.RunInNamespace(ctx, d.bk, d.ns, func() (string, error) {
				// NB(vito): it's a _little_ silly to dial a UDP network to see that it's
				// up, since it'll be a false positive even if they're not listening yet,
				// but it at least checks that we're able to resolve the container address.
				conn, err := dialer.Dial(
					port.Protocol.Network(),
					net.JoinHostPort(d.host, fmt.Sprintf("%d", port.Port)),
				)
				if err != nil {
					fmt.Fprintf(d.logWriter, "port not ready: %v, elapsed: %s\n", err, retry.GetElapsedTime())
					return "", err
				}

				endpoint := conn.RemoteAddr().String()
				_ = conn.Close()
				return endpoint, nil
			})
		}, backoff.WithContext(retry, ctx))
		if err != nil {
			return fmt.Errorf("checking for port %d/%s: %w", port.Port, port.Protocol.Network(), err)
		}

		fmt.Fprintf(d.logWriter, "port is up: %s\n", endpoint)
	}

	return nil
}
