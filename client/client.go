package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/opentracing/opentracing-go"
	"github.com/rs/zerolog/log"

	// Cue

	// buildkit
	bk "github.com/moby/buildkit/client"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the container connection driver
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"

	// docker output
	"go.dagger.io/dagger/util/buildkitd"
	"go.dagger.io/dagger/util/progressui"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"
)

// A dagger client
type Client struct {
	c       *bk.Client
	noCache bool
}

func New(ctx context.Context, host string, noCache bool) (*Client, error) {
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
	if span := opentracing.SpanFromContext(ctx); span != nil {
		opts = append(opts, bk.WithTracer(span.Tracer()))
	}
	c, err := bk.New(ctx, host, opts...)
	if err != nil {
		return nil, fmt.Errorf("buildkit client: %w", err)
	}
	return &Client{
		c:       c,
		noCache: noCache,
	}, nil
}

type DoFunc func(context.Context, *environment.Environment, solver.Solver) error

// FIXME: return completed *Route, instead of *compiler.Value
func (c *Client) Do(ctx context.Context, state *state.State, fn DoFunc) (*environment.Environment, error) {
	lg := log.Ctx(ctx)
	eg, gctx := errgroup.WithContext(ctx)

	environment, err := environment.New(state)
	if err != nil {
		return nil, err
	}

	// Spawn print function
	events := make(chan *bk.SolveStatus)
	eg.Go(func() error {
		// Create a background context so that logging will not be cancelled
		// with the main context.
		dispCtx := lg.WithContext(context.Background())
		return c.logSolveStatus(dispCtx, state, events)
	})

	// Spawn build function
	eg.Go(func() error {
		return c.buildfn(gctx, state, environment, fn, events)
	})

	return environment, eg.Wait()
}

func (c *Client) buildfn(ctx context.Context, st *state.State, env *environment.Environment, fn DoFunc, ch chan *bk.SolveStatus) error {
	lg := log.Ctx(ctx)

	// Scan local dirs to grant access
	localdirs := env.LocalDirs()
	for label, dir := range localdirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		localdirs[label] = abs
	}

	// buildkit auth provider (registry)
	auth := solver.NewRegistryAuthProvider()

	// secrets
	secrets := solver.NewSecretsProvider(st)

	// Setup solve options
	opts := bk.SolveOpt{
		LocalDirs: localdirs,
		Session:   []session.Attachable{auth, secrets},
	}

	// Call buildkit solver
	lg.Debug().
		Interface("localdirs", opts.LocalDirs).
		Interface("attrs", opts.FrontendAttrs).
		Msg("spawning buildkit job")

	resp, err := c.c.Build(ctx, opts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		s := solver.New(solver.Opts{
			Control: c.c,
			Gateway: gw,
			Events:  ch,
			Auth:    auth,
			Secrets: secrets,
			NoCache: c.noCache,
		})

		lg.Debug().Msg("loading configuration")
		if err := env.LoadPlan(ctx, s); err != nil {
			return nil, err
		}

		// Compute output overlay
		if fn != nil {
			if err := fn(ctx, env, s); err != nil {
				return nil, compiler.Err(err)
			}
		}

		// Export environment to a cue directory
		// FIXME: this should be elsewhere
		lg.Debug().Msg("exporting environment")
		span, _ := opentracing.StartSpanFromContext(ctx, "Environment.Export")
		defer span.Finish()

		computed := env.Computed().JSON().PrettyString()
		st := llb.
			Scratch().
			File(
				llb.Mkfile("computed.json", 0600, []byte(computed)),
				llb.WithCustomName("[internal] serializing computed values"),
			)

		ref, err := s.Solve(ctx, st)
		if err != nil {
			return nil, err
		}
		res := bkgw.NewResult()
		res.SetRef(ref)
		return res, nil
	}, ch)
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

func (c *Client) logSolveStatus(ctx context.Context, st *state.State, ch chan *bk.SolveStatus) error {
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
	secureSprintf := func(format string, a ...interface{}) string {
		s := fmt.Sprintf(format, a...)
		for _, i := range st.Inputs {
			if i.Secret == nil {
				continue
			}
			s = strings.ReplaceAll(s, i.Secret.PlainText(), "***")
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
			switch stream {
			case 1:
				lg.
					Info().
					Msg(msg)
			case 2:
				lg.
					Error().
					Msg(msg)
			}
		},
	)
}
