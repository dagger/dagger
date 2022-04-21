package client

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/containerd/containerd/platforms"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/rs/zerolog/log"

	specs "github.com/opencontainers/image-spec/specs-go/v1"

	// Cue

	// buildkit
	bk "github.com/moby/buildkit/client"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
	_ "github.com/moby/buildkit/client/connhelper/kubepod"         // import the kubernetes connection driver
	_ "github.com/moby/buildkit/client/connhelper/podmancontainer" // import the podman connection driver

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"

	// docker output
	"go.dagger.io/dagger/plan/task"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/util/buildkitd"
	"go.dagger.io/dagger/util/progressui"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/solver"
)

// Client is a dagger client
type Client struct {
	c   *bk.Client
	cfg Config
}

type Config struct {
	NoCache bool

	CacheExports []bk.CacheOptionsEntry
	CacheImports []bk.CacheOptionsEntry

	TargetPlatform *specs.Platform
}

func New(ctx context.Context, host string, cfg Config) (*Client, error) {
	if host == "" {
		host = os.Getenv("BUILDKIT_HOST")
	}
	if host == "" {
		h, err := buildkitd.Start(ctx)
		if err != nil {
			return nil, err
		}

		host = h
	}
	opts := []bk.ClientOpt{}

	if span := trace.SpanFromContext(ctx); span != nil {
		opts = append(opts, bk.WithTracerProvider(span.TracerProvider()))
	}

	c, err := bk.New(ctx, host, opts...)
	if err != nil {
		return nil, fmt.Errorf("buildkit client: %w", err)
	}
	return &Client{
		c:   c,
		cfg: cfg,
	}, nil
}

type DoFunc func(context.Context, *solver.Solver) error

// FIXME: return completed *Route, instead of *compiler.Value
func (c *Client) Do(ctx context.Context, pctx *plancontext.Context, fn DoFunc) error {
	lg := log.Ctx(ctx)
	eg, gctx := errgroup.WithContext(ctx)

	if c.cfg.TargetPlatform != nil {
		pctx.Platform.Set(*c.cfg.TargetPlatform)
	} else {
		p, err := c.detectPlatform(ctx)
		if err != nil {
			return err
		}
		pctx.Platform.Set(*p)
	}

	// Spawn print function
	events := make(chan *bk.SolveStatus)
	eg.Go(func() error {
		// Create a background context so that logging will not be cancelled
		// with the main context.
		dispCtx := lg.WithContext(context.Background())
		return c.logSolveStatus(dispCtx, pctx, events)
	})

	// Spawn build function
	eg.Go(func() error {
		return c.buildfn(gctx, pctx, fn, events)
	})

	return eg.Wait()
}

// detectPlatform tries using Buildkit's target platform;
// if not possible, default platform will be used.
func (c *Client) detectPlatform(ctx context.Context) (*specs.Platform, error) {
	w, err := c.c.ListWorkers(ctx)
	if err != nil {
		return nil, fmt.Errorf("error detecting platform %w", err)
	}

	lg := log.Ctx(ctx)
	if len(w) > 0 && len(w[0].Platforms) > 0 {
		dPlatform := w[0].Platforms[0]
		lg.Debug().
			Str("platform", fmt.Sprintf("%s", dPlatform)).
			Msg("platform detected automatically")
		return &dPlatform, nil
	}
	defaultPlatform := platforms.DefaultSpec()
	return &defaultPlatform, nil
}

func convertCacheOptionEntries(ims []bk.CacheOptionsEntry) []bkgw.CacheOptionsEntry {
	convertIms := []bkgw.CacheOptionsEntry{}

	for _, im := range ims {
		convertIm := bkgw.CacheOptionsEntry{
			Type:  im.Type,
			Attrs: im.Attrs,
		}
		convertIms = append(convertIms, convertIm)
	}
	return convertIms
}

func (c *Client) buildfn(ctx context.Context, pctx *plancontext.Context, fn DoFunc, ch chan *bk.SolveStatus) error {
	wg := sync.WaitGroup{}

	// Close output channel
	defer func() {
		// Wait until all the events are caught
		wg.Wait()
		close(ch)
	}()

	lg := log.Ctx(ctx)

	// buildkit auth provider (registry)
	auth := solver.NewRegistryAuthProvider()

	localdirs, err := pctx.LocalDirs.Paths()
	if err != nil {
		return err
	}

	// Setup solve options
	opts := bk.SolveOpt{
		LocalDirs: localdirs,
		Session: []session.Attachable{
			auth,
			solver.NewSecretsStoreProvider(pctx),
			solver.NewDockerSocketProvider(pctx),
		},
		CacheExports: c.cfg.CacheExports,
		CacheImports: c.cfg.CacheImports,
	}

	// Call buildkit solver
	lg.Debug().
		Interface("localdirs", opts.LocalDirs).
		Interface("attrs", opts.FrontendAttrs).
		Msg("spawning buildkit job")

	// Catch output from events
	catchOutput := func(inCh chan *bk.SolveStatus) {
		for e := range inCh {
			ch <- e
		}
		wg.Done()
	}

	// Catch build events
	// Closed by buildkit
	buildCh := make(chan *bk.SolveStatus)
	wg.Add(1)
	go catchOutput(buildCh)

	resp, err := c.c.Build(ctx, opts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		// Catch solver's events
		// Closed by solver.Stop
		eventsCh := make(chan *bk.SolveStatus)
		wg.Add(1)
		go catchOutput(eventsCh)

		s := solver.New(solver.Opts{
			Control:      c.c,
			Gateway:      gw,
			Events:       eventsCh,
			Auth:         auth,
			NoCache:      c.cfg.NoCache,
			CacheImports: convertCacheOptionEntries(opts.CacheImports),
		})

		// Close events channel
		defer s.Stop()

		// Compute output overlay
		res := bkgw.NewResult()
		if fn != nil {
			err := fn(ctx, s)
			if err != nil {
				return nil, compiler.Err(err)
			}

			refs := s.References()
			// Add functions layers
			for _, ref := range refs {
				res.AddRef(uuid.New().String(), ref)
			}
		}
		return res, nil
	}, buildCh)
	if err != nil {
		return solver.CleanError(err)
	}
	for k, v := range resp.ExporterResponse {
		// FIXME consume exporter response
		lg.
			Debug().
			Str("key", k).
			Str("value", v).
			Msg("exporter response")
	}

	return nil
}

func (c *Client) logSolveStatus(ctx context.Context, pctx *plancontext.Context, ch chan *bk.SolveStatus) error {
	parseName := func(v *bk.Vertex) (string, string) {
		// For all cases besides resolve image config, the component is set in the progress group id
		if v.ProgressGroup != nil {
			return v.ProgressGroup.Id, v.Name
		}
		// fallback to parsing the component and vertex name of out just the name
		return task.ParseResolveImageConfigLog(v.Name)
	}

	// Just like sprintf, but redacts secrets automatically
	secureSprintf := func(format string, a ...interface{}) string {
		// Load a fresh copy of secrets (since they can be dynamically added).
		secrets := pctx.Secrets.List()

		s := fmt.Sprintf(format, a...)
		for _, secret := range secrets {
			s = strings.ReplaceAll(s, secret.PlainText(), "***")
		}
		return s
	}

	return progressui.PrintSolveStatus(ctx, ch,
		func(v *bk.Vertex, index int) {
			component, name := parseName(v)
			lg := log.
				Ctx(ctx).
				With().
				Str("task", component).
				Logger()

			lg.
				Debug().
				Msg(secureSprintf("#%d %s\n", index, name))
			lg.
				Debug().
				Msg(secureSprintf("#%d %s\n", index, v.Digest))
		},
		func(v *bk.Vertex, format string, a ...interface{}) {
			component, _ := parseName(v)
			lg := log.
				Ctx(ctx).
				With().
				Str("task", component).
				Logger()

			msg := secureSprintf(format, a...)
			lg.
				Debug().
				Msg(msg)
		},
		func(v *bk.Vertex, stream int, partial bool, format string, a ...interface{}) {
			component, _ := parseName(v)
			lg := log.
				Ctx(ctx).
				With().
				Str("task", component).
				Logger()

			msg := secureSprintf(format, a...)
			lg.
				Info().
				Msg(msg)
		},
	)
}
