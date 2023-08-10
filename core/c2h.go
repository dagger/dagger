package core

import (
	"context"
	"fmt"
	"strings"
	"syscall"

	"github.com/dagger/dagger/core/socket"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
)

type c2hTunnel struct {
	bk                *buildkit.Client
	upstreamAddr      string
	tunnelServiceHost string
	tunnelServicePort int
	protocol          NetworkProtocol
}

func (d *c2hTunnel) Tunnel(ctx context.Context) (err error) {
	rec := progrock.RecorderFromContext(ctx)

	args := []string{
		"tunnel",
		fmt.Sprintf("%d/%s", d.tunnelServicePort, d.protocol.Network()),
	}

	vtx := rec.Vertex(
		digest.Digest(identity.NewID()),
		strings.Join(args, " "),
		progrock.Internal(),
	)
	defer func() {
		vtx.Done(err)
	}()

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

	// TODO(vito): make sure client scoping is correct
	upstream := socket.NewHostIPSocket(d.protocol.Network(), d.upstreamAddr)
	upstreamID, err := upstream.ID()
	if err != nil {
		return err
	}

	container, err := d.bk.NewContainer(ctx, bkgw.NewContainerRequest{
		Hostname: d.tunnelServiceHost,
		Mounts: []bkgw.Mount{
			{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       scratchRes.Ref,
			},
			{
				Dest:      "/upstream.sock",
				MountType: pb.MountType_SSH,
				SSHOpt: &pb.SSHOpt{
					ID: upstreamID.String(),
				},
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
