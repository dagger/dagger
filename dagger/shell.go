package dagger

import (
	"context"
	"os"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/solver/pb"
	"golang.org/x/crypto/ssh/terminal"
)

// Starts an alpine shell with "fs" mounted at /output
func Shell(ctx *Context, fs FS) error {
	c, err := bkclient.New(ctx.ctx, "docker-container://dagger-buildkitd", bkclient.WithFailFast())
	if err != nil {
		return err
	}

	socketProvider := newAPISocketProvider()
	secretProvider := newSecretProvider()
	attachables := []session.Attachable{socketProvider, secretsprovider.NewSecretProvider(secretProvider)}

	_, err = c.Build(ctx.ctx, bkclient.SolveOpt{Session: attachables}, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		api := newAPIServer(c, gw)
		socketProvider.api = api // TODO: less ugly way of setting this
		dctx := &Context{
			ctx:    ctx,
			client: gw,
		}

		baseDef, err := llb.Image("alpine:3.15").Marshal(ctx)
		if err != nil {
			return nil, err
		}
		baseRes, err := gw.Solve(ctx, bkgw.SolveRequest{
			Definition: baseDef.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		baseRef, err := baseRes.SingleRef()
		if err != nil {
			return nil, err
		}

		fsRes, err := gw.Solve(ctx, bkgw.SolveRequest{
			Definition: fs.Definition(dctx),
		})
		if err != nil {
			return nil, err
		}
		fsRef, err := fsRes.SingleRef()
		if err != nil {
			return nil, err
		}

		ctr, err := gw.NewContainer(ctx, bkgw.NewContainerRequest{
			Mounts: []bkgw.Mount{
				{
					Dest:      "/",
					Ref:       baseRef,
					MountType: pb.MountType_BIND,
				},
				{
					Dest:      "/output",
					Ref:       fsRef,
					MountType: pb.MountType_BIND,
				},
			},
		})
		if err != nil {
			return nil, err
		}
		proc, err := ctr.Start(ctx, bkgw.StartRequest{
			Args:   []string{"/bin/sh"},
			Cwd:    "/output",
			Tty:    true,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		})
		if err != nil {
			return nil, err
		}
		termState, err := terminal.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return nil, err
		}
		defer terminal.Restore(int(os.Stdin.Fd()), termState)
		return nil, proc.Wait()
	}, nil)
	return err
}
