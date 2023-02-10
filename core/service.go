package core

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"golang.org/x/sync/errgroup"
)

type Service struct {
	Container *Container
	Detach    func()
}

// WithServices runs the given function with the given services started,
// detaching from each of them after the function completes.
func WithServices[T any](ctx context.Context, gw bkgw.Client, svcs []ContainerID, fn func() (T, error)) (T, error) {
	var zero T

	// NB: don't use errgroup.WithCancel; we don't want to cancel on Wait
	eg := new(errgroup.Group)
	started := make(chan *Service, len(svcs))

	for _, svcID := range svcs {
		svc := &Container{ID: svcID}

		host, err := svc.Hostname()
		if err != nil {
			return zero, err
		}

		eg.Go(func() error {
			svc, err := svc.Start(ctx, gw)
			if err != nil {
				return fmt.Errorf("start %s: %w", host, err)
			}
			started <- svc
			return nil
		})
	}

	startErr := eg.Wait()

	close(started)

	defer func() {
		go func() {
			<-time.After(10 * time.Second)

			for svc := range started {
				svc.Detach()
			}
		}()
	}()

	// wait for all services to start
	if startErr != nil {
		return zero, startErr
	}

	return fn()
}

type portHealthChecker struct {
	gw    bkgw.Client
	host  string
	ports []ContainerPort
}

func newHealth(gw bkgw.Client, host string, ports []ContainerPort) *portHealthChecker {
	return &portHealthChecker{
		gw:    gw,
		host:  host,
		ports: ports,
	}
}

type marshalable interface {
	Marshal(ctx context.Context, co ...llb.ConstraintsOpt) (*llb.Definition, error)
}

func result(ctx context.Context, gw bkgw.Client, st marshalable) (*bkgw.Result, error) {
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	return gw.Solve(ctx, bkgw.SolveRequest{
		Definition: def.ToPB(),
	})
}

func (d *portHealthChecker) Check(ctx context.Context) error {
	scratchRes, err := result(ctx, d.gw, llb.Scratch())
	if err != nil {
		return err
	}

	container, err := d.gw.NewContainer(ctx, bkgw.NewContainerRequest{
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
	cleanupCtx := context.Background()

	defer container.Release(cleanupCtx)

	args := []string{"check", d.host}
	for _, port := range d.ports {
		// TODO(vito): include protocol
		args = append(args, strconv.Itoa(port.Port))
	}

	proc, err := container.Start(cleanupCtx, bkgw.StartRequest{
		Args: args,
		Env:  []string{"_DAGGER_INTERNAL_COMMAND="},
		// TODO(vito): it would be great to make this visible somehow for
		// troubleshooting, but there's no good place to send it. hmm.
		Stdout: os.Stderr,
		Stderr: os.Stderr,
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
