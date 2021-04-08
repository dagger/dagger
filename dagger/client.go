package dagger

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	bkEntitlements "github.com/moby/buildkit/util/entitlements"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"golang.org/x/sync/errgroup"

	"github.com/opentracing/opentracing-go"
	"github.com/rs/zerolog/log"

	// Cue

	// buildkit
	bk "github.com/moby/buildkit/client"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the container connection driver
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"

	// docker output
	"dagger.io/go/pkg/buildkitd"
	"dagger.io/go/pkg/progressui"

	"dagger.io/go/dagger/compiler"
)

// A dagger client options
type ClientOpts struct {
	Host         string
	Entitlements []bkEntitlements.Entitlement
}

// A dagger client
type Client struct {
	c    *bk.Client
	opts *ClientOpts
}

// TODO Host = Client opts
func NewClient(ctx context.Context, clientOpts *ClientOpts) (*Client, error) {
	if clientOpts.Host == "" {
		clientOpts.Host = os.Getenv("BUILDKIT_HOST")
	}
	if clientOpts.Host == "" {
		h, err := buildkitd.Start(ctx)
		if err != nil {
			return nil, err
		}

		clientOpts.Host = h
	}
	opts := []bk.ClientOpt{}
	if span := opentracing.SpanFromContext(ctx); span != nil {
		opts = append(opts, bk.WithTracer(span.Tracer()))
	}
	c, err := bk.New(ctx, clientOpts.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("buildkit client: %w", err)
	}
	return &Client{
		c:    c,
		opts: clientOpts,
	}, nil
}

type ClientDoFunc func(context.Context, *Deployment, Solver) error

// FIXME: return completed *Route, instead of *compiler.Value
func (c *Client) Do(ctx context.Context, state *DeploymentState, fn ClientDoFunc) (*compiler.Value, error) {
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
	var out *compiler.Value
	eg.Go(func() error {
		defer outr.Close()
		out, err = c.outputfn(gctx, outr)
		return err
	})

	return out, eg.Wait()
}

func (c *Client) checkEntitlements(entitlements []bkEntitlements.Entitlement) error {
	check := func(entitlement bkEntitlements.Entitlement) error {
		for _, elem := range c.opts.Entitlements {
			if elem == entitlement {
				return nil
			}
		}
		return errors.New(fmt.Sprintf("Config require entitlement. To enable it use \"%v\"", entitlement))
	}

	for _, elem := range entitlements {
		if err := check(elem); err != nil {
			return err
		}
	}
	return nil
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
		AllowedEntitlements: c.opts.Entitlements,
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
			entitlements := deployment.Entitlements()

			// TODO Check if entitlements is necessary if fn  != nil
			if err := c.checkEntitlements(entitlements); err != nil {
				return nil, err
			}

			if err := fn(ctx, deployment, s); err != nil {
				return nil, compiler.Err(err)
			}
		}

		// Export deployment to a cue directory
		// FIXME: this should be elsewhere
		lg.Debug().Msg("exporting deployment")
		span, _ := opentracing.StartSpanFromContext(ctx, "Deployment.Export")
		defer span.Finish()

		stateSource, err := deployment.State().Source()
		if err != nil {
			return nil, compiler.Err(err)
		}

		st := llb.Scratch().File(
			llb.Mkfile("state.cue", 0600, stateSource),
			llb.WithCustomName("[internal] serializing state to CUE"),
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

// Read tar export stream from buildkit Build(), and extract cue output
func (c *Client) outputfn(ctx context.Context, r io.Reader) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	// FIXME: merge this into deployment output.
	out := compiler.EmptyStruct()

	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar stream: %w", err)
		}

		lg := lg.
			With().
			Str("file", h.Name).
			Logger()

		if !strings.HasSuffix(h.Name, ".cue") {
			lg.Debug().Msg("skipping non-cue file from exporter tar stream")
			continue
		}
		lg.Debug().Msg("outputfn: compiling & merging")

		v, err := compiler.Compile(h.Name, tr)
		if err != nil {
			return nil, err
		}
		if err := out.FillPath(cue.MakePath(), v); err != nil {
			return nil, fmt.Errorf("%s: %w", h.Name, compiler.Err(err))
		}
	}
	return out, nil
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
