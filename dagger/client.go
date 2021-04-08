package dagger

import (
	"context"
	"fmt"
	"io"
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
	bkgw "github.com/moby/buildkit/frontend/gateway/client"

	// docker output
	"dagger.io/go/pkg/buildkitd"
	"dagger.io/go/pkg/progressui"

	"dagger.io/go/dagger/compiler"
)

// A dagger client
type Client struct {
	c *bk.Client
}

func NewClient(ctx context.Context, host string) (*Client, error) {
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
		c: c,
	}, nil
}

type ClientDoFunc func(context.Context, *Deployment, Solver) error

// FIXME: return completed *Route, instead of *compiler.Value
func (c *Client) Do(ctx context.Context, state *DeploymentState, fn ClientDoFunc) (*DeploymentResult, error) {
	lg := log.Ctx(ctx)
	eg, gctx := errgroup.WithContext(ctx)

	deployment, err := NewDeployment(state)
	if err != nil {
		return nil, err
	}

	// Spawn print function
	events := make(chan *bk.SolveStatus)
	eg.Go(func() error {
		// Create a background context so that logging will not be cancelled
		// with the main context.
		dispCtx := lg.WithContext(context.Background())
		return c.logSolveStatus(dispCtx, events)
	})

	// Spawn build function
	outr, outw := io.Pipe()
	eg.Go(func() error {
		defer outw.Close()
		return c.buildfn(gctx, deployment, fn, events, outw)
	})

	// Spawn output retriever
	var result *DeploymentResult
	eg.Go(func() error {
		defer outr.Close()

		result, err = ReadDeploymentResult(gctx, outr)
		return err
	})

	return result, eg.Wait()
}

func (c *Client) buildfn(ctx context.Context, deployment *Deployment, fn ClientDoFunc, ch chan *bk.SolveStatus, w io.WriteCloser) error {
	lg := log.Ctx(ctx)

	// Scan local dirs to grant access
	localdirs := deployment.LocalDirs()
	for label, dir := range localdirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		localdirs[label] = abs
	}

	// Setup solve options
	opts := bk.SolveOpt{
		LocalDirs: localdirs,
		// FIXME: catch output & return as cue value
		Exports: []bk.ExportEntry{
			{
				Type: bk.ExporterTar,
				Output: func(m map[string]string) (io.WriteCloser, error) {
					return w, nil
				},
			},
		},
	}

	// Call buildkit solver
	lg.Debug().
		Interface("localdirs", opts.LocalDirs).
		Interface("attrs", opts.FrontendAttrs).
		Msg("spawning buildkit job")

	resp, err := c.c.Build(ctx, opts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		s := NewSolver(c.c, gw, ch)

		lg.Debug().Msg("loading configuration")
		if err := deployment.LoadPlan(ctx, s); err != nil {
			return nil, err
		}

		// Compute output overlay
		if fn != nil {
			if err := fn(ctx, deployment, s); err != nil {
				return nil, compiler.Err(err)
			}
		}

		// Export deployment to a cue directory
		// FIXME: this should be elsewhere
		lg.Debug().Msg("exporting deployment")
		span, _ := opentracing.StartSpanFromContext(ctx, "Deployment.Export")
		defer span.Finish()

		result := deployment.Result()
		st, err := result.ToLLB()
		if err != nil {
			return nil, err
		}

		ref, err := s.Solve(ctx, st)
		if err != nil {
			return nil, err
		}
		res := bkgw.NewResult()
		res.SetRef(ref)
		return res, nil
	}, ch)
	if err != nil {
		return fmt.Errorf("buildkit solve: %w", bkCleanError(err))
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

func (c *Client) logSolveStatus(ctx context.Context, ch chan *bk.SolveStatus) error {
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
				Msg(fmt.Sprintf("#%d %s\n", index, name))
			lg.
				Debug().
				Msg(fmt.Sprintf("#%d %s\n", index, v.Digest))
		},
		func(v *bk.Vertex, format string, a ...interface{}) {
			component, _ := parseName(v)
			lg := log.
				Ctx(ctx).
				With().
				Str("component", component).
				Logger()

			lg.
				Debug().
				Msg(fmt.Sprintf(format, a...))
		},
		func(v *bk.Vertex, stream int, partial bool, format string, a ...interface{}) {
			component, _ := parseName(v)
			lg := log.
				Ctx(ctx).
				With().
				Str("component", component).
				Logger()

			switch stream {
			case 1:
				lg.
					Info().
					Msg(fmt.Sprintf(format, a...))
			case 2:
				lg.
					Error().
					Msg(fmt.Sprintf(format, a...))
			}
		},
	)
}
