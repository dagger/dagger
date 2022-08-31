package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/containerd/containerd/platforms"
	"github.com/dagger/cloak/core"
	"github.com/dagger/cloak/extension"
	"github.com/dagger/cloak/router"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/dagger/cloak/secret"
	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/moby/buildkit/util/tracing/detect"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.opentelemetry.io/otel"
	"golang.org/x/sync/errgroup"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
)

const (
	workdirID = "__cloak_workdir" // FIXME:(sipsma) just hoping users don't try to use this as a directory id themselves, not robust
)

type Config struct {
	LocalDirs  map[string]string
	DevServer  int
	Workdir    string
	ConfigPath string
	// If true, just load extension metadata rather than compiling and stitching them in
	SkipInstall bool
}

type Context struct {
	context.Context
	Client     graphql.Client
	Operations string
	LocalDirs  map[string]dagger.FSID
	Extension  *core.Extension
}

type StartCallback func(Context) error

func Start(ctx context.Context, startOpts *Config, fn StartCallback) error {
	if startOpts == nil {
		startOpts = &Config{}
	}

	opts := []bkclient.ClientOpt{
		bkclient.WithFailFast(),
		bkclient.WithTracerProvider(otel.GetTracerProvider()),
	}

	exp, err := detect.Exporter()
	if err != nil {
		return err
	}

	if td, ok := exp.(bkclient.TracerDelegate); ok {
		opts = append(opts, bkclient.WithTracerDelegate(td))
	}

	c, err := bkclient.New(ctx, "docker-container://dagger-buildkitd", opts...)
	if err != nil {
		return err
	}

	platform, err := detectPlatform(ctx, c)
	if err != nil {
		return err
	}

	router := router.New()
	secretStore := secret.NewStore()

	socketProviders := MergedSocketProviders{
		extension.DaggerSockName: extension.NewAPIProxy(router),
	}
	var sshAuthSockID string
	if _, ok := os.LookupEnv(sshAuthSockEnv); ok {
		sshAuthHandler, err := sshAuthSockHandler()
		if err != nil {
			return err
		}
		// using env key as the socket ID too for now
		sshAuthSockID = sshAuthSockEnv
		socketProviders[sshAuthSockID] = sshAuthHandler
	}
	solveOpts := bkclient.SolveOpt{
		Session: []session.Attachable{
			secretsprovider.NewSecretProvider(secretStore),
			socketProviders,
			authprovider.NewDockerAuthProvider(os.Stderr),
		},
	}
	if startOpts.LocalDirs == nil {
		startOpts.LocalDirs = map[string]string{}
	}
	startOpts.LocalDirs[workdirID] = startOpts.Workdir
	solveOpts.LocalDirs = startOpts.LocalDirs

	ch := make(chan *bkclient.SolveStatus)
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		var err error
		_, err = c.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			coreAPI, err := core.New(core.InitializeArgs{
				Router:        router,
				SecretStore:   secretStore,
				SSHAuthSockID: sshAuthSockID,
				WorkdirID:     workdirID,
				Gateway:       gw,
				BKClient:      c,
				SolveOpts:     solveOpts,
				SolveCh:       ch,
				Platform:      *platform,
			})
			if err != nil {
				return nil, err
			}
			if err := router.Add(coreAPI); err != nil {
				return nil, err
			}

			ctx = withInMemoryAPIClient(ctx, router)
			engineCtx := Context{
				Context: ctx,
			}

			engineCtx.Client, err = dagger.Client(ctx)
			if err != nil {
				return nil, err
			}

			engineCtx.LocalDirs, err = loadLocalDirs(ctx, engineCtx.Client, solveOpts.LocalDirs)
			if err != nil {
				return nil, err
			}

			if !startOpts.SkipInstall {
				defaultOperations, err := installExtension(
					ctx,
					engineCtx.Client,
					engineCtx.LocalDirs[workdirID],
					startOpts.ConfigPath,
				)
				if err != nil {
					return nil, err
				}
				engineCtx.Operations = defaultOperations
			}

			if fn == nil {
				return nil, nil
			}

			if err := fn(engineCtx); err != nil {
				return nil, err
			}

			if startOpts.DevServer != 0 {
				fmt.Fprintf(os.Stderr, "==> dev server listening on http://localhost:%d", startOpts.DevServer)
				return nil, http.ListenAndServe(fmt.Sprintf(":%d", startOpts.DevServer), router)
			}

			return bkgw.NewResult(), nil
		}, ch)
		return err
	})
	eg.Go(func() error {
		warn, err := progressui.DisplaySolveStatus(context.TODO(), "", nil, os.Stderr, ch)
		for _, w := range warn {
			fmt.Fprintf(os.Stderr, "=> %s\n", w.Short)
		}
		return err
	})
	if err := eg.Wait(); err != nil {
		return err
	}
	return nil
}

func withInMemoryAPIClient(ctx context.Context, router *router.Router) context.Context {
	return dagger.WithHTTPClient(ctx, &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				// TODO: not efficient, but whatever
				serverConn, clientConn := net.Pipe()

				go func() {
					_ = router.ServeConn(serverConn)
				}()

				return clientConn, nil
			},
		},
	})
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

func loadLocalDirs(ctx context.Context, cl graphql.Client, localDirs map[string]string) (map[string]dagger.FSID, error) {
	var eg errgroup.Group
	var l sync.Mutex

	mapping := map[string]dagger.FSID{}
	for localID := range localDirs {
		localID := localID
		eg.Go(func() error {
			res := struct {
				Host struct {
					Dir struct {
						Read struct {
							Id dagger.FSID
						}
					}
				}
			}{}
			resp := &graphql.Response{Data: &res}

			err := cl.MakeRequest(ctx,
				&graphql.Request{
					Query: `
						query LocalDir($id: String!) {
							host {
								dir(id: $id) {
									read {
										id
									}
								}
							}
						}
					`,
					Variables: map[string]any{
						"id": localID,
					},
				},
				resp,
			)
			if err != nil {
				return err
			}

			l.Lock()
			mapping[localID] = res.Host.Dir.Read.Id
			l.Unlock()

			return nil
		})
	}

	return mapping, eg.Wait()
}

func installExtension(ctx context.Context, cl graphql.Client, contextFS dagger.FSID, configPath string) (operations string, rerr error) {
	res := struct {
		Core struct {
			Filesystem struct {
				LoadExtension struct {
					Operations string
				}
			}
		}
	}{}
	resp := &graphql.Response{Data: &res}

	err := cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query LoadExtension($fs: FSID!, $configPath: String!) {
				core {
					filesystem(id: $fs) {
						loadExtension(configPath: $configPath) {
							install
							operations
						}
					}
				}
			}`,
			Variables: map[string]any{
				"fs":         contextFS,
				"configPath": configPath,
			},
		},
		resp,
	)
	if err != nil {
		return "", err
	}

	return res.Core.Filesystem.LoadExtension.Operations, nil
}
