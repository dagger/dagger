package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	Export *bkclient.ExportEntry
	// TODO: All these fields can in theory be more dynamic, added after Start is called, but that requires
	// varying levels of effort (secrets are easy, local dirs are hard unless we patch upstream buildkit)
	LocalDirs map[string]string
	Secrets   map[string]string
}

type StartCallback func(ctx context.Context, localDirs map[string]dagger.FS, secrets map[string]string) (*dagger.FS, error)

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
	solveOpts := bkclient.SolveOpt{
		Session: []session.Attachable{&server},
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

			secrets := make(map[string]string)
			secretIDToKey := make(map[string]string)
			for k, v := range startOpts.Secrets {
				hashkey := sha256.Sum256([]byte(v))
				hashVal := hex.EncodeToString(hashkey[:])
				secrets[hashVal] = v
				secretIDToKey[k] = hashVal
			}

			server = api.NewServer(gw, platform, secrets)

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
				resp := &graphql.Response{Data: &res}
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
					resp,
				)
				if err != nil {
					return nil, err
				}
				if len(resp.Errors) > 0 {
					return nil, resp.Errors
				}

				// copy to scratch to avoid making the buildkit's snapshot of the local dir immutable,
				// which makes it unable to reused, which in turn creates cache invalidations
				copyRes := struct {
					Core struct {
						Copy dagger.FS
					}
				}{}
				resp = &graphql.Response{Data: &copyRes}
				err = cl.MakeRequest(ctx,
					&graphql.Request{
						Query: `
							query Copy($src: FS!) {
								core {
									copy(src: $src)
								}
							}`,
						Variables: map[string]any{
							"src": res.ClientDir,
						},
					},
					resp,
				)
				if err != nil {
					return nil, err
				}
				if len(resp.Errors) > 0 {
					return nil, resp.Errors
				}
				localDirs[localID] = copyRes.Core.Copy
			}

			outputFs, err := fn(ctx, localDirs, secretIDToKey)
			if err != nil {
				return nil, err
			}

			var result *bkgw.Result
			if outputFs != nil {
				var fs api.FS
				if err := fs.UnmarshalText([]byte(*outputFs)); err != nil {
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

func ListenAndServe(ctx context.Context, port int) error {
	return Start(ctx, &StartOpts{}, func(ctx context.Context, _ map[string]dagger.FS, _ map[string]string) (*dagger.FS, error) {
		gw := ctx.Value(gatewayClientKey{}).(bkgw.Client)
		platform := ctx.Value(platformKey{}).(*specs.Platform)
		return nil, api.ListenAndServe(ctx, port, gw, platform)
	})
}
