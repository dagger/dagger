package sessions

import (
	"context"
	"log"
	"sync"

	"github.com/dagger/dagger/secret"
	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/mwitkow/grpc-proxy/proxy"
	"google.golang.org/grpc"
)

type Manager struct {
	*session.Manager

	bkClient  *bkclient.Client
	solveCh   chan *bkclient.SolveStatus
	solveOpts bkclient.SolveOpt

	gws  map[string]bkgw.Client
	gwsL sync.RWMutex
}

func NewManager(
	bkClient *bkclient.Client,
	solveCh chan *bkclient.SolveStatus,
	solveOpts bkclient.SolveOpt,
) (*Manager, error) {
	m, err := session.NewManager()
	if err != nil {
		return nil, err
	}

	return &Manager{
		Manager:   m,
		bkClient:  bkClient,
		solveCh:   solveCh,
		solveOpts: solveOpts,
		gws:       make(map[string]bkgw.Client),
	}, nil
}

func (manager *Manager) Gateway(ctx context.Context, id string) (bkgw.Client, error) {
	// FIXME(vito): per-ID lock; this is a bit of a bottleneck
	manager.gwsL.Lock()
	defer manager.gwsL.Unlock()

	gw, ok := manager.gws[id]
	if ok {
		return gw, nil
	}

	caller, err := manager.Get(ctx, id, false)
	if err != nil {
		return nil, err
	}

	// TODO(vito): do we need to care about wg?
	ch, _ := mirrorCh(manager.solveCh)

	// TODO(vito): wire up SolveOpts that forward to the Caller

	secretStore := secret.NewStore()
	solveOpts := manager.solveOpts
	solveOpts.Session = append(solveOpts.Session,
		secretsprovider.NewSecretProvider(secretStore),
		clientProxy{caller},
	)

	gwCh := make(chan bkgw.Client, 1)
	gwErrCh := make(chan error, 1)
	go func() {
		_, gwErr := manager.bkClient.Build(context.Background(), solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			// Secret store is a circular dependency, since it needs to resolve
			// SecretIDs using the gateway, we don't have a gateway until we call
			// Build, which needs SolveOpts, which needs to contain the secret store.
			//
			// Thankfully we can just yeet the gateway into the store.
			secretStore.SetGateway(gw)

			gwCh <- gw
			log.Println("SENT GATEWAY", gw.BuildOpts().SessionID)

			// wait for
			<-caller.Context().Done()
			log.Println("CALLER CONTEXT DONE", gw.BuildOpts().SessionID)

			return nil, nil
		}, ch)
		gwErrCh <- gwErr
	}()

	select {
	case gw := <-gwCh:
		manager.gws[id] = gw
		return gw, nil
	case err := <-gwErrCh:
		return nil, err
	}
}

// mirrorCh mirrors messages from one channel to another, protecting the
// destination channel from being closed.
//
// this is used to reflect Build/Solve progress in a longer-lived progress UI,
// since they close the channel when they're done.
func mirrorCh[T any](dest chan<- T) (chan T, *sync.WaitGroup) {
	wg := new(sync.WaitGroup)

	if dest == nil {
		return nil, wg
	}

	mirrorCh := make(chan T)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range mirrorCh {
			dest <- event
		}
	}()

	return mirrorCh, wg
}

type clientProxy struct {
	caller session.Caller
}

func (p clientProxy) Register(srv *grpc.Server) {
	director := proxy.DefaultDirector(p.caller.Conn())
	// proxy.RegisterService(
	// 	srv,
	// 	director,
	// 	"moby.filesync.v1.Auth",
	// 	"GetTokenAuthority",
	// 	"VerifyTokenAuthority",
	// 	"Credentials",
	// 	"FetchToken",
	// )
	proxy.RegisterService(
		srv,
		director,
		"moby.filesync.v1.FileSync",
		"DiffCopy",
		"TarStream",
	)
}
