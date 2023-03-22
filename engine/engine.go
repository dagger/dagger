package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/secret"
	"github.com/dagger/dagger/telemetry"
	"github.com/docker/cli/cli/config"
	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/entitlements"
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
	JournalFile   string
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
	c, privilegedExecEnabled, err := engine.Client(ctx, remote)
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

	// Load default labels asynchronously in the background.
	go pipeline.LoadRootLabels(ctx, startOpts.Workdir)

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

	registryAuth := auth.NewRegistryAuthProvider(config.LoadDefaultConfigFile(os.Stderr))

	var allowedEntitlements []entitlements.Entitlement
	if privilegedExecEnabled {
		// NOTE: this just allows clients to set this if they want. It also needs
		// to be set in the ExecOp LLB and enabled server-side in order for privileged
		// execs to actually run.
		allowedEntitlements = append(allowedEntitlements, entitlements.EntitlementSecurityInsecure)
	}

	solveOpts := bkclient.SolveOpt{
		Session: []session.Attachable{
			registryAuth,
			secretsprovider.NewSecretProvider(secretStore),
			socketProviders,
		},
		CacheExports: []bkclient.CacheOptionsEntry{{
			Type: "dagger",
			Attrs: map[string]string{
				"mode": "max",
			},
		}},
		CacheImports: []bkclient.CacheOptionsEntry{{
			Type: "dagger",
		}},
		AllowedEntitlements: allowedEntitlements,
	}

	if !startOpts.DisableHostRW {
		solveOpts.Session = append(solveOpts.Session, filesync.NewFSSyncProvider(AnyDirSource{}))
	}

	eg, ctx := errgroup.WithContext(ctx)
	solveCh := make(chan *bkclient.SolveStatus)
	eg.Go(func() error {
		return handleSolveEvents(startOpts, solveCh)
	})

	eg.Go(func() error {
		_, err := c.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			// Secret store is a circular dependency, since it needs to resolve
			// SecretIDs using the gateway, we don't have a gateway until we call
			// Build, which needs SolveOpts, which needs to contain the secret store.
			//
			// Thankfully we can just yeet the gateway into the store.
			secretStore.SetGateway(gw)

			gwClient := core.NewGatewayClient(gw)
			coreAPI, err := schema.New(schema.InitializeArgs{
				Router:         router,
				Workdir:        startOpts.Workdir,
				Gateway:        gwClient,
				BKClient:       c,
				SolveOpts:      solveOpts,
				SolveCh:        solveCh,
				Platform:       *platform,
				DisableHostRW:  startOpts.DisableHostRW,
				Auth:           registryAuth,
				EnableServices: os.Getenv(engine.ServicesDNSEnvName) != "0",
				Secrets:        secretStore,
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

			// Return a result that contains every reference that was solved in this session.
			// If cache export is enabled server-side, all these references will be exported.
			return gwClient.CombinedResult(), nil
		}, solveCh)
		return err
	})

	return eg.Wait()
}

func handleSolveEvents(startOpts *Config, ch chan *bkclient.SolveStatus) error {
	eg := &errgroup.Group{}
	readers := []chan *bkclient.SolveStatus{}

	// Dispatch events to raw listener
	if startOpts.RawBuildkitStatus != nil {
		readers = append(readers, startOpts.RawBuildkitStatus)
	}

	// (Optionally) Upload Telemetry
	telemetryCh := make(chan *bkclient.SolveStatus)
	readers = append(readers, telemetryCh)
	eg.Go(func() error {
		return uploadTelemetry(telemetryCh)
	})

	// Print events to the console
	if startOpts.LogOutput != nil {
		ch := make(chan *bkclient.SolveStatus)
		readers = append(readers, ch)

		// Read `ch`; strip away custom names; re-write to `cleanCh`
		cleanCh := make(chan *bkclient.SolveStatus)
		eg.Go(func() error {
			defer close(cleanCh)
			for ev := range ch {
				cleaned := *ev
				cleaned.Vertexes = make([]*bkclient.Vertex, len(ev.Vertexes))
				for i, v := range ev.Vertexes {
					customName := pipeline.CustomName{}
					if json.Unmarshal([]byte(v.Name), &customName) == nil {
						cp := *v
						cp.Name = customName.Name
						cleaned.Vertexes[i] = &cp
					} else {
						cleaned.Vertexes[i] = v
					}
				}
				cleanCh <- &cleaned
			}
			return nil
		})

		// Display from `cleanCh`
		eg.Go(func() error {
			warn, err := progressui.DisplaySolveStatus(context.TODO(), nil, startOpts.LogOutput, cleanCh)
			for _, w := range warn {
				fmt.Fprintf(startOpts.LogOutput, "=> %s\n", w.Short)
			}
			return err
		})
	}

	// Write events to a journal file
	if startOpts.JournalFile != "" {
		ch := make(chan *bkclient.SolveStatus)
		readers = append(readers, ch)
		eg.Go(func() error {
			f, err := os.OpenFile(
				startOpts.JournalFile,
				os.O_APPEND|os.O_CREATE|os.O_WRONLY,
				0644,
			)
			if err != nil {
				return err
			}
			defer f.Close()
			enc := json.NewEncoder(f)
			for ev := range ch {
				entry := struct {
					Event *bkclient.SolveStatus `json:"event"`
					TS    time.Time             `json:"ts"`
				}{
					Event: ev,
					TS:    time.Now().UTC(),
				}

				if err := enc.Encode(entry); err != nil {
					return err
				}
			}
			return nil
		})
	}

	eventsMultiReader(ch, readers...)
	return eg.Wait()
}

func eventsMultiReader(ch chan *bkclient.SolveStatus, readers ...chan *bkclient.SolveStatus) {
	wg := sync.WaitGroup{}

	for ev := range ch {
		for _, r := range readers {
			r := r
			ev := ev
			wg.Add(1)
			go func() {
				defer wg.Done()
				r <- ev
			}()
		}
	}

	wg.Wait()

	for _, w := range readers {
		close(w)
	}
}

func uploadTelemetry(ch chan *bkclient.SolveStatus) error {
	t := telemetry.New()
	defer t.Flush()

	for ev := range ch {
		ts := time.Now().UTC()

		for _, v := range ev.Vertexes {
			id := v.Digest.String()

			var custom pipeline.CustomName
			if json.Unmarshal([]byte(v.Name), &custom) != nil {
				custom.Name = v.Name
				if pg := v.ProgressGroup.GetId(); pg != "" {
					if err := json.Unmarshal([]byte(pg), &custom.Pipeline); err != nil {
						return err
					}
				}
			}

			payload := telemetry.OpPayload{
				OpID:     id,
				OpName:   custom.Name,
				Internal: custom.Internal,
				Pipeline: custom.Pipeline,

				Started:   v.Started,
				Completed: v.Completed,
				Cached:    v.Cached,
				Error:     v.Error,
			}

			payload.Inputs = []string{}
			for _, input := range v.Inputs {
				payload.Inputs = append(payload.Inputs, input.String())
			}

			t.Push(payload, ts)
		}

		for _, l := range ev.Logs {
			t.Push(telemetry.LogPayload{
				OpID:   l.Vertex.String(),
				Data:   string(l.Data),
				Stream: l.Stream,
			}, l.Timestamp)
		}
	}

	return nil
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
