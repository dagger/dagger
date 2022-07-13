package dagger

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/progress/progressui"
	"golang.org/x/sync/errgroup"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
)

type Context struct {
	ctx    context.Context
	client APIClient
	// TODO: should just be part of API, not here, temp hack
	secretProvider *secretProvider
}

// TODO: unecessary abstraction now, just make Context implement APIClient or something
func Do(ctx *Context, payload string) (string, error) {
	return ctx.client.Do(ctx.ctx, payload)
}

func RunWithContext(f func(*Context) error) error {
	return f(&Context{
		ctx: context.Background(),
		client: httpClient{&http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", "/dagger.sock")
				},
			},
		}},
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

			dctx := &Context{
				ctx:            ctx,
				client:         clientAdapter{api},
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
