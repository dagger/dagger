package engine

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/secret"
	"github.com/docker/cli/cli/config"
	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/progress/progressui"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
)

const (
	daggerJSONName = "dagger.json"
)

type Config struct {
	Workdir    string
	ConfigPath string
	// If true, do not load project extensions
	NoExtensions  bool
	LogOutput     io.Writer
	DisableHostRW bool
	RunnerHost    string
	SessionToken  string

	// WARNING: this is currently exposed directly but will be removed or
	// replaced with something incompatible in the future.
	RawBuildkitStatus chan *bkclient.SolveStatus
}

type StartCallback func(context.Context, *router.Router) error

func Start(ctx context.Context, startOpts *Config, fn StartCallback) error {
	if startOpts == nil || startOpts.RunnerHost == "" {
		return fmt.Errorf("must specify runner host")
	}

	remote, err := url.Parse(startOpts.RunnerHost)
	if err != nil {
		return err
	}
	c, err := engine.Client(ctx, remote)
	if err != nil {
		return err
	}

	platform, err := detectPlatform(ctx, c)
	if err != nil {
		return err
	}

	startOpts.Workdir, startOpts.ConfigPath, err = NormalizePaths(startOpts.Workdir, startOpts.ConfigPath)
	if err != nil {
		return err
	}

	_, err = os.Stat(startOpts.ConfigPath)
	switch {
	case err == nil:
		startOpts.ConfigPath, err = filepath.Rel(startOpts.Workdir, startOpts.ConfigPath)
		if err != nil {
			return err
		}
	case os.IsNotExist(err):
		startOpts.ConfigPath = ""
	default:
		return err
	}

	router := router.New(startOpts.SessionToken)
	secretStore := secret.NewStore()

	socketProviders := SocketProvider{
		EnableHostNetworkAccess: !startOpts.DisableHostRW,
	}

	solveOpts := bkclient.SolveOpt{
		Session: []session.Attachable{
			secretsprovider.NewSecretProvider(secretStore),
			socketProviders,
			authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr)),
		},
	}

	if !startOpts.DisableHostRW {
		solveOpts.Session = append(solveOpts.Session, filesync.NewFSSyncProvider(AnyDirSource{}))
	}

	eg, ctx := errgroup.WithContext(ctx)

	if startOpts.RawBuildkitStatus == nil && startOpts.LogOutput != nil {
		ch := make(chan *bkclient.SolveStatus)
		startOpts.RawBuildkitStatus = ch

		eg.Go(func() error {
			w := startOpts.LogOutput
			if w == nil {
				w = io.Discard
			}

			warn, err := progressui.DisplaySolveStatus(context.TODO(), "", nil, w, ch)
			for _, w := range warn {
				fmt.Fprintf(os.Stderr, "=> %s\n", w.Short)
			}
			return err
		})
	}

	eg.Go(func() error {
		_, err := c.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			// Secret store is a circular dependency, since it needs to resolve
			// SecretIDs using the gateway, we don't have a gateway until we call
			// Build, which needs SolveOpts, which needs to contain the secret store.
			//
			// Thankfully we can just yeet the gateway into the store.
			secretStore.SetGateway(gw)

			coreAPI, err := schema.New(schema.InitializeArgs{
				Router:        router,
				Workdir:       startOpts.Workdir,
				Gateway:       gw,
				BKClient:      c,
				SolveOpts:     solveOpts,
				SolveCh:       startOpts.RawBuildkitStatus,
				Platform:      *platform,
				DisableHostRW: startOpts.DisableHostRW,
			})
			if err != nil {
				return nil, err
			}
			if err := router.Add(coreAPI); err != nil {
				return nil, err
			}

			if startOpts.ConfigPath != "" && !startOpts.NoExtensions {
				_, err = installExtensions(
					ctx,
					router,
					startOpts.ConfigPath,
				)
				if err != nil {
					return nil, err
				}
			}

			if fn == nil {
				return nil, nil
			}

			if err := fn(ctx, router); err != nil {
				return nil, err
			}

			return bkgw.NewResult(), nil
		}, startOpts.RawBuildkitStatus)
		return err
	})

	return eg.Wait()
}

func NormalizePaths(workdir, configPath string) (string, string, error) {
	if workdir == "" {
		workdir = os.Getenv("DAGGER_WORKDIR")
	}
	if workdir == "" {
		var err error
		workdir, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	workdir, err := filepath.Abs(workdir)
	if err != nil {
		return "", "", err
	}

	if configPath == "" {
		configPath = os.Getenv("DAGGER_CONFIG")
	}
	if configPath == "" {
		configPath = filepath.Join(workdir, daggerJSONName)
	}
	if !filepath.IsAbs(configPath) {
		var err error
		configPath, err = filepath.Abs(configPath)
		if err != nil {
			return "", "", err
		}
	}
	return workdir, configPath, nil
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

func installExtensions(ctx context.Context, r *router.Router, configPath string) (*schema.Project, error) {
	res := struct {
		Core struct {
			Directory struct {
				LoadProject schema.Project
			}
		}
	}{}

	_, err := r.Do(ctx,
		// FIXME:(sipsma) toggling install is extremely weird here, need better way
		`
			query LoadProject($configPath: String!) {
				host {
					workdir {
						loadProject(configPath: $configPath) {
							name
							install
						}
					}
				}
			}`,
		"LoadProject",
		map[string]any{
			"configPath": configPath,
		},
		&res,
	)
	if err != nil {
		return nil, err
	}

	return &res.Core.Directory.LoadProject, nil
}

type AnyDirSource struct{}

func (AnyDirSource) LookupDir(name string) (filesync.SyncedDir, bool) {
	return filesync.SyncedDir{
		Dir: name,
		Map: func(p string, st *fstypes.Stat) fsutil.MapResult {
			st.Uid = 0
			st.Gid = 0
			return fsutil.MapResultKeep
		},
	}, true
}
