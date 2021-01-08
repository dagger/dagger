package dagger

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

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

	defaultBootDir = "."

	// FIXME: rename to defaultConfig ?
	defaultBootScript = `
bootdir: string | *"."
bootscript: [
	{
		do: "local"
		dir: bootdir
		include: ["*.cue", "cue.mod"]
	},
]
`
)

type Client struct {
	c *bk.Client

	localdirs map[string]string
	boot      string
	bootdir   string
	input     string
}

func NewClient(ctx context.Context, host, boot, bootdir string) (*Client, error) {
	// buildkit client
	if host == "" {
		host = os.Getenv("BUILDKIT_HOST")
	}
	if host == "" {
		host = defaultBuildkitHost
	}
	if boot == "" {
		boot = defaultBootScript
	}
	if bootdir == "" {
		bootdir = defaultBootDir
	}
	c, err := bk.New(ctx, host)
	if err != nil {
		return nil, errors.Wrap(err, "buildkit client")
	}
	return &Client{
		c:         c,
		boot:      boot,
		bootdir:   bootdir,
		input:     `{}`,
		localdirs: map[string]string{},
	}, nil
}

func (c *Client) LocalDirs() ([]string, error) {
	boot, err := c.BootScript()
	if err != nil {
		return nil, err
	}
	return boot.LocalDirs()
}

func (c *Client) BootScript() (*Script, error) {
	debugf("compiling boot script: %q\n", c.boot)
	cc := &Compiler{}
	src, err := cc.Compile("boot.cue", c.boot)
	if err != nil {
		return nil, errors.Wrap(err, "compile")
	}
	src, err = src.MergeTarget(c.bootdir, "bootdir")
	if err != nil {
		return nil, err
	}
	return src.Get("bootscript").Script()
}

func (c *Client) Compute(ctx context.Context) (*Value, error) {
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
	eg.Go(c.buildfn(ctx, events, outw))
	// Spawn print function(s)
	dispCtx := context.TODO()
	var eventsdup chan *bk.SolveStatus
	if os.Getenv("DOCKER_OUTPUT") != "" {
		eventsdup = make(chan *bk.SolveStatus)
		eg.Go(c.dockerprintfn(dispCtx, eventsdup, os.Stderr))
	}
	eg.Go(c.printfn(dispCtx, events, eventsdup))
	// Retrieve output
	eg.Go(c.outputfn(ctx, outr, out))
	return out, eg.Wait()
}

func (c *Client) buildfn(ctx context.Context, ch chan *bk.SolveStatus, w io.WriteCloser) func() error {
	return func() (err error) {
		defer func() {
			debugf("buildfn complete, err=%q", err)
			if err != nil {
				// Close exporter pipe so that export processor can return
				w.Close()
			}
		}()
		boot, err := c.BootScript()
		if err != nil {
			close(ch)
			return errors.Wrap(err, "assemble boot script")
		}
		bootSource, err := boot.Value().Source()
		if err != nil {
			close(ch)
			return errors.Wrap(err, "serialize boot script")
		}
		// Setup solve options
		opts := bk.SolveOpt{
			FrontendAttrs: map[string]string{
				bkInputKey: c.input,
				bkBootKey:  string(bootSource),
			},
			LocalDirs: map[string]string{},
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
		// Connect local dirs
		localdirs, err := c.LocalDirs()
		if err != nil {
			close(ch)
			return errors.Wrap(err, "connect local dirs")
		}
		for _, dir := range localdirs {
			opts.LocalDirs[dir] = dir
		}
		// Call buildkit solver
		resp, err := c.c.Build(ctx, opts, "", Compute, ch)
		if err != nil {
			err = errors.New(bkCleanError(err.Error()))
			return errors.Wrap(err, "buildkit solve")
		}
		for k, v := range resp.ExporterResponse {
			// FIXME consume exporter response
			fmt.Printf("exporter response: %s=%s\n", k, v)
		}
		return nil
	}
}

// Read tar export stream from buildkit Build(), and extract cue output
func (c *Client) outputfn(_ context.Context, r io.Reader, out *Value) func() error {
	return func() error {
		defer debugf("outputfn complete")
		tr := tar.NewReader(r)
		for {
			debugf("outputfn: reading next tar entry")
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return errors.Wrap(err, "read tar stream")
			}
			if !strings.HasSuffix(h.Name, ".cue") {
				debugf("skipping non-cue file from exporter tar stream: %s", h.Name)
				continue
			}
			debugf("outputfn: compiling & merging %q", h.Name)

			cc := out.Compiler()
			v, err := cc.Compile(h.Name, tr)
			if err != nil {
				return err
			}
			if err := out.Fill(v); err != nil {
				return errors.Wrap(err, h.Name)
			}
			debugf("outputfn: DONE: compiling & merging %q", h.Name)
		}
		return nil
	}
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

func (n Node) Logf(msg string, args ...interface{}) {
	componentPath := n.ComponentPath().String()
	args = append([]interface{}{componentPath}, args...)
	if msg != "" && !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	fmt.Fprintf(os.Stderr, "[%s] "+msg, args...)
}

func (n Node) LogStream(nStream int, data []byte) {
	var stream string
	switch nStream {
	case 1:
		stream = "stdout"
	case 2:
		stream = "stderr"
	default:
		stream = fmt.Sprintf("%d", nStream)
	}
	// FIXME: use bufio reader?
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		n.Logf("[%s] %s", stream, line)
	}
}

func (n Node) LogError(errmsg string) {
	n.Logf("ERROR: %s", bkCleanError(errmsg))
}

func (c *Client) printfn(ctx context.Context, ch, ch2 chan *bk.SolveStatus) func() error {
	return func() error {
		// Node status mapped to buildkit vertex digest
		nodesByDigest := map[string]*Node{}
		// Node status mapped to cue path
		nodesByPath := map[string]*Node{}

		defer debugf("printfn complete")
		if ch2 != nil {
			defer close(ch2)
		}
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			case status, ok := <-ch:
				if !ok {
					return nil
				}
				if ch2 != nil {
					ch2 <- status
				}
				debugf("status event: vertexes:%d statuses:%d logs:%d\n",
					len(status.Vertexes),
					len(status.Statuses),
					len(status.Logs),
				)
				for _, v := range status.Vertexes {
					p := cue.ParsePath(v.Name)
					if err := p.Err(); err != nil {
						debugf("ignoring buildkit vertex %q: not a valid cue path", v.Name)
						continue
					}
					n := &Node{
						Path:   p,
						Vertex: v,
					}
					nodesByPath[n.Path.String()] = n
					nodesByDigest[n.Digest.String()] = n
					if n.Error != "" {
						n.LogError(n.Error)
					}
				}
				for _, log := range status.Logs {
					if n, ok := nodesByDigest[log.Vertex.String()]; ok {
						n.LogStream(log.Stream, log.Data)
					}
				}
				// debugJSON(status)
				// FIXME: callbacks for extracting stream/result
				// see proto 67
			}
		}
	}
}

// A helper to remove noise from buildkit error messages.
// FIXME: Obviously a cleaner solution would be nice.
func bkCleanError(msg string) string {
	noise := []string{
		"executor failed running ",
		"buildkit-runc did not terminate successfully",
		"rpc error: code = Unknown desc =",
		"failed to solve: ",
	}
	for _, s := range noise {
		msg = strings.Replace(msg, s, "", -1)
	}
	return msg
}

func (c *Client) dockerprintfn(ctx context.Context, ch chan *bk.SolveStatus, out io.Writer) func() error {
	return func() error {
		defer debugf("dockerprintfn complete")
		var cons console.Console
		// FIXME: use smarter writer from blr
		return progressui.DisplaySolveStatus(ctx, "", cons, out, ch)
	}
}
