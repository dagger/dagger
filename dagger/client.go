package dagger

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

	// Cue
	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	cueformat "cuelang.org/go/cue/format"

	// buildkit
	bk "github.com/moby/buildkit/client"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"

	// docker output
	"github.com/containerd/console"
	"github.com/moby/buildkit/util/progress/progressui"
)

const (
	defaultBuildkitHost = "docker-container://buildkitd"

	bkConfigKey = "context"
	bkInputKey  = ":dagger:input:"
	bkActionKey = ":dagger:action:"
)

type Client struct {
	c *bk.Client

	inputs    map[string]llb.State
	localdirs map[string]string

	BKFrontend bkgw.BuildFunc
}

func NewClient(ctx context.Context, host string) (*Client, error) {
	// buildkit client
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
		c:         c,
		inputs:    map[string]llb.State{},
		localdirs: map[string]string{},
	}, nil
}

func (c *Client) ConnectInput(target string, input interface{}) error {
	var st llb.State
	switch in := input.(type) {
	case llb.State:
		st = in
	case string:
		// Generate a random local input label for security
		st = c.AddLocalDir(in, target)
	default:
		return fmt.Errorf("unsupported input type")
	}
	c.inputs[bkInputKey+target] = st
	return nil
}

func (c *Client) AddLocalDir(dir, label string, opts ...llb.LocalOption) llb.State {
	c.localdirs[label] = dir
	return llb.Local(label, opts...)
}

// Set cue config for future calls.
// input can be:
//   - llb.State: valid cue config directory
//   - io.Reader: valid cue source
//   - string: local path to valid cue file or directory
//   - func(llb.State)llb.Stte: modify existing state

func (c *Client) SetConfig(inputs ...interface{}) error {
	for _, input := range inputs {
		if err := c.setConfig(input); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) setConfig(input interface{}) error {
	var st llb.State
	switch in := input.(type) {
	case llb.State:
		st = in
	case func(llb.State) llb.State:
		// Modify previous state
		last, ok := c.inputs[bkConfigKey]
		if !ok {
			last = llb.Scratch()
		}
		st = in(last)
	case io.Reader:
		contents, err := ioutil.ReadAll(in)
		if err != nil {
			return err
		}
		st = llb.Scratch().File(llb.Mkfile(
			"config.cue",
			0660,
			contents,
		))
	// Interpret string as a path (dir or file)
	case string:
		info, err := os.Stat(in)
		if err != nil {
			return err
		}
		if info.IsDir() {
			// FIXME: include pattern *.cue ooh yeah
			st = c.AddLocalDir(in, "config",
				//llb.IncludePatterns([]string{"*.cue", "cue.mod"})),
				llb.FollowPaths([]string{"*.cue", "cue.mod"}),
			)
		} else {
			f, err := os.Open(in)
			if err != nil {
				return err
			}
			defer f.Close()
			return c.SetConfig(f)
		}
	}
	c.inputs[bkConfigKey] = st
	return nil
}

func (c *Client) Run(ctx context.Context, action string) (*Output, error) {
	// Spawn Build() goroutine
	eg, ctx := errgroup.WithContext(ctx)
	events := make(chan *bk.SolveStatus)
	outr, outw := io.Pipe()
	// Spawn build function
	eg.Go(c.buildfn(ctx, action, events, outw))
	// Spawn print function(s)
	dispCtx := context.TODO()
	var eventsdup chan *bk.SolveStatus
	if os.Getenv("DOCKER_OUTPUT") != "" {
		eventsdup = make(chan *bk.SolveStatus)
		eg.Go(c.dockerprintfn(dispCtx, eventsdup, os.Stderr))
	}
	eg.Go(c.printfn(dispCtx, events, eventsdup))
	// Retrieve output
	out := NewOutput()
	eg.Go(c.outputfn(ctx, outr, out))
	return out, eg.Wait()
}

func (c *Client) buildfn(ctx context.Context, action string, ch chan *bk.SolveStatus, w io.WriteCloser) func() error {
	return func() error {
		defer debugf("buildfn complete")
		// Setup solve options
		opts := bk.SolveOpt{
			FrontendAttrs: map[string]string{
				bkActionKey: action,
			},
			LocalDirs:      c.localdirs,
			FrontendInputs: c.inputs,
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
		// Setup frontend
		bkFrontend := c.BKFrontend
		if bkFrontend == nil {
			r := &Runtime{}
			bkFrontend = r.BKFrontend
		}
		resp, err := c.c.Build(ctx, opts, "", bkFrontend, ch)
		if err != nil {
			// Close exporter pipe so that export processor can return
			w.Close()
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
func (c *Client) outputfn(ctx context.Context, r io.Reader, out *Output) func() error {
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
			// FIXME: only doing this for debug. you can pass tr directly as io.Reader.
			contents, err := ioutil.ReadAll(tr)
			if err != nil {
				return err
			}
			//if err := out.FillSource(h.Name, tr); err != nil {
			if err := out.FillSource(h.Name, contents); err != nil {
				debugf("error with %s: contents=\n------\n%s\n-----\n", h.Name, contents)
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
	var parts []cue.Selector
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
						debugf("ignoring buildkit vertex %q: not a valid cue path", p.String())
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
		return nil
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

type Output struct {
	r    *cue.Runtime
	inst *cue.Instance
}

func NewOutput() *Output {
	r := &cue.Runtime{}
	inst, _ := r.Compile("", "")
	return &Output{
		r:    r,
		inst: inst,
	}
}

func (o *Output) Print(w io.Writer) error {
	v := o.Cue().Value().Eval()
	b, err := cueformat.Node(v.Syntax())
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func (o *Output) JSON() JSON {
	return cueToJSON(o.Cue().Value())
}

func (o *Output) Cue() *cue.Instance {
	return o.inst
}

func (o *Output) FillSource(filename string, x interface{}) error {
	inst, err := o.r.Compile(filename, x)
	if err != nil {
		return fmt.Errorf("compile %s: %s", filename, cueerrors.Details(err, nil))
	}
	if err := o.FillValue(inst.Value()); err != nil {
		return fmt.Errorf("merge %s: %s", filename, cueerrors.Details(err, nil))
	}
	return nil
}

func (o *Output) FillValue(x interface{}) error {
	inst, err := o.inst.Fill(x)
	if err != nil {
		return err
	}
	if err := inst.Value().Validate(); err != nil {
		return err
	}
	o.inst = inst
	return nil
}
