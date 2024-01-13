package core

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
)

type c2hTunnel struct {
	bk                 *buildkit.Client
	upstreamHost       string
	tunnelServiceHost  string
	tunnelServicePorts []PortForward
}

func (d *c2hTunnel) Tunnel(ctx context.Context) (err error) {
	rec := progrock.FromContext(ctx)

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

	mounts := []bkgw.Mount{
		{
			Dest:      "/",
			MountType: pb.MountType_BIND,
			Ref:       scratchRes.Ref,
		},
	}

	args := []string{"tunnel"}

	for _, port := range d.tunnelServicePorts {
		var frontend int
		if port.Frontend != nil {
			frontend = *port.Frontend
		} else {
			frontend = port.Backend
		}

		upstream := NewHostIPSocket(
			port.Protocol.Network(),
			fmt.Sprintf("%s:%d", d.upstreamHost, port.Backend),
		)

		sockPath := fmt.Sprintf("/upstream.%d.sock", frontend)

		mounts = append(mounts, bkgw.Mount{
			Dest:      sockPath,
			MountType: pb.MountType_SSH,
			SSHOpt: &pb.SSHOpt{
				ID: upstream.SSHID(),
			},
		})

		args = append(args, fmt.Sprintf(
			"%s:%d/%s",
			sockPath,
			frontend,
			port.Protocol.Network(),
		))
	}

	vtx := rec.Vertex(
		digest.Digest(identity.NewID()),
		strings.Join(args, " "),
		// progrock.Internal(),
	)
	defer func() {
		vtx.Done(err)
	}()

	container, err := d.bk.NewContainer(ctx, bkgw.NewContainerRequest{
		Hostname: d.tunnelServiceHost,
		Mounts:   mounts,
	})
	if err != nil {
		return err
	}

	// NB: use a different ctx than the one that'll be interrupted for anything
	// that needs to run as part of post-interruption cleanup
	//
	// set a reasonable timeout on this since there have been funky hangs in the
	// past
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()

	defer container.Release(cleanupCtx)

	proc, err := container.Start(ctx, bkgw.StartRequest{
		Args:   args,
		Env:    []string{"_DAGGER_INTERNAL_COMMAND="},
		Stdout: nopCloser{vtx.Stdout()},
		Stderr: nopCloser{vtx.Stderr()},
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
