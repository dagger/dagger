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

// FIXME: return completed *Env, instead of *Value
func (c *Client) Compute(ctx context.Context, env *Env) (o *Value, err error) {
	lg := log.Ctx(ctx)
	defer func() {
		if err != nil {
			// Expand cue errors to get full details
			err = cueErr(err)
		}
	}()

	// FIXME: merge this into env output.
	out, err := env.Compiler().EmptyStruct()
	if err != nil {
		return nil, err
	}

	// Spawn Build() goroutine
	eg, ctx := errgroup.WithContext(ctx)
	events := make(chan *bk.SolveStatus)
	outr, outw := io.Pipe()

	// Spawn build function
	eg.Go(func() error {
		defer outw.Close()
		return c.buildfn(ctx, env, events, outw)
	})

	// Spawn print function
	if os.Getenv("DOCKER_OUTPUT") != "" {
		eg.Go(func() error {
			dispCtx := context.TODO()
			return c.dockerprintfn(dispCtx, events, lg)
		})
	}

	// Retrieve output
	eg.Go(func() error {
		defer outr.Close()
		return c.outputfn(ctx, outr, out, env.cc)
	})
	return out, eg.Wait()
}

func (c *Client) buildfn(ctx context.Context, env *Env, ch chan *bk.SolveStatus, w io.WriteCloser) error {
	lg := log.Ctx(ctx)

	// Scan local dirs to grant access
	localdirs, err := env.LocalDirs(ctx)
	if err != nil {
		return errors.Wrap(err, "scan local dirs")
	}
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
func (c *Client) outputfn(ctx context.Context, r io.Reader, out *Value, cc *Compiler) error {
	lg := log.Ctx(ctx)

	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.Wrap(err, "read tar stream")
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

		v, err := cc.Compile(h.Name, tr)
		if err != nil {
			return err
		}
		if err := out.Fill(v); err != nil {
			return errors.Wrap(err, h.Name)
		}
	}
	return nil
}

func (c *Client) dockerprintfn(ctx context.Context, ch chan *bk.SolveStatus, out io.Writer) error {
	var cons console.Console
	// FIXME: use smarter writer from blr
	return progressui.DisplaySolveStatus(ctx, "", cons, out, ch)
}
