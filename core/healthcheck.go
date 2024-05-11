package core

import (
	"context"
	"fmt"
	"strings"
	"syscall"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/telemetry"
)

type portHealthChecker struct {
	bk    *buildkit.Client
	host  string
	ports []Port
}

func newHealth(bk *buildkit.Client, host string, ports []Port) *portHealthChecker {
	return &portHealthChecker{
		bk:    bk,
		host:  host,
		ports: ports,
	}
}

func (d *portHealthChecker) Check(ctx context.Context) (rerr error) {
	args := []string{"check", d.host}
	allPortsSkipped := true
	for _, port := range d.ports {
		if !port.ExperimentalSkipHealthcheck {
			args = append(args, fmt.Sprintf("%d/%s", port.Port, port.Protocol.Network()))
			allPortsSkipped = false
		}
	}
	if allPortsSkipped {
		return nil
	}

	// always show health checks
	ctx, span := Tracer().Start(ctx, strings.Join(args, " "))
	defer telemetry.End(span, func() error { return rerr })
	ctx, stdout, stderr := telemetry.WithStdioToOtel(ctx, InstrumentationLibrary)

	scratchDef, err := llb.Scratch().Marshal(ctx)
	if err != nil {
		return err
	}

	scratchRes, err := d.bk.Solve(ctx, bkgw.SolveRequest{
		Definition: scratchDef.ToPB(),
	})
	if err != nil {
		return err
	}

	container, err := d.bk.NewContainer(ctx, buildkit.NewContainerRequest{
		Mounts: []bkgw.Mount{
			{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       scratchRes.Ref,
			},
		},
	})
	if err != nil {
		return err
	}

	// NB: use a different ctx than the one that'll be interrupted for anything
	// that needs to run as part of post-interruption cleanup
	cleanupCtx := context.WithoutCancel(ctx)

	defer container.Release(cleanupCtx)

	proc, err := container.Start(ctx, bkgw.StartRequest{
		Args:   args,
		Env:    append(telemetry.PropagationEnv(ctx), "_DAGGER_INTERNAL_COMMAND="),
		Stdout: nopCloser{stdout},
		Stderr: nopCloser{stderr},
	})
	if err != nil {
		return err
	}

	exited := make(chan error, 1)
	go func() {
		exited <- proc.Wait()
	}()

	select {
	case err := <-exited:
		if err != nil {
			return err
		}

		return nil
	case <-ctx.Done():
		err := proc.Signal(cleanupCtx, syscall.SIGKILL)
		if err != nil {
			return fmt.Errorf("interrupt check: %w", err)
		}

		<-exited

		return ctx.Err()
	}
}
