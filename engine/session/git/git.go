package git

import (
	context "context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/dagger/dagger/util/grpcutil"
	grpc "google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
)

// gitHelperMutex retains the existing serialization for the short-lived
// credential-helper and config commands. Checkout packing has its own keyed,
// context-aware synchronization and must never block these requests.
var gitHelperMutex sync.Mutex

var gitCheckoutLocks contextKeyedLocker

// MaxGitPackBytes bounds an aggregate checkout bundle or uncommitted patch on
// both sides of the session transport.
const MaxGitPackBytes int64 = 4 << 30

type contextKeyedLocker struct {
	mu      sync.Mutex
	entries map[string]*contextKeyedLock
}

type contextKeyedLock struct {
	token chan struct{}
	refs  int
}

func (locks *contextKeyedLocker) lock(ctx context.Context, key string) (func(), error) {
	locks.mu.Lock()
	if locks.entries == nil {
		locks.entries = map[string]*contextKeyedLock{}
	}
	entry := locks.entries[key]
	if entry == nil {
		entry = &contextKeyedLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		locks.entries[key] = entry
	}
	entry.refs++
	locks.mu.Unlock()

	releaseRef := func() {
		locks.mu.Lock()
		defer locks.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(locks.entries, key)
		}
	}

	select {
	case <-ctx.Done():
		releaseRef()
		return nil, context.Cause(ctx)
	case <-entry.token:
		if err := context.Cause(ctx); err != nil {
			entry.token <- struct{}{}
			releaseRef()
			return nil, err
		}
		var once sync.Once
		return func() {
			once.Do(func() {
				entry.token <- struct{}{}
				releaseRef()
			})
		}, nil
	}
}

type GitAttachable struct {
	rootCtx context.Context

	UnimplementedGitServer
}

func NewGitAttachable(rootCtx context.Context) GitAttachable {
	return GitAttachable{
		rootCtx: rootCtx,
	}
}

func (s GitAttachable) Register(srv *grpc.Server) {
	RegisterGitServer(srv, &s)
}

type GitAttachableProxy struct {
	client GitClient
}

func NewGitAttachableProxy(client GitClient) GitAttachableProxy {
	return GitAttachableProxy{client: client}
}

func (p GitAttachableProxy) Register(server *grpc.Server) {
	RegisterGitServer(server, p)
}

func (p GitAttachableProxy) GetCredential(ctx context.Context, req *GitCredentialRequest) (*GitCredentialResponse, error) {
	return p.client.GetCredential(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p GitAttachableProxy) GetConfig(ctx context.Context, req *GitConfigRequest) (*GitConfigResponse, error) {
	return p.client.GetConfig(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p GitAttachableProxy) CheckoutState(ctx context.Context, req *CheckoutStateRequest) (*CheckoutStateResponse, error) {
	return p.client.CheckoutState(grpcutil.IncomingToOutgoingContext(ctx), req)
}

func (p GitAttachableProxy) PackCheckout(req *PackCheckoutRequest, srv Git_PackCheckoutServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))

	clientStream, err := p.client.PackCheckout(grpcutil.IncomingToOutgoingContext(ctx), req)
	if err != nil {
		return fmt.Errorf("create client stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, srv)
}

func (p GitAttachableProxy) PackUncommitted(req *PackUncommittedRequest, srv Git_PackUncommittedServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))

	clientStream, err := p.client.PackUncommitted(grpcutil.IncomingToOutgoingContext(ctx), req)
	if err != nil {
		return fmt.Errorf("create client stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, srv)
}

func (p GitAttachableProxy) CaptureGit(req *CaptureGitRequest, srv Git_CaptureGitServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))
	clientStream, err := p.client.CaptureGit(grpcutil.IncomingToOutgoingContext(ctx), req)
	if err != nil {
		return fmt.Errorf("create client stream: %w", err)
	}
	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, srv)
}

func (p GitAttachableProxy) ApplyBundle(srv Git_ApplyBundleServer) error {
	ctx, cancel := context.WithCancelCause(srv.Context())
	defer cancel(errors.New("proxy stream closed"))
	stream, err := p.client.ApplyBundle(grpcutil.IncomingToOutgoingContext(ctx))
	if err != nil {
		return err
	}
	for {
		req, err := srv.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if err := stream.Send(req); err != nil {
			return err
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		return err
	}
	return srv.SendAndClose(response)
}
