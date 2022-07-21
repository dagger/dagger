package engine

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/containerd/containerd/platforms"
	"github.com/dagger/cloak/api"
	"github.com/dagger/cloak/sdk/go/dagger"
	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/progress/progressui"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/sync/errgroup"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
	"github.com/moby/buildkit/client/llb"
)

type StartOpts struct {
	Export    *bkclient.ExportEntry
	LocalDirs map[string]string
}

type StartCallback func(ctx context.Context, localDirs map[string]dagger.FS) (*dagger.FS, error)

func Start(ctx context.Context, startOpts *StartOpts, fn StartCallback) error {
	c, err := bkclient.New(ctx, "docker-container://dagger-buildkitd", bkclient.WithFailFast())
	if err != nil {
		return err
	}

	platform, err := detectPlatform(ctx, c)
	if err != nil {
		return err
	}

	ch := make(chan *bkclient.SolveStatus)

	var server api.Server
	attachables := []session.Attachable{&server}

	solveOpts := bkclient.SolveOpt{
		Session: attachables,
	}
	if startOpts != nil {
		if startOpts.Export != nil {
			solveOpts.Exports = []bkclient.ExportEntry{*startOpts.Export}
		}
		solveOpts.LocalDirs = startOpts.LocalDirs
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		var err error
		_, err = c.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			defer func() {
				if r := recover(); r != nil {
					time.Sleep(2 * time.Second) // TODO: dumb, but allows logs to fully flush
					panic(r)
				}
			}()
			server = api.NewServer(gw, platform)

			ctx = dagger.WithInMemoryAPIClient(ctx, server)
			ctx = withGatewayClient(ctx, gw)
			ctx = withPlatform(ctx, platform)

			cl, err := dagger.Client(ctx)
			if err != nil {
				return nil, err
			}

			localDirs := make(map[string]dagger.FS)
			for localID := range solveOpts.LocalDirs {
				res := struct {
					ClientDir dagger.FS
				}{}
				err = cl.MakeRequest(ctx,
					&graphql.Request{
						Query: `
							mutation ClientDir($id: String!) {
								clientdir(id: $id)
							}`,
						Variables: map[string]any{
							"id": localID,
						},
					},
					&graphql.Response{Data: &res},
				)
				if err != nil {
					return nil, err
				}

				localDirs[localID] = dagger.FS(res.ClientDir)
			}

			outputFs, err := fn(ctx, localDirs)
			if err != nil {
				return nil, err
			}

			var result *bkgw.Result
			if outputFs != nil {
				data := struct {
					Evaluate dagger.FS
				}{}
				err = cl.MakeRequest(ctx,
					&graphql.Request{
						Query: `
							mutation Evaluate($fs: FS!) {
								evaluate(fs: $fs)
							}`,
						Variables: map[string]any{
							"fs": outputFs,
						},
					},
					&graphql.Response{Data: &data},
				)
				if err != nil {
					return nil, err
				}

				var fs api.FS
				if err := fs.UnmarshalText([]byte(data.Evaluate)); err != nil {
					return nil, err
				}
				res, err := gw.Solve(ctx, bkgw.SolveRequest{Evaluate: true, Definition: fs.PB})
				if err != nil {
					return nil, err
				}
				result = res
			}
			if result == nil {
				result = bkgw.NewResult()
			}

			return result, nil
		}, ch)
		return err
	})
	eg.Go(func() error {
		warn, err := progressui.DisplaySolveStatus(context.TODO(), "", nil, os.Stdout, ch)
		for _, w := range warn {
			fmt.Printf("=> %s\n", w.Short)
		}
		return err
	})
	if err := eg.Wait(); err != nil {
		return err
	}
	return nil
}

type gatewayClientKey struct{}

func withGatewayClient(ctx context.Context, gw bkgw.Client) context.Context {
	return context.WithValue(ctx, gatewayClientKey{}, gw)
}

type platformKey struct{}

func withPlatform(ctx context.Context, platform *specs.Platform) context.Context {
	return context.WithValue(ctx, platformKey{}, platform)
}

func detectPlatform(ctx context.Context, c *bkclient.Client) (*specs.Platform, error) {
	w, err := c.ListWorkers(ctx)
	if err != nil {
		return nil, fmt.Errorf("error detecting platform %w", err)
	}

	if len(w) > 0 && len(w[0].Platforms) > 0 {
		dPlatform := w[0].Platforms[0]
		return &dPlatform, nil
	}
	defaultPlatform := platforms.DefaultSpec()
	return &defaultPlatform, nil
}

func Shell(ctx context.Context, inputFS dagger.FS) error {
	gw := ctx.Value(gatewayClientKey{}).(bkgw.Client)
	platform := ctx.Value(platformKey{}).(*specs.Platform)
	baseDef, err := llb.Image("alpine:3.15").Marshal(ctx, llb.Platform(*platform))
	if err != nil {
		return err
	}
	baseRes, err := gw.Solve(ctx, bkgw.SolveRequest{
		Definition: baseDef.ToPB(),
	})
	if err != nil {
		return err
	}
	baseRef, err := baseRes.SingleRef()
	if err != nil {
		return err
	}

	var fs api.FS
	if err := fs.UnmarshalText([]byte(inputFS)); err != nil {
		return err
	}
	fsRes, err := gw.Solve(ctx, bkgw.SolveRequest{
		Definition: fs.PB,
	})
	if err != nil {
		return err
	}
	fsRef, err := fsRes.SingleRef()
	if err != nil {
		return err
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
		return err
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
		return err
	}
	termState, err := terminal.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer terminal.Restore(int(os.Stdin.Fd()), termState)
	return proc.Wait()
}

func RunGraphiQL(ctx context.Context, port int) error {
	return Start(ctx, nil, func(ctx context.Context, _ map[string]dagger.FS) (*dagger.FS, error) {
		gw := ctx.Value(gatewayClientKey{}).(bkgw.Client)
		platform := ctx.Value(platformKey{}).(*specs.Platform)
		return nil, api.RunGraphiQLServer(ctx, port, gw, platform)
	})
}
