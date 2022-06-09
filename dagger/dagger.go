package dagger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/containerd/console"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/progress/progressui"
	"golang.org/x/sync/errgroup"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
)

type FS struct{}

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

func Do(ctx *Context, pkg, action string, payload string) (*Output, error) {
	res, err := ctx.client.Solve(ctx.ctx, bkgw.SolveRequest{
		Evaluate: true,
		Frontend: "gateway.v0",
		FrontendOpt: map[string]string{
			"source":  pkg,
			"action":  action,
			"payload": payload,
		},
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	data, err := ref.ReadFile(ctx.ctx, bkgw.ReadRequest{
		Filename: "/dagger/output.json",
	})
	if err != nil {
		return nil, err
	}

	output := &Output{}
	if err := json.Unmarshal(data, &output.data); err != nil {
		return nil, err
	}

	return output, nil
}

type Input struct {
	payload []byte
}

func (i *Input) Decode(v any) error {
	return json.Unmarshal(i.payload, v)
}

type Output struct {
	data any
}

func (o *Output) Set(data any) {
	o.data = data
}

func (o *Output) Get() any {
	return o.data
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
		c, err := console.ConsoleFromFile(os.Stderr)
		if err != nil {
			return err
		}

		warn, err := progressui.DisplaySolveStatus(context.TODO(), "", c, os.Stdout, ch)
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

		input := &Input{
			payload: []byte(opts["payload"]),
		}

		output, err := fn(dctx, input)
		if err != nil {
			return nil, err
		}

		data, err := json.Marshal(output.data)
		if err != nil {
			return nil, err
		}

		st := llb.
			Scratch().
			File(llb.Mkdir("/dagger", 0755)).
			File(llb.Mkfile("/dagger/output.json", 0644, data))
		def, err := st.Marshal(ctx, llb.LinuxArm64)
		if err != nil {
			return nil, err
		}

		return c.Solve(ctx, bkgw.SolveRequest{
			Definition: def.ToPB(),
		})
	})
}

type ActionFunc func(*Context, *Input) (*Output, error)

func (p *Package) Action(name string, fn ActionFunc) {
	p.actions[name] = fn
}
