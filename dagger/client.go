package dagger

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
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

	bkBootKey  = "boot"
	bkInputKey = "input"

	// Base client config, for default values & schema validation.
	baseClientConfig = `
	close({
		bootdir: string | *"."
		boot: [...{do:string,...}] | *[
			{
				do: "local"
				dir: bootdir
				include: ["*.cue", "cue.mod"]
			}
		]
	})
	`
)

type Client struct {
	c *bk.Client

	localdirs map[string]string
	cfg       ClientConfig
}

type ClientConfig struct {
	// Buildkit host address, eg. `docker://buildkitd`
	Host string
	// Env boot script, eg. `[{do:"local",dir:"."}]`
	Boot string
	// Env boot dir, eg. `.`
	// May be referenced by boot script.
	BootDir string
	// Input overlay, eg. `www: source: #dagger: compute: [{do:"local",dir:"./src"}]`
	Input string
}

func NewClient(ctx context.Context, cfg ClientConfig) (result *Client, err error) {
	defer func() {
		if err != nil {
			// Expand cue errors to get full details
			err = cueErr(err)
		}
	}()
	// Finalize config values
	localdirs, err := (&cfg).Finalize(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "client config")
	}
	log.Ctx(ctx).Debug().
		Interface("cfg", cfg).
		Interface("localdirs", localdirs).
		Msg("finalized client config")
	c, err := bk.New(ctx, cfg.Host)
	if err != nil {
		return nil, errors.Wrap(err, "buildkit client")
	}
	return &Client{
		c:         c,
		cfg:       cfg,
		localdirs: localdirs,
	}, nil
}

// Compile config, fill in final values,
// and return a rollup of local directories
// referenced in the config.
// Localdirs may be referenced in 2 places:
//  1. Boot script
//  2. Input overlay (FIXME: scan not yet implemented)
func (cfg *ClientConfig) Finalize(ctx context.Context) (map[string]string, error) {
	localdirs := map[string]string{}
	// buildkit client
	if cfg.Host == "" {
		cfg.Host = os.Getenv("BUILDKIT_HOST")
	}
	if cfg.Host == "" {
		cfg.Host = defaultBuildkitHost
	}
	// Compile cue template for boot script & boot dir
	// (using cue because script may reference dir)
	v, err := cfg.Compile()
	if err != nil {
		return nil, errors.Wrap(err, "invalid client config")
	}
	// Finalize boot script
	boot, err := v.Get("boot").Script()
	if err != nil {
		return nil, errors.Wrap(err, "invalid env boot script")
	}
	cfg.Boot = string(boot.Value().JSON())
	// Scan boot script for references to local dirs, to grant access.
	bootLocalDirs, err := boot.LocalDirs(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "scan boot script for local dir access")
	}
	// Finalize boot dir
	cfg.BootDir, err = v.Get("bootdir").String()
	if err != nil {
		return nil, errors.Wrap(err, "invalid env boot dir")
	}
	// Scan boot script for references to local dirs, to grant access.
	for _, dir := range bootLocalDirs {
		// FIXME: randomize local dir references for security
		// (currently a malicious cue package may guess common local paths
		//  and access the corresponding host directory)
		localdirs[dir] = dir
	}
	// FIXME: scan input overlay for references to local dirs, to grant access.
	// See issue #41
	return localdirs, nil
}

// Compile client config to a cue value
// FIXME: include host and input.
func (cfg ClientConfig) Compile() (v *Value, err error) {
	cc := &Compiler{}
	v, err = cc.Compile("client.cue", baseClientConfig)
	if err != nil {
		return nil, errors.Wrap(err, "base client config")
	}
	if cfg.BootDir != "" {
		v, err = v.Merge(cfg.BootDir, "bootdir")
		if err != nil {
			return nil, errors.Wrap(err, "client config key 'bootdir'")
		}
	}
	if cfg.Boot != "" {
		v, err = v.Merge(cfg.Boot, "boot")
		if err != nil {
			return nil, errors.Wrap(err, "client config key 'boot'")
		}
	}
	return v, nil
}

func (c *Client) Compute(ctx context.Context) (*Value, error) {
	lg := log.Ctx(ctx)

	cc := &Compiler{}
	out, err := cc.EmptyStruct()
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
		return c.buildfn(ctx, events, outw)
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
		return c.outputfn(ctx, outr, out, cc)
	})
	return out, eg.Wait()
}

func (c *Client) buildfn(ctx context.Context, ch chan *bk.SolveStatus, w io.WriteCloser) error {
	lg := log.Ctx(ctx)

	// Setup solve options
	opts := bk.SolveOpt{
		FrontendAttrs: map[string]string{
			bkInputKey: c.cfg.Input,
			bkBootKey:  c.cfg.Boot,
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
		Interface("host", c.cfg.Host).
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
