package client

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/containerd/containerd/platforms"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/rs/zerolog/log"

	// Cue

	// buildkit
	bk "github.com/moby/buildkit/client"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the container connection driver
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"

	// docker output
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

type DoFunc func(context.Context, solver.Solver) error

// FIXME: return completed *Route, instead of *compiler.Value
func (c *Client) Do(ctx context.Context, pctx *plancontext.Context, localdirs map[string]string, fn DoFunc) error {
	lg := log.Ctx(ctx)
	eg, gctx := errgroup.WithContext(ctx)

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
		return c.buildfn(gctx, pctx, localdirs, fn, events)
	})

	return eg.Wait()
}

func (c *Client) buildfn(ctx context.Context, pctx *plancontext.Context, localdirs map[string]string, fn DoFunc, ch chan *bk.SolveStatus) error {
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

	// Catch solver's events
	// Closed manually
	eventsCh := make(chan *bk.SolveStatus)
	wg.Add(1)
	go catchOutput(eventsCh)

	// Catch build events
	// Closed by buildkit
	buildCh := make(chan *bk.SolveStatus)
	wg.Add(1)
	go catchOutput(buildCh)

	resp, err := c.c.Build(ctx, opts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		s := solver.New(solver.Opts{
			Control: c.c,
			Gateway: gw,
			Events:  eventsCh,
			Auth:    auth,
			NoCache: c.cfg.NoCache,
		})

		// Close events channel
		defer s.Stop()

		// Compute output overlay
		if fn != nil {
			if err := fn(ctx, s); err != nil {
				return nil, compiler.Err(err)
			}
		}

		ref, err := s.Solve(ctx, llb.Scratch(), platforms.DefaultSpec())
		if err != nil {
			return nil, err
		}
		res := bkgw.NewResult()
		res.SetRef(ref)
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
		// Pattern: `@name@ message`. Minimal length is len("@X@ ")
		if len(v.Name) < 2 || !strings.HasPrefix(v.Name, "@") {
			return "", v.Name
		}

		prefixEndPos := strings.Index(v.Name[1:], "@")
		if prefixEndPos == -1 {
			return "", v.Name
		}

		component := v.Name[1 : prefixEndPos+1]
		return component, v.Name[prefixEndPos+3 : len(v.Name)]
	}

	// Just like sprintf, but redacts secrets automatically
	secrets := pctx.Secrets.List()
	secureSprintf := func(format string, a ...interface{}) string {
		s := fmt.Sprintf(format, a...)
		for _, secret := range secrets {
			s = strings.ReplaceAll(s, secret.PlainText, "***")
		}
		return s
	}

	return progressui.PrintSolveStatus(ctx, ch,
		func(v *bk.Vertex, index int) {
			component, name := parseName(v)
			lg := log.
				Ctx(ctx).
				With().
				Str("component", component).
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
				Str("component", component).
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
				Str("component", component).
				Logger()

			msg := secureSprintf(format, a...)
			lg.
				Info().
				Msg(msg)
		},
	)
}
