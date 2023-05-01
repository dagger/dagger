package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/internal/engine/journal"
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
	"github.com/pkg/errors"
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
	JournalURI    string
	JournalWriter journal.Writer
	DisableHostRW bool
	RunnerHost    string
	SessionToken  string
	UserAgent     string

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
	c, err := engine.NewClient(ctx, remote, startOpts.UserAgent)
	if err != nil {
		return err
	}

	platform, err := detectPlatform(ctx, c.BuildkitClient)
	if err != nil {
		return err
	}

	startOpts.Workdir, startOpts.ConfigPath, err = NormalizePaths(startOpts.Workdir, startOpts.ConfigPath)
	if err != nil {
		return err
	}

	// Load default labels asynchronously in the background.
	go pipeline.LoadRootLabels(startOpts.Workdir, c.EngineName)

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
	if c.PrivilegedExecEnabled {
		// NOTE: this just allows clients to set this if they want. It also needs
		// to be set in the ExecOp LLB and enabled server-side in order for privileged
		// execs to actually run.
		allowedEntitlements = append(allowedEntitlements, entitlements.EntitlementSecurityInsecure)
	}

	ociStoreDir := filepath.Join(xdg.CacheHome, "dagger", "oci")
	ociStore, err := local.NewStore(ociStoreDir)
	if err != nil {
		return err
	}

	solveOpts := bkclient.SolveOpt{
		Session: []session.Attachable{
			registryAuth,
			secretsprovider.NewSecretProvider(secretStore),
			socketProviders,
		},
		AllowedEntitlements: allowedEntitlements,
		OCIStores: map[string]content.Store{
			core.OCIStoreName: ociStore,
		},
	}

	// Check if any of the upstream cache importers/exporters are enabled.
	// Note that this is not the cache service support in engine/cache/, that
	// is a different feature which is configured in the engine daemon.
	cacheConfigType, cacheConfigAttrs, err := cacheConfigFromEnv()
	if err != nil {
		return err
	}
	cacheConfigEnabled := cacheConfigType != ""
	if cacheConfigEnabled {
		solveOpts.CacheExports = []bkclient.CacheOptionsEntry{{
			Type:  cacheConfigType,
			Attrs: cacheConfigAttrs,
		}}
		solveOpts.CacheImports = []bkclient.CacheOptionsEntry{{
			Type:  cacheConfigType,
			Attrs: cacheConfigAttrs,
		}}
	}

	if !startOpts.DisableHostRW {
		solveOpts.Session = append(solveOpts.Session, filesync.NewFSSyncProvider(AnyDirSource{}))
	}

	eg, groupCtx := errgroup.WithContext(ctx)
	solveCh := make(chan *bkclient.SolveStatus)
	eg.Go(func() error {
		return handleSolveEvents(startOpts, solveCh)
	})

	eg.Go(func() error {
		_, err := c.BuildkitClient.Build(groupCtx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			// Secret store is a circular dependency, since it needs to resolve
			// SecretIDs using the gateway, we don't have a gateway until we call
			// Build, which needs SolveOpts, which needs to contain the secret store.
			//
			// Thankfully we can just yeet the gateway into the store.
			secretStore.SetGateway(gw)

			gwClient := core.NewGatewayClient(gw, cacheConfigType, cacheConfigAttrs)
			coreAPI, err := schema.New(schema.InitializeArgs{
				Router:         router,
				Workdir:        startOpts.Workdir,
				Gateway:        gwClient,
				BKClient:       c.BuildkitClient,
				SolveOpts:      solveOpts,
				SolveCh:        solveCh,
				Platform:       *platform,
				DisableHostRW:  startOpts.DisableHostRW,
				Auth:           registryAuth,
				EnableServices: os.Getenv(engine.ServicesDNSEnvName) != "0",
				Secrets:        secretStore,
				OCIStore:       ociStore,
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

			if cacheConfigEnabled {
				// Return a result that contains every reference that was solved in this session.
				return gwClient.CombinedResult(ctx)
			}
			return nil, nil
		}, solveCh)
		return err
	})

	err = eg.Wait()
	if err != nil {
		// preserve context error if any, otherwise we get an error sent over gRPC
		// that loses the original context error
		//
		// NB: only do this for the outer context; groupCtx.Err() will be != nil if
		// any of the group members errored, which isn't interesting
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	return nil
}

func handleSolveEvents(startOpts *Config, upstreamCh chan *bkclient.SolveStatus) error {
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
	if startOpts.JournalURI != "" {
		ch := make(chan *bkclient.SolveStatus)
		readers = append(readers, ch)
		u, err := url.Parse(startOpts.JournalURI)
		if err != nil {
			return fmt.Errorf("journal URI: %w", err)
		}

		switch u.Scheme {
		case "":
			eg.Go(func() error {
				f, err := os.OpenFile(
					startOpts.JournalURI,
					os.O_APPEND|os.O_CREATE|os.O_WRONLY,
					0644,
				)
				if err != nil {
					return err
				}
				defer f.Close()
				enc := json.NewEncoder(f)
				for ev := range ch {
					entry := &journal.Entry{
						Event: ev,
						TS:    time.Now().UTC(),
					}

					if err := enc.Encode(entry); err != nil {
						return err
					}
				}
				return nil
			})
		case "tcp":
			w, err := journal.Dial("tcp", u.Host)
			if err != nil {
				return fmt.Errorf("journal: %w", err)
			}
			defer w.Close()
			eg.Go(func() error {
				for ev := range ch {
					entry := &journal.Entry{
						Event: ev,
						TS:    time.Now().UTC(),
					}

					if err := w.WriteEntry(entry); err != nil {
						return err
					}
				}
				return nil
			})
		}
	}

	if startOpts.JournalWriter != nil {
		ch := make(chan *bkclient.SolveStatus)
		readers = append(readers, ch)

		journalW := startOpts.JournalWriter
		eg.Go(func() error {
			for ev := range ch {
				entry := &journal.Entry{
					Event: ev,
					TS:    time.Now().UTC(),
				}

				if err := journalW.WriteEntry(entry); err != nil {
					return err
				}
			}

			return journalW.Close()
		})
	}

	eventsMultiReader(upstreamCh, readers...)
	return eg.Wait()
}

func eventsMultiReader(ch chan *bkclient.SolveStatus, readers ...chan *bkclient.SolveStatus) {
	for ev := range ch {
		for _, r := range readers {
			r <- ev
		}
	}

	for _, w := range readers {
		close(w)
	}
}

func uploadTelemetry(ch chan *bkclient.SolveStatus) error {
	t := telemetry.New(true)
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

func cacheConfigFromEnv() (string, map[string]string, error) {
	envVal, ok := os.LookupEnv(engine.CacheConfigEnvName)
	if !ok {
		return "", nil, nil
	}

	// env is in form k1=v1,k2=v2,...
	kvs := strings.Split(envVal, ",")
	if len(kvs) == 0 {
		return "", nil, nil
	}
	attrs := make(map[string]string)
	for _, kv := range kvs {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return "", nil, errors.Errorf("invalid form for cache config %q", kv)
		}
		attrs[parts[0]] = parts[1]
	}
	typeVal, ok := attrs["type"]
	if !ok {
		return "", nil, errors.Errorf("missing type in cache config: %q", envVal)
	}
	delete(attrs, "type")
	return typeVal, attrs, nil
}
