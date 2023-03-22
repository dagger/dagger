package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"dagger.io/dagger"
	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/secret"
	"github.com/docker/cli/cli/config"
	"github.com/google/uuid"
	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/progress/progressui"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

// NewOperatorClient is a dagger client used for querying+configuring engine-wide settings such
// as cache mount import/export.
func NewOperatorClient(ctx context.Context, c *bkclient.Client) (_ *dagger.Client, _ func() error, rerr error) {
	platform, err := detectPlatform(ctx, c)
	if err != nil {
		return nil, nil, err
	}

	sessionToken, err := uuid.NewRandom()
	if err != nil {
		return nil, nil, err
	}

	// allocate the next available port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	port := l.Addr().(*net.TCPAddr).Port

	solveCh := make(chan *bkclient.SolveStatus)
	go func() {
		warn, err := progressui.DisplaySolveStatus(context.TODO(), nil, os.Stdout, solveCh)
		for _, w := range warn {
			fmt.Fprintf(os.Stdout, "=> %s\n", w.Short)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "progress error: %v\n", err)
		}
	}()

	secretStore := secret.NewStore()
	registryAuth := auth.NewRegistryAuthProvider(config.LoadDefaultConfigFile(os.Stderr))
	socketProviders := engine.SocketProvider{
		EnableHostNetworkAccess: false,
	}

	solveOpts := bkclient.SolveOpt{
		Session: []session.Attachable{
			secretsprovider.NewSecretProvider(secretStore),
			registryAuth,
			socketProviders,
		},
	}

	solveCtx, cancel := context.WithCancel(context.Background())
	defer func() {
		if rerr != nil {
			cancel()
		}
	}()
	doneCh := make(chan error, 1)
	go func() {
		defer close(doneCh)
		_, err = c.Build(solveCtx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			go pipeline.LoadRootLabels(ctx, ".")
			router := router.New(sessionToken.String())
			secretStore.SetGateway(gw)
			gwClient := core.NewGatewayClient(gw)
			coreAPI, err := schema.New(schema.InitializeArgs{
				Router:         router,
				Gateway:        gwClient,
				BKClient:       c,
				SolveOpts:      solveOpts,
				SolveCh:        solveCh,
				Platform:       *platform,
				DisableHostRW:  true,
				EnableServices: true,
				Auth:           registryAuth,
			})
			if err != nil {
				return nil, err
			}
			if err := router.Add(coreAPI); err != nil {
				return nil, err
			}
			srv := http.Server{
				Handler:           router,
				ReadHeaderTimeout: 30 * time.Second,
			}
			err = srv.Serve(l)
			if err != nil && !errors.Is(err, net.ErrClosed) {
				return nil, err
			}
			return nil, nil
		}, solveCh)
		doneCh <- err
	}()

	os.Setenv("DAGGER_SESSION_PORT", strconv.Itoa(port))
	defer os.Unsetenv("DAGGER_SESSION_PORT")
	os.Setenv("DAGGER_SESSION_TOKEN", sessionToken.String())
	defer os.Unsetenv("DAGGER_SESSION_TOKEN")
	daggerClient, err := dagger.Connect(ctx)
	if err != nil {
		return nil, nil, err
	}
	return daggerClient, func() error {
		cancel()
		l.Close()
		err := <-doneCh
		c.Close()
		daggerClient.Close()
		return err
	}, nil
}

func detectPlatform(ctx context.Context, c *bkclient.Client) (*specs.Platform, error) {
	w, err := c.ListWorkers(ctx)
	if err != nil {
		return nil, fmt.Errorf("error detecting platform %w", err)
	}

	if len(w) > 0 && len(w[0].Platforms) > 0 {
		dPlatform := w[0].Platforms[0]
		return &dPlatform, nil
	}
	defaultPlatform := platforms.DefaultSpec()
	return &defaultPlatform, nil
}

type inMemListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

func newInMemListener(server *grpc.Server) *inMemListener {
	l := &inMemListener{
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
	go server.Serve(l)
	return l
}

func (l *inMemListener) NewClient(ctx context.Context, opts ...bkclient.ClientOpt) (*bkclient.Client, error) {
	return bkclient.New(ctx, "inmem", append(opts, bkclient.WithContextDialer(l.Dial))...)
}

func (l *inMemListener) Dial(ctx context.Context, _ string) (net.Conn, error) {
	clientConn, serverConn := net.Pipe()
	select {
	case <-l.closed:
		clientConn.Close()
		serverConn.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		clientConn.Close()
		serverConn.Close()
		return nil, ctx.Err()
	case l.conns <- serverConn:
	}
	return clientConn, nil
}

func (l *inMemListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	case conn := <-l.conns:
		return conn, nil
	}
}

func (l *inMemListener) Close() error {
	close(l.closed)
	return nil
}

func (l *inMemListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "inmem", Net: "unix"}
}
