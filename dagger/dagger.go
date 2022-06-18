package dagger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/progress/progressui"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/sync/errgroup"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
)

type FS struct {
	// TODO: don't like this being public, but needed for core actions and for marshal
	Def *pb.Definition `json:"def,omitempty"`
	// ref is set lazily if Evaluate, ReadFile or similar APIs are called
	ref bkgw.Reference
}

func (fs *FS) setRef(ctx *Context) error {
	// TODO: singleflight
	res, err := ctx.client.Solve(ctx.ctx, bkgw.SolveRequest{
		Definition: fs.Def,
	})
	if err != nil {
		return err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return err
	}
	fs.ref = ref
	return nil
}

// Evaluate synchronously instantiates the filesystem, blocking until it is created
func (fs *FS) Evaluate(ctx *Context) error {
	if err := fs.setRef(ctx); err != nil {
		return err
	}
	// TODO: sort of silly, more efficient would be to call Solve w/ Evaluate=true when ref is nil, but
	// need to be careful that if ref is non-nil it may or may not be lazy still server-side
	_, err := fs.ref.StatFile(ctx.ctx, bkgw.StatRequest{Path: "."})
	if err != nil {
		return err
	}
	return nil
}

func (fs *FS) ReadFile(ctx *Context, path string) ([]byte, error) {
	if err := fs.setRef(ctx); err != nil {
		return nil, err
	}
	return fs.ref.ReadFile(ctx.ctx, bkgw.ReadRequest{
		Filename: path,
	})
}

type Secret struct{}

type Context struct {
	ctx    context.Context
	client bkgw.Client
}

func DummyRun(ctx *Context, cmd string) error {
	st := llb.Image("alpine").Run(llb.Shlex(cmd)).Root()

	def, err := st.Marshal(ctx.ctx, llb.LinuxArm64)
	if err != nil {
		return err
	}

	// call solve
	_, err = ctx.client.Solve(ctx.ctx, bkgw.SolveRequest{
		Definition: def.ToPB(),
		Evaluate:   true,
	})
	if err != nil {
		return err
	}

	return nil
}

func Marshal(ctx *Context, v any) (FS, error) {
	var bytes []byte
	switch v := v.(type) {
	case string:
		bytes = []byte(v)
	default:
		var err error
		bytes, err = json.Marshal(v)
		if err != nil {
			return FS{}, err
		}
	}
	return Solve(ctx, llb.Scratch().
		File(llb.Mkfile("/dagger.json", 0644, bytes)))
}

func Unmarshal(ctx *Context, fs FS, v any) error {
	bytes, err := fs.ReadFile(ctx, "/dagger.json")
	if err != nil {
		return err
	}
	switch v := v.(type) {
	case *string:
		*v = string(bytes)
	default:
		return json.Unmarshal(bytes, v)
	}
	return nil
}

func Solve(ctx *Context, st llb.State) (FS, error) {
	def, err := st.Marshal(ctx.ctx) // TODO: options
	if err != nil {
		return FS{}, err
	}
	res, err := ctx.client.Solve(ctx.ctx, bkgw.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return FS{}, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return FS{}, err
	}
	return FS{Def: def.ToPB(), ref: ref}, nil
}

func Do(ctx *Context, pkg, action string, input FS) (FS, error) {
	res, err := ctx.client.Solve(ctx.ctx, bkgw.SolveRequest{
		Frontend: "gateway.v0",
		FrontendOpt: map[string]string{
			"source": pkg, // TODO: put these in the input?
			"action": action,
		},
		FrontendInputs: map[string]*pb.Definition{"dagger": input.Def},
	})
	if err != nil {
		return FS{}, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return FS{}, err
	}
	st, err := ref.ToState()
	if err != nil {
		return FS{}, err
	}
	def, err := st.Marshal(ctx.ctx)
	if err != nil {
		return FS{}, err
	}
	return FS{Def: def.ToPB(), ref: ref}, nil
}

// Starts an alpine shell with "fs" mounted at /output
func Shell(ctx *Context, fs FS) error {
	base, err := Solve(ctx, llb.Image("alpine:3.15"))
	if err != nil {
		return err
	}

	if err := base.Evaluate(ctx); err != nil {
		return err
	}

	if err := fs.Evaluate(ctx); err != nil {
		return err
	}

	ctr, err := ctx.client.NewContainer(ctx.ctx, bkgw.NewContainerRequest{
		Mounts: []bkgw.Mount{
			{
				Dest:      "/",
				Ref:       base.ref,
				MountType: pb.MountType_BIND,
			},
			{
				Dest:      "/output",
				Ref:       fs.ref,
				MountType: pb.MountType_BIND,
			},
		},
	})
	if err != nil {
		return err
	}
	proc, err := ctr.Start(ctx.ctx, bkgw.StartRequest{
		Args:   []string{"/bin/sh"},
		Cwd:    "/output",
		Tty:    true,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	if err != nil {
		return err
	}
	termState, err := terminal.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer terminal.Restore(int(os.Stdin.Fd()), termState)
	return proc.Wait()
}

func Client(fn func(*Context) error) error {
	ctx := context.TODO()
	c, err := bkclient.New(ctx, "docker-container://dagger-buildkitd", bkclient.WithFailFast())
	if err != nil {
		return err
	}

	ch := make(chan *bkclient.SolveStatus)

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		var err error
		_, err = c.Build(ctx, bkclient.SolveOpt{}, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			c := &Context{
				ctx:    ctx,
				client: gw,
			}

			err := fn(c)
			if err != nil {
				return nil, err
			}

			return bkgw.NewResult(), nil
		}, ch)
		return err
	})
	eg.Go(func() error {
		/* TODO:
		c, err := console.ConsoleFromFile(os.Stderr)
		if err != nil {
			return err
		}
		warn, err := progressui.DisplaySolveStatus(context.TODO(), "", c, os.Stdout, ch)
		*/
		warn, err := progressui.DisplaySolveStatus(context.TODO(), "", nil, os.Stdout, ch)
		for _, w := range warn {
			fmt.Printf("=> %s\n", w.Short)
		}
		return err
	})
	if err := eg.Wait(); err != nil {
		return err
	}
	return nil
}

func New() *Package {
	return &Package{
		actions: make(map[string]ActionFunc),
	}
}

type Package struct {
	actions map[string]ActionFunc
}

func (p *Package) Serve() error {
	return grpcclient.RunFromEnvironment(appcontext.Context(), func(ctx context.Context, c bkgw.Client) (*bkgw.Result, error) {
		dctx := &Context{
			ctx:    ctx,
			client: c,
		}

		opts := c.BuildOpts().Opts

		action := opts["action"]
		fn := p.actions[action]

		inputStates, err := c.Inputs(ctx)
		if err != nil {
			return nil, err
		}
		inputState, ok := inputStates["dagger"]
		if !ok {
			return nil, fmt.Errorf("missing dagger frontend input")
		}
		inputFS, err := Solve(dctx, inputState)
		if err != nil {
			return nil, err
		}

		outputFS, err := fn(dctx, inputFS)
		if err != nil {
			return nil, err
		}

		res := bkgw.NewResult()
		res.SetRef(outputFS.ref)
		return res, nil
	})
}

type ActionFunc func(ctx *Context, input FS) (FS, error)

func (p *Package) Action(name string, fn ActionFunc) {
	p.actions[name] = fn
}
