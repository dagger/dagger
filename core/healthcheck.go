package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/executor"
	gwpb "github.com/dagger/dagger/internal/buildkit/frontend/gateway/pb"
	telemetry "github.com/dagger/otel-go"
)

type portHealthChecker struct {
	bk    *engineutil.Client
	ns    engineutil.Namespaced
	host  string
	ports []Port
}

func newPortHealth(bk *engineutil.Client, ns engineutil.Namespaced, host string, ports []Port) *portHealthChecker {
	return &portHealthChecker{
		bk:    bk,
		ns:    ns,
		host:  host,
		ports: ports,
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
	ctx, span := Tracer(ctx).Start(ctx, strings.Join(portStrs, " "))
	defer telemetry.EndWithCause(span, &rerr)

	slog := slog.SpanLogger(ctx, InstrumentationLibrary).With("host", d.host)

	dialer := net.Dialer{
		Timeout: time.Second,
	}

	for _, port := range ports {
		retry := backoff.NewExponentialBackOff(
			backoff.WithInitialInterval(100*time.Millisecond),
			backoff.WithMaxInterval(10*time.Second),
		)
		endpoint, err := backoff.RetryWithData(func() (string, error) {
			return engineutil.RunInNetNS(ctx, d.bk, d.ns, func() (string, error) {
				// NB(vito): it's a _little_ silly to dial a UDP network to see that it's
				// up, since it'll be a false positive even if they're not listening yet,
				// but it at least checks that we're able to resolve the container address.
				conn, err := dialer.Dial(
					port.Protocol.Network(),
					net.JoinHostPort(d.host, fmt.Sprintf("%d", port.Port)),
				)
				if err != nil {
					slog.Warn("port not ready", "error", err, "elapsed", retry.GetElapsedTime())
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

		slog.Info("port is healthy", "endpoint", endpoint)
	}

	return nil
}

type dockerHealthcheck struct {
	args    []string
	creator trace.SpanContext
	ctr     *Container
	exec    executor.Executor
	svcID   string
}

func newDockerHealthcheck(exec executor.Executor, svcID string, ctr *Container, creator trace.SpanContext) (*dockerHealthcheck, error) {
	if ctr == nil || ctr.Config.Healthcheck == nil || len(ctr.Config.Healthcheck.Test) == 0 || ctr.Config.Healthcheck.Test[0] == "NONE" {
		return nil, fmt.Errorf("container does not have a healthcheck command")
	}

	var args []string
	switch ctr.Config.Healthcheck.Test[0] {
	case "CMD":
		if len(ctr.Config.Healthcheck.Test) < 2 {
			return nil, fmt.Errorf("healthcheck command should have at least 2 elements: %v", ctr.Config.Healthcheck.Test)
		}
		args = ctr.Config.Healthcheck.Test[1:]
	case "CMD-SHELL":
		if len(ctr.Config.Healthcheck.Test) != 2 {
			return nil, fmt.Errorf("healthcheck shell command should have exactly 2 elements: %v", ctr.Config.Healthcheck.Test)
		}
		if len(ctr.Config.Shell) > 0 {
			args = append([]string{}, ctr.Config.Shell...)
		} else {
			args = []string{"/bin/sh", "-c"}
		}
		args = append(args, ctr.Config.Healthcheck.Test[1:]...)
	default:
		return nil, fmt.Errorf("malformed healthcheck command: %v", ctr.Config.Healthcheck.Test)
	}

	return &dockerHealthcheck{
		args:    args,
		creator: creator,
		ctr:     ctr,
		exec:    exec,
		svcID:   svcID,
	}, nil
}

func (chk *dockerHealthcheck) durationBetweenRetries() time.Duration {
	if chk.ctr.Config.Healthcheck.StartInterval > 0 {
		return chk.ctr.Config.Healthcheck.StartInterval
	}
	if chk.ctr.Config.Healthcheck.Interval > 0 {
		return chk.ctr.Config.Healthcheck.Interval
	}
	return time.Second * 5
}

func (chk *dockerHealthcheck) Check(ctx context.Context) error {
	sleepDuration := chk.durationBetweenRetries()

	allowFailuresUntil := time.Now()
	if chk.ctr.Config.Healthcheck.StartPeriod > 0 {
		allowFailuresUntil = allowFailuresUntil.Add(chk.ctr.Config.Healthcheck.StartPeriod)
	}

	numRetries := 3
	if chk.ctr.Config.Healthcheck.Retries > 0 {
		numRetries = chk.ctr.Config.Healthcheck.Retries
	}
	var numFailures int
	for {
		err := chk.check(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(allowFailuresUntil) {
			numFailures++
		}
		if numFailures == numRetries {
			return err
		}
		// sleep before retrying
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepDuration):
			break
		}
	}
}

func (chk *dockerHealthcheck) check(ctx context.Context) error {
	healthcheckMeta, err := chk.ctr.metaSpec(ctx, ContainerExecOpts{
		Args: chk.args,
	})
	if err != nil {
		return err
	}

	stdoutBuf := new(strings.Builder)
	stderrBuf := new(strings.Builder)
	// buffer stdout/stderr so we can return a nice error
	outBufWC := discardOnClose(stdoutBuf)
	errBufWC := discardOnClose(stderrBuf)
	// stop buffering service logs once it's started
	defer outBufWC.Close()
	defer errBufWC.Close()

	err = chk.exec.Exec(ctx, chk.svcID, executor.ProcessInfo{
		Meta:   *healthcheckMeta,
		Stdout: outBufWC,
		Stderr: errBufWC,
	})
	if err != nil {
		var gwErr *gwpb.ExitError
		if errors.As(err, &gwErr) {
			return &ExecError{
				Err:      telemetry.TrackOrigin(gwErr, chk.creator),
				Cmd:      healthcheckMeta.Args,
				ExitCode: int(gwErr.ExitCode),
				Stdout:   stdoutBuf.String(),
				Stderr:   stderrBuf.String(),
			}
		}
		return err
	}
	return nil
}
