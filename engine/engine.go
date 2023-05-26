package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Config struct {
	Workdir        string
	LogOutput      io.Writer
	JournalFile    string
	JournalWriter  journal.Writer
	ProgrockWriter progrock.Writer
	DisableHostRW  bool
	RunnerHost     string
	SessionToken   string
	UserAgent      string
}

type StartCallback func(context.Context, *router.Router) error

func Start(ctx context.Context, startOpts Config, fn StartCallback) error {
	if startOpts.RunnerHost == "" {
		return fmt.Errorf("must specify runner host")
	}

	if startOpts.ProgrockWriter == nil {
		startOpts.ProgrockWriter = progrock.Discard{}
	}

	remote, err := url.Parse(startOpts.RunnerHost)
	if err != nil {
		return err
	}

	c, err := engine.NewClient(ctx, remote, startOpts.UserAgent)
	if err != nil {
		return err
	}

	if c.EngineName != "" && startOpts.LogOutput != nil {
		fmt.Fprintln(startOpts.LogOutput, "Connected to engine", c.EngineName)
	}

	// Load default labels asynchronously in the background.
	go pipeline.LoadRootLabels(startOpts.Workdir, c.EngineName)

	// NB(vito): this RootLabels call effectively makes loading labels
	// synchronous, but it was already required for running just about any query
	// (see core/query.go), and it's still helpful to have the separate
	// LoadRootLabels step until we can get rid of the core/query.go call site.
	labels := []*progrock.Label{}
	for _, label := range pipeline.RootLabels() {
		labels = append(labels, &progrock.Label{
			Name:  label.Name,
			Value: label.Value,
		})
	}

	progSock, progW, cleanup, err := progrockForwarder(startOpts.ProgrockWriter, startOpts.LogOutput)
	if err != nil {
		return fmt.Errorf("progress forwarding: %w", err)
	}
	defer cleanup()

	recorder := progrock.NewRecorder(progW, labels...)
	ctx = progrock.RecorderToContext(ctx, recorder)

	platform, err := detectPlatform(ctx, c.BuildkitClient)
	if err != nil {
		return err
	}

	startOpts.Workdir, err = NormalizeWorkdir(startOpts.Workdir)
	if err != nil {
		return err
	}

	router := router.New(startOpts.SessionToken, recorder)
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
		return handleSolveEvents(recorder, startOpts, solveCh)
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
				ProgrockSocket: progSock,
			})
			if err != nil {
				return nil, err
			}
			if err := router.Add(coreAPI); err != nil {
				return nil, err
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

func handleSolveEvents(recorder *progrock.Recorder, startOpts Config, upstreamCh chan *bkclient.SolveStatus) error {
	eg := &errgroup.Group{}
	readers := []chan *bkclient.SolveStatus{}

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
			f, err := os.Create(startOpts.JournalFile)
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

	if startOpts.ProgrockWriter != nil {
		ch := make(chan *bkclient.SolveStatus)
		readers = append(readers, ch)

		eg.Go(func() error {
			for ev := range ch {
				if err := recorder.Record(bk2progrock(ev)); err != nil {
					return err
				}
			}

			// mark all groups completed
			recorder.Complete()

			// close the recorder so the UI exits
			return recorder.Close()
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

func NormalizeWorkdir(workdir string) (string, error) {
	if workdir == "" {
		workdir = os.Getenv("DAGGER_WORKDIR")
	}

	if workdir == "" {
		var err error
		workdir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	workdir, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}

	return workdir, nil
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

func bk2progrock(event *bkclient.SolveStatus) *progrock.StatusUpdate {
	var status progrock.StatusUpdate
	for _, v := range event.Vertexes {
		vtx := &progrock.Vertex{
			Id:     v.Digest.String(),
			Name:   v.Name,
			Cached: v.Cached,
		}
		if strings.Contains(v.Name, "[hide]") {
			vtx.Internal = true
		}
		for _, input := range v.Inputs {
			vtx.Inputs = append(vtx.Inputs, input.String())
		}
		if v.Started != nil {
			vtx.Started = timestamppb.New(*v.Started)
		}
		if v.Completed != nil {
			vtx.Completed = timestamppb.New(*v.Completed)
		}
		if v.Error != "" {
			if strings.HasSuffix(v.Error, context.Canceled.Error()) {
				vtx.Canceled = true
			} else {
				msg := v.Error
				vtx.Error = &msg
			}
		}

		// clean up any shimmied CustomNames
		// TODO(vito): remove this once we stop relying on ProgressGroup/CustomName
		// JSON embedding for progress
		var custom pipeline.CustomName
		if json.Unmarshal([]byte(v.Name), &custom) == nil {
			vtx.Name = custom.Name
			vtx.Internal = custom.Internal
		}

		status.Vertexes = append(status.Vertexes, vtx)
	}

	for _, s := range event.Statuses {
		task := &progrock.VertexTask{
			Vertex:  s.Vertex.String(),
			Name:    s.ID, // remap
			Total:   s.Total,
			Current: s.Current,
		}
		if s.Started != nil {
			task.Started = timestamppb.New(*s.Started)
		}
		if s.Completed != nil {
			task.Completed = timestamppb.New(*s.Completed)
		}
		status.Tasks = append(status.Tasks, task)
	}

	for _, s := range event.Logs {
		status.Logs = append(status.Logs, &progrock.VertexLog{
			Vertex:    s.Vertex.String(),
			Stream:    progrock.LogStream(s.Stream),
			Data:      s.Data,
			Timestamp: timestamppb.New(s.Timestamp),
		})
	}

	return &status
}

func progrockForwarder(w progrock.Writer, rawOutput io.Writer) (string, progrock.Writer, func() error, error) {
	parentDir := filepath.Join(
		xdg.RuntimeDir,
		"dagger",
	)
	err := os.MkdirAll(parentDir, 0700)
	if os.IsPermission(err) {
		fallbackParentDir := filepath.Join(
			xdg.CacheHome,
			"dagger",
		)
		fmt.Fprintf(rawOutput, "WARNING: unable to create dagger runtime dir %s, falling back to %s: %v\n", parentDir, fallbackParentDir, err)
		if err := os.MkdirAll(fallbackParentDir, 0700); err != nil {
			return "", nil, nil, err
		}
		parentDir = fallbackParentDir
	} else if err != nil {
		return "", nil, nil, err
	}

	progSock := filepath.Join(
		parentDir,
		fmt.Sprintf("progrock-%d.sock", time.Now().UnixNano()),
	)

	l, err := net.Listen("unix", progSock)
	if err != nil {
		return "", nil, nil, err
	}

	progW, err := progrock.ServeRPC(l, w)
	if err != nil {
		return "", nil, nil, err
	}

	return progSock, progW, l.Close, nil
}
