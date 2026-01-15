package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagger/dagger/engine"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/util/grpcutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Socket struct {
	// The digest of the DagQL ID that accessed this socket, used as its identifier
	// in socket stores.
	IDDigest digest.Digest
}

func (*Socket) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Socket",
		NonNull:   true,
	}
}

func (*Socket) TypeDescription() string {
	return "A Unix or TCP/IP socket that can be mounted into a container."
}

func (*Socket) PBDefinitions(context.Context) ([]*pb.Definition, error) {
	return nil, nil
}

func (socket *Socket) LLBID() string {
	if socket == nil {
		return ""
	}
	return socket.IDDigest.String()
}

func GetHostIPSocketAccessor(ctx context.Context, query *Query, upstreamHost string, port PortForward) (string, error) {
	// want to include all PortForward values + upstreamHost for the unique accessor
	jsonBytes, err := json.Marshal(struct {
		HostEndpoint string      `json:"host_endpoint,omitempty"`
		PortForward  PortForward `json:"port_forward"`
	}{upstreamHost, port})
	if err != nil {
		return "", fmt.Errorf("failed to marshal host ip socket: %w", err)
	}
	return GetClientResourceAccessor(ctx, query, string(jsonBytes))
}

func NewSocketStore(bkSessionManager *bksession.Manager) *SocketStore {
	return &SocketStore{
		bkSessionManager: bkSessionManager,
		sockets:          map[digest.Digest]*storedSocket{},
	}
}

type SocketStore struct {
	bkSessionManager *bksession.Manager

	sockets map[digest.Digest]*storedSocket
	mu      sync.RWMutex
}

// storedSocket has the actual metadata of the Socket. The Socket type is just it's key into the
// SocketStore, which allows us to pass it around but still more easily enforce that any code that
// wants to access it has to go through the SocketStore. So storedSocket has all the actual data
// once you've asked for the socket from the store.
type storedSocket struct {
	*Socket

	// The id of the buildkit session the socket will be connected through.
	BuildkitSessionID string

	// Unix
	HostPath string

	// IP
	HostEndpoint string // e.g. "localhost", "10.0.0.1", etc. w/out port
	PortForward  PortForward
}

var _ sshforward.SSHServer = &SocketStore{}

func (store *SocketStore) AddUnixSocket(sock *Socket, buildkitSessionID, hostPath string) error {
	if sock == nil {
		return errors.New("socket must not be nil")
	}
	if sock.IDDigest == "" {
		return errors.New("socket must have an ID digest")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.sockets[sock.IDDigest] = &storedSocket{
		Socket:            sock,
		BuildkitSessionID: buildkitSessionID,
		HostPath:          hostPath,
	}
	return nil
}

func (store *SocketStore) AddIPSocket(sock *Socket, buildkitSessionID, upstreamHost string, port PortForward) error {
	if sock == nil {
		return errors.New("socket must not be nil")
	}
	if sock.IDDigest == "" {
		return errors.New("socket must have an ID digest")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.sockets[sock.IDDigest] = &storedSocket{
		Socket:            sock,
		BuildkitSessionID: buildkitSessionID,
		HostEndpoint:      upstreamHost,
		PortForward:       port,
	}
	return nil
}

func (store *SocketStore) AddSocketFromOtherStore(socket *Socket, otherStore *SocketStore) error {
	otherStore.mu.RLock()
	socketVals, ok := otherStore.sockets[socket.IDDigest]
	otherStore.mu.RUnlock()
	if !ok {
		return fmt.Errorf("socket %s not found in other store", socket.IDDigest)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.sockets[socket.IDDigest] = socketVals
	return nil
}

func (store *SocketStore) HasSocket(idDgst digest.Digest) bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	_, ok := store.sockets[idDgst]
	return ok
}

func (store *SocketStore) GetSocketURLEncoded(idDgst digest.Digest) (string, bool) {
	store.mu.RLock()
	sock, ok := store.sockets[idDgst]
	store.mu.RUnlock()
	if !ok {
		return "", false
	}

	return store.getSocketURLEncoded(sock), true
}

func (store *SocketStore) GetSocketPortForward(idDgst digest.Digest) (PortForward, bool) {
	store.mu.RLock()
	sock, ok := store.sockets[idDgst]
	store.mu.RUnlock()
	if !ok {
		return PortForward{}, false
	}

	return sock.PortForward, true
}

func (store *SocketStore) getSocketURLEncoded(sock *storedSocket) string {
	u := &url.URL{
		Fragment: sock.BuildkitSessionID,
	}

	switch {
	case sock.HostPath != "":
		u.Scheme = "unix"
		u.Path = sock.HostPath
	default:
		u.Scheme = sock.PortForward.Protocol.Network()
		u.Host = fmt.Sprintf("%s:%d", sock.HostEndpoint, sock.PortForward.Backend)
	}

	return u.String()
}

func (store *SocketStore) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	store.mu.RLock()
	sock, ok := store.sockets[digest.Digest(req.GetID())]
	store.mu.RUnlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "socket %s not found", req.GetID())
	}

	buildkitSessionID := sock.BuildkitSessionID
	if buildkitSessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "missing buildkit session id")
	}
	caller, err := store.bkSessionManager.Get(ctx, buildkitSessionID, true)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get buildkit session: %s", err)
	}
	if caller == nil {
		return nil, status.Errorf(codes.Internal, "failed to get buildkit session: was nil")
	}

	urlEncoded := store.getSocketURLEncoded(sock)
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(engine.SocketURLEncodedKey, urlEncoded))

	return sshforward.NewSSHClient(caller.Conn()).CheckAgent(ctx, &sshforward.CheckAgentRequest{
		ID: urlEncoded,
	})
}

func (store *SocketStore) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	ctx, cancel := context.WithCancelCause(stream.Context())
	defer cancel(errors.New("forward agent done"))

	opts, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Errorf(codes.InvalidArgument, "no metadata")
	}

	v, ok := opts[sshforward.KeySSHID]
	if !ok || len(v) == 0 || v[0] == "" {
		return status.Errorf(codes.InvalidArgument, "missing ssh id")
	}

	forwardAgentClient, err := store.ConnectSocket(ctx, digest.Digest(v[0]))
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get forward agent client: %s", err)
	}

	return grpcutil.ProxyStream[sshforward.BytesMessage](ctx, forwardAgentClient, stream)
}

func (store *SocketStore) ConnectSocket(ctx context.Context, idDgst digest.Digest) (sshforward.SSH_ForwardAgentClient, error) {
	store.mu.RLock()
	sock, ok := store.sockets[idDgst]
	store.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("socket %s not found", idDgst)
	}

	urlEncoded := store.getSocketURLEncoded(sock)

	opts, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		opts = metadata.Pairs()
	}
	opts.Set(engine.SocketURLEncodedKey, urlEncoded)
	ctx = metadata.NewOutgoingContext(ctx, opts)

	buildkitSessionID := sock.BuildkitSessionID
	if buildkitSessionID == "" {
		return nil, errors.New("missing buildkit session id")
	}
	caller, err := store.bkSessionManager.Get(ctx, buildkitSessionID, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit session: %w", err)
	}

	forwardAgentClient, err := sshforward.NewSSHClient(caller.Conn()).ForwardAgent(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get forward agent client: %w", err)
	}
	return forwardAgentClient, nil
}

func (store *SocketStore) MountSocket(ctx context.Context, idDgst digest.Digest) (string, func() error, error) {
	dir, err := os.MkdirTemp("", ".dagger-ssh-sock")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()

	if err := os.Chmod(dir, 0711); err != nil {
		return "", nil, fmt.Errorf("failed to chmod temp dir: %w", err)
	}

	sockPath := filepath.Join(dir, "ssh_auth_sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to listen on unix socket: %w", err)
	}

	// TODO: correctly set uid,gid,mode
	uid := 0
	gid := 0
	mode := 0775
	if err := os.Chown(sockPath, uid, gid); err != nil {
		l.Close()
		return "", nil, fmt.Errorf("failed to chown unix socket: %w", err)
	}
	if err := os.Chmod(sockPath, os.FileMode(mode)); err != nil {
		l.Close()
		return "", nil, fmt.Errorf("failed to chmod unix socket: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)
	ctx, cancel := context.WithCancelCause(ctx)

	eg.Go(func() error {
		defer l.Close()
		<-ctx.Done()
		return nil
	})

	eg.Go(func() (rerr error) {
		defer func() {
			if rerr != nil {
				cancel(fmt.Errorf("failed to forward ssh agent: %w", rerr))
			} else {
				cancel(nil)
			}
		}()
		for {
			conn, err := l.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return nil
				}
				return fmt.Errorf("failed to accept connection: %w", err)
			}

			stream, err := store.ConnectSocket(ctx, idDgst)
			if err != nil {
				conn.Close()
				return fmt.Errorf("failed to connect to socket: %w", err)
			}

			go sshforward.Copy(ctx, conn, stream, stream.CloseSend)
		}
	})

	return sockPath, func() error {
		cancel(errors.New("cleanup socket mount"))
		err := eg.Wait()
		os.RemoveAll(dir)
		return err
	}, nil
}

func (store *SocketStore) Register(srv *grpc.Server) {
	sshforward.RegisterSSHServer(srv, store)
}
