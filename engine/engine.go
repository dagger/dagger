package engine

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/engine/filesync"
	"github.com/dagger/dagger/internal/buildkitd"
	"github.com/dagger/dagger/project"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/sessions"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/progress/progressui"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	fstypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
)

type Config struct {
	Workdir       string
	ConfigPath    string
	LogOutput     io.Writer
	DisableHostRW bool

	// WARNING: this is currently exposed directly but will be removed or
	// replaced with something incompatible in the future.
	RawBuildkitStatus chan *bkclient.SolveStatus
}

type StartCallback func(context.Context, *router.Router) error

func Start(ctx context.Context, startOpts *Config, fn StartCallback) error {
	if startOpts == nil {
		startOpts = &Config{}
	}

	c, err := buildkitd.Client(ctx)
	if err != nil {
		return err
	}

	platform, err := detectPlatform(ctx, c)
	if err != nil {
		return err
	}

	socketProviders := MergedSocketProviders{}
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
			socketProviders,
		},
	}

	// if !startOpts.DisableHostRW {
	// 	solveOpts.Session = append(solveOpts.Session, filesync.NewFSSyncProvider(AnyDirSource{}))
	// }

	eg, ctx := errgroup.WithContext(ctx)

	if startOpts.RawBuildkitStatus == nil && startOpts.LogOutput != nil {
		ch := make(chan *bkclient.SolveStatus)
		startOpts.RawBuildkitStatus = ch

		go func() {
			w := startOpts.LogOutput
			if w == nil {
				w = io.Discard
			}

			warn, err := progressui.DisplaySolveStatus(context.TODO(), "", nil, w, ch)
			for _, w := range warn {
				fmt.Fprintf(os.Stderr, "=> %s\n", w.Short)
			}

			if err != nil {
				log.Println("DISPLAY", err)
			}
		}()
	}

	sm := sessions.NewManager(c, startOpts.RawBuildkitStatus, solveOpts)

	router := router.New(sm)
	socketProviders[core.DaggerSockName] = project.NewAPIProxy(router)

	eg.Go(func() error {
		if startOpts.RawBuildkitStatus != nil {
			defer func() {
				sm.Wait()
				close(startOpts.RawBuildkitStatus)
			}()
		}

		coreAPI, err := schema.New(schema.InitializeArgs{
			Router:        router,
			SSHAuthSockID: sshAuthSockID,
			Workdir:       startOpts.Workdir,
			Sessions:      sm,
			BKClient:      c,
			SolveOpts:     solveOpts,
			SolveCh:       startOpts.RawBuildkitStatus,
			Platform:      *platform,
			DisableHostRW: startOpts.DisableHostRW,
		})
		if err != nil {
			return err
		}

		if err := router.Add(coreAPI); err != nil {
			return err
		}

		if fn == nil {
			return nil
		}

		return fn(ctx, router)
	})

	return eg.Wait()
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

type AnyDirSource struct{}

func (AnyDirSource) LookupDir(name string) (filesync.SyncedDir, bool) {
	return filesync.SyncedDir{
		Dir: name,
		Map: func(p string, st *fstypes.Stat) bool {
			st.Uid = 0
			st.Gid = 0
			return true
		},
	}, true
}
