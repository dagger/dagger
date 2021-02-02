package dagger

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

	"github.com/rs/zerolog/log"

	// Cue
	"cuelang.org/go/cue"

	// buildkit
	bk "github.com/moby/buildkit/client"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the container connection driver

	// docker output
	"github.com/containerd/console"
	"github.com/moby/buildkit/util/progress/progressui"
)

const (
	defaultBuildkitHost = "docker-container://buildkitd"
	bkUpdaterKey        = "updater"
	bkInputKey          = "input"
)

// A dagger client
type Client struct {
	c *bk.Client

	localdirs map[string]string
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
	// Scan local dirs to grant access
	localdirs, err := env.LocalDirs(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "scan local dirs")
	}
	for label, dir := range localdirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		localdirs[label] = abs
	}
	c.localdirs = localdirs

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

	// Spawn print function(s)
	dispCtx := context.TODO()
	if os.Getenv("DOCKER_OUTPUT") != "" {
		// Multiplex events
		eventsPrint := make(chan *bk.SolveStatus)
		eventsDockerPrint := make(chan *bk.SolveStatus)
		eg.Go(func() error {
			defer close(eventsPrint)
			defer close(eventsDockerPrint)

			for e := range events {
				eventsPrint <- e
				eventsDockerPrint <- e
			}
			return nil
		})

		eg.Go(func() error {
			return c.printfn(dispCtx, eventsPrint)
		})

		eg.Go(func() error {
			return c.dockerprintfn(dispCtx, eventsDockerPrint, lg)
		})
	} else {
		eg.Go(func() error {
			return c.printfn(dispCtx, events)
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

	// Serialize input and updater
	input, err := env.Input().SourceString()
	if err != nil {
		return errors.Wrap(err, "serialize env input")
	}
	updater, err := env.Updater().Value().SourceString()
	if err != nil {
		return errors.Wrap(err, "serialize updater script")
	}
	// Setup solve options
	opts := bk.SolveOpt{
		FrontendAttrs: map[string]string{
			bkInputKey:   input,
			bkUpdaterKey: updater,
		},
		LocalDirs: c.localdirs,
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
	resp, err := c.c.Build(ctx, opts, "", Compute, ch)
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

// Status of a node in the config tree being computed
// Node may be a component, or a value within a component
// (eg. a script or individual operation in a script)
type Node struct {
	Path cue.Path
	*bk.Vertex
}

func (n Node) ComponentPath() cue.Path {
	parts := []cue.Selector{}
	for _, sel := range n.Path.Selectors() {
		if strings.HasPrefix(sel.String(), "#") {
			break
		}
		parts = append(parts, sel)
	}
	return cue.MakePath(parts...)
}

func (n Node) Logf(ctx context.Context, msg string, args ...interface{}) {
	componentPath := n.ComponentPath().String()
	args = append([]interface{}{componentPath}, args...)
	if msg != "" && !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	fmt.Fprintf(os.Stderr, "[%s] "+msg, args...)
}

func (n Node) LogStream(ctx context.Context, nStream int, data []byte) {
	lg := log.
		Ctx(ctx).
		With().
		Str("path", n.ComponentPath().String()).
		Logger()

	switch nStream {
	case 1:
		lg = lg.With().Str("stream", "stdout").Logger()
	case 2:
		lg = lg.With().Str("stream", "stderr").Logger()
	default:
		lg = lg.With().Str("stream", fmt.Sprintf("%d", nStream)).Logger()
	}

	lg.Debug().Msg(string(data))
}

func (n Node) LogError(ctx context.Context, errmsg string) {
	log.
		Ctx(ctx).
		Error().
		Str("path", n.ComponentPath().String()).
		Msg(errmsg)
}

func (c *Client) printfn(ctx context.Context, ch chan *bk.SolveStatus) error {
	lg := log.Ctx(ctx)

	// Node status mapped to buildkit vertex digest
	nodesByDigest := map[string]*Node{}
	// Node status mapped to cue path
	nodesByPath := map[string]*Node{}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case status, ok := <-ch:
			if !ok {
				return nil
			}
			lg.
				Debug().
				Int("vertexes", len(status.Vertexes)).
				Int("statuses", len(status.Statuses)).
				Int("logs", len(status.Logs)).
				Msg("status event")

			for _, v := range status.Vertexes {
				// FIXME: insert raw buildkit telemetry here (ie for debugging, etc.)

				// IF a buildkit vertex has a valid cue path as name, extract additional info:
				p := cue.ParsePath(v.Name)
				if err := p.Err(); err != nil {
					// Not a valid cue path: skip.
					continue
				}
				n := &Node{
					Path:   p,
					Vertex: v,
				}
				nodesByPath[n.Path.String()] = n
				nodesByDigest[n.Digest.String()] = n
				if n.Error != "" {
					n.LogError(ctx, n.Error)
				}
			}
			for _, log := range status.Logs {
				if n, ok := nodesByDigest[log.Vertex.String()]; ok {
					n.LogStream(ctx, log.Stream, log.Data)
				}
			}
			// debugJSON(status)
			// FIXME: callbacks for extracting stream/result
			// see proto 67
		}
	}
}

func (c *Client) dockerprintfn(ctx context.Context, ch chan *bk.SolveStatus, out io.Writer) error {
	var cons console.Console
	// FIXME: use smarter writer from blr
	return progressui.DisplaySolveStatus(ctx, "", cons, out, ch)
}
