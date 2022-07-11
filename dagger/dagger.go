package dagger

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	gwpb "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/grpcerrors"
	"github.com/moby/buildkit/util/progress/progressui"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
)

type Context struct {
	ctx    context.Context
	client bkgw.Client
	// TODO: should just be part of API, not here, temp hack
	secretProvider *secretProvider
}

func Do(ctx *Context, pkg, action string, v any) (*Result, error) {
	inputBytes, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	res, err := ctx.client.Solve(ctx.ctx, bkgw.SolveRequest{
		Frontend: "dagger",
		FrontendOpt: map[string]string{
			"pkg":     pkg,
			"action":  action,
			"payload": string(inputBytes),
		},
	})
	if err != nil {
		return nil, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	outputBytes, err := ref.ReadFile(ctx.ctx, bkgw.ReadRequest{
		Filename: "/result.json",
	})
	if err != nil {
		return nil, err
	}

	result := &Result{}
	err = json.Unmarshal(outputBytes, result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// TODO: obviously needs to be more secure, validated you are supposed to have the secret
func ReadSecret(ctx *Context, id string) (string, error) {
	res, err := ctx.client.Solve(ctx.ctx, bkgw.SolveRequest{
		Frontend: "read-secret",
		FrontendOpt: map[string]string{
			"secretID": string(id),
		},
	})
	if err != nil {
		return "", err
	}
	return string(res.Metadata[id]), nil
}

func RunWithContext(f func(*Context) error) error {
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
		err := f(&Context{
			ctx:    ctx,
			client: c,
		})
		if err != nil {
			return nil, err
		}
		return nil, nil
	})
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
	return RunWithContext(func(ctx *Context) error {
		actionName := flag.String("a", "", "name of action to invoke")
		flag.Parse()
		if *actionName == "" {
			return errors.New("action name required")
		}
		fn, ok := p.actions[*actionName]
		if !ok {
			return errors.New("action not found: " + *actionName)
		}
		inputBytes, err := os.ReadFile("/inputs/dagger.json")
		if err != nil {
			return err
		}

		outputBytes, err := fn(ctx, inputBytes)
		if err != nil {
			return err
		}
		err = os.WriteFile("/outputs/dagger.json", outputBytes, 0644)
		if err != nil {
			return err
		}

		return nil
	})
}

type ActionFunc func(ctx *Context, input []byte) ([]byte, error)

func (p *Package) Action(name string, fn ActionFunc) {
	p.actions[name] = fn
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
			defer func() {
				if r := recover(); r != nil {
					time.Sleep(2 * time.Second) // TODO: dumb, but allows logs to fully flush
					panic(r)
				}
			}()
			api := newAPIServer(c, gw, secretProvider)
			socketProvider.api = api // TODO: less ugly way of setting this

			// TODO: redirect the gw we use to our api. This silliness will go away when we move to our own custom API
			gw, err := grpcclient.New(ctx, nil, "", "", clientAdapter{api}, nil)
			if err != nil {
				return nil, err
			}

			dctx := &Context{
				ctx:            ctx,
				client:         gw,
				secretProvider: secretProvider,
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
