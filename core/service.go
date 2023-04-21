package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
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

type ServiceBindings map[ContainerID]AliasSet

type AliasSet []string

func (set AliasSet) String() string {
	if len(set) == 0 {
		return "no aliases"
	}

	return fmt.Sprintf("aliased as %s", strings.Join(set, ", "))
}

func (set AliasSet) With(alias string) AliasSet {
	for _, a := range set {
		if a == alias {
			return set
		}
	}
	return append(set, alias)
}

func (bndp *ServiceBindings) Merge(other ServiceBindings) {
	if *bndp == nil {
		*bndp = ServiceBindings{}
	}

	bnd := *bndp

	for id, aliases := range other {
		if len(bnd[id]) == 0 {
			bnd[id] = aliases
		} else {
			for _, alias := range aliases {
				bnd[id] = bnd[id].With(alias)
			}
		}
	}
}

var debugHealthchecks bool

func init() {
	if os.Getenv("_DAGGER_DEBUG_HEALTHCHECKS") != "" {
		debugHealthchecks = true
	}
}

// NetworkProtocol is a string deriving from NetworkProtocol enum
type NetworkProtocol string

const (
	NetworkProtocolTCP NetworkProtocol = "TCP"
	NetworkProtocolUDP NetworkProtocol = "UDP"
)

// Network returns the value appropriate for the "network" argument to Go
// net.Dial, and for appending to the port number to form the key for the
// ExposedPorts object in the OCI image config.
func (p NetworkProtocol) Network() string {
	return strings.ToLower(string(p))
}

// WithServices runs the given function with the given services started,
// detaching from each of them after the function completes.
func WithServices[T any](ctx context.Context, gw bkgw.Client, svcs ServiceBindings, fn func() (T, error)) (T, error) {
	var zero T

	// NB: don't use errgroup.WithCancel; we don't want to cancel on Wait
	eg := new(errgroup.Group)
	started := make(chan *Service, len(svcs))

	for svcID, aliases := range svcs {
		svc, err := svcID.ToContainer()
		if err != nil {
			return zero, err
		}

		host, err := svc.HostnameOrErr()
		if err != nil {
			return zero, err
		}

		aliases := aliases
		eg.Go(func() error {
			svc, err := svc.Start(ctx, gw)
			if err != nil {
				return fmt.Errorf("start %s (%s): %w", host, aliases, err)
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
		args = append(args, fmt.Sprintf("%d/%s", port.Port, port.Protocol.Network()))
	}

	var debugW io.WriteCloser
	if debugHealthchecks {
		debugW = os.Stderr
	}

	proc, err := container.Start(ctx, bkgw.StartRequest{
		Args: args,
		Env:  []string{"_DAGGER_INTERNAL_COMMAND="},
		// FIXME(vito): it would be great to send these to the progress stream
		// somehow instead
		Stdout: debugW,
		Stderr: debugW,
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
