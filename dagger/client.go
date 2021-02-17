package dagger

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

	"github.com/rs/zerolog/log"

	// Cue

	// buildkit
	bk "github.com/moby/buildkit/client"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the container connection driver
	bkgw "github.com/moby/buildkit/frontend/gateway/client"

	// docker output
	"github.com/containerd/console"
	"github.com/moby/buildkit/util/progress/progressui"

	"dagger.cloud/go/dagger/compiler"
)

const (
	defaultBuildkitHost = "docker-container://buildkitd"
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
		host = defaultBuildkitHost
	}
	c, err := bk.New(ctx, host)
	if err != nil {
		return nil, errors.Wrap(err, "buildkit client")
	}
	return &Client{
		c: c,
	}, nil
}

// FIXME: return completed *Env, instead of *compiler.Value
func (c *Client) Compute(ctx context.Context, env *Env) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	eg, gctx := errgroup.WithContext(ctx)

	// Spawn print function
	var events chan *bk.SolveStatus
	if os.Getenv("DOCKER_OUTPUT") != "" {
		events = make(chan *bk.SolveStatus)
		eg.Go(func() error {
			dispCtx := context.TODO()
			return c.dockerprintfn(dispCtx, events, lg)
		})
	}

	// Spawn build function
	outr, outw := io.Pipe()
	eg.Go(func() error {
		defer outw.Close()
		return c.buildfn(gctx, env, events, outw)
	})

	// Spawn output retriever
	var (
		out *compiler.Value
		err error
	)
	eg.Go(func() error {
		defer outr.Close()
		out, err = c.outputfn(gctx, outr)
		return err
	})

	return out, compiler.Err(eg.Wait())
}

func (c *Client) buildfn(ctx context.Context, env *Env, ch chan *bk.SolveStatus, w io.WriteCloser) error {
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

	resp, err := c.c.Build(ctx, opts, "", func(ctx context.Context, c bkgw.Client) (*bkgw.Result, error) {
		s := NewSolver(c)

		if err := env.Update(ctx, s); err != nil {
			return nil, err
		}
		lg.Debug().Msg("computing env")
		// Compute output overlay
		if err := env.Compute(ctx, s); err != nil {
			return nil, err
		}
		lg.Debug().Msg("exporting env")
		// Export env to a cue directory
		outdir, err := env.Export(s.Scratch())
		if err != nil {
			return nil, err
		}
		// Wrap cue directory in buildkit result
		return outdir.Result(ctx)
	}, ch)
	if err != nil {
		return errors.Wrap(bkCleanError(err), "buildkit solve")
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

	// FIXME: merge this into env output.
	out, err := compiler.EmptyStruct()
	if err != nil {
		return nil, err
	}

	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.Wrap(err, "read tar stream")
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
		if err := out.Fill(v); err != nil {
			return nil, errors.Wrap(err, h.Name)
		}
	}
	return out, nil
}

func (c *Client) dockerprintfn(ctx context.Context, ch chan *bk.SolveStatus, out io.Writer) error {
	var cons console.Console
	// FIXME: use smarter writer from blr
	return progressui.DisplaySolveStatus(ctx, "", cons, out, ch)
}
