package dagger

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	gwpb "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/grpcerrors"
	"github.com/moby/buildkit/util/progress/progressui"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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

type Secret struct {
	id string
}

// TODO: implement Secret, it's just identified by a string id which is associated w/ the buildkit session

type String struct {
	fs   FS
	path string
}

// TODO: implement String, lazy by being stored in FS

type Context struct {
	ctx    context.Context
	client bkgw.Client
}

// TODO: rename to MarshalFS
func Marshal(ctx *Context, v any) (FS, error) {
	bytes, err := MarshalBytes(ctx, v)
	if err != nil {
		return FS{}, err
	}
	return Solve(ctx, llb.Scratch().
		File(llb.Mkfile("/dagger.json", 0644, bytes)))
}

func MarshalBytes(ctx *Context, v any) ([]byte, error) {
	var bytes []byte
	switch v := v.(type) {
	case string:
		bytes = []byte(v)
	case []byte:
		bytes = v
	default:
		var err error
		bytes, err = json.Marshal(v)
		if err != nil {
			return nil, err
		}
	}
	return bytes, nil
}

// TODO: rename to UnmarshalFS
func Unmarshal(ctx *Context, fs FS, v any) error {
	bytes, err := fs.ReadFile(ctx, "/dagger.json")
	if err != nil {
		return err
	}
	return UnmarshalBytes(ctx, bytes, v)
}

func UnmarshalBytes(ctx *Context, bytes []byte, v any) error {
	switch v := v.(type) {
	case *string:
		*v = string(bytes)
	case *[]byte:
		*v = bytes
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
		Definition: input.Def,
		Frontend:   "dagger",
		FrontendOpt: map[string]string{
			"pkg":    pkg,
			"action": action,
		},
	})
	if err != nil {
		return FS{}, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return FS{}, err
	}

	// TODO: silly that this is the only way to get the pb def, it's just not a public field, maybe fix upstream
	st, err := ref.ToState()
	if err != nil {
		return FS{}, err
	}
	llbdef, err := st.Marshal(ctx.ctx)
	if err != nil {
		return FS{}, err
	}

	return FS{Def: llbdef.ToPB(), ref: ref}, nil
}

func Client(fn func(*Context) error) error {
	ctx := context.TODO()
	c, err := bkclient.New(ctx, "docker-container://dagger-buildkitd", bkclient.WithFailFast())
	if err != nil {
		return err
	}

	ch := make(chan *bkclient.SolveStatus)

	socketProvider := newAPISocketProvider()
	secretProvider := newSecretProvider()
	attachables := []session.Attachable{socketProvider, secretsprovider.NewSecretProvider(secretProvider)}

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		var err error
		_, err = c.Build(ctx, bkclient.SolveOpt{Session: attachables}, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			api := newAPIServer(c, gw)
			socketProvider.api = api // TODO: less ugly way of setting this

			// TODO: redirect the gw we use to our api. This silliness will go away when we move to our own custom API
			gw, err := grpcclient.New(ctx, nil, "", "", clientAdapter{api}, nil)
			if err != nil {
				return nil, err
			}

			dctx := &Context{
				ctx:    ctx,
				client: gw,
			}

			err = fn(dctx)
			if err != nil {
				return nil, err
			}

			return bkgw.NewResult(), nil
		}, ch)
		return err
	})
	eg.Go(func() error {
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
	grpcConn, err := grpc.DialContext(context.Background(), "unix:///dagger.sock",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(grpcerrors.UnaryClientInterceptor),
		grpc.WithStreamInterceptor(grpcerrors.StreamClientInterceptor))
	if err != nil {
		return err
	}

	client, err := grpcclient.New(context.Background(),
		// opts are passed through /dagger/inputs.json
		make(map[string]string),

		// TODO: not setting sessionID, if it's needed pass it here as a secret to prevent cache bust
		"",

		// product, not needed
		"",

		gwpb.NewLLBBridgeClient(grpcConn),

		// TODO: worker info
		nil,
	)
	if err != nil {
		return err
	}

	return client.Run(appcontext.Context(), func(ctx context.Context, c bkgw.Client) (*bkgw.Result, error) {
		dctx := &Context{
			ctx:    ctx,
			client: c,
		}

		actionName := flag.String("a", "", "name of action to invoke")
		flag.Parse()
		if *actionName == "" {
			return nil, errors.New("action name required")
		}
		fn, ok := p.actions[*actionName]
		if !ok {
			return nil, errors.New("action not found: " + *actionName)
		}
		inputBytes, err := os.ReadFile("/inputs/dagger.json")
		if err != nil {
			return nil, err
		}

		outputBytes, err := fn(dctx, inputBytes)
		if err != nil {
			return nil, err
		}
		err = os.WriteFile("/outputs/dagger.json", outputBytes, 0644)
		if err != nil {
			return nil, err
		}

		return nil, nil
	})
}

type ActionFunc func(ctx *Context, input []byte) ([]byte, error)

func (p *Package) Action(name string, fn ActionFunc) {
	p.actions[name] = fn
}
