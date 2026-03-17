package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"github.com/dagger/dagger/util/grpcutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
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

func (socket *Socket) LLBID() string {
	if socket == nil {
		return ""
	}
	return socket.IDDigest.String()
}

func SocketIDDigest(id *call.ID) digest.Digest {
	if id == nil {
		return ""
	}
	if contentDigest := id.ContentDigest(); contentDigest != "" {
		return contentDigest
	}
	return id.Digest()
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

func cloneSocket(sock *Socket) *Socket {
	if sock == nil {
		return nil
	}
	cp := *sock
	return &cp
}

func cloneStoredSocket(sock *storedSocket) *storedSocket {
	if sock == nil {
		return nil
	}
	cp := *sock
	cp.Socket = cloneSocket(sock.Socket)
	return &cp
}

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
		Socket:            cloneSocket(sock),
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
		Socket:            cloneSocket(sock),
		BuildkitSessionID: buildkitSessionID,
		HostEndpoint:      upstreamHost,
		PortForward:       port,
	}
	return nil
}

func (store *SocketStore) AddSocketFromOtherStore(socket *Socket, otherStore *SocketStore) error {
	if socket == nil {
		return errors.New("socket must not be nil")
	}
	if socket.IDDigest == "" {
		return errors.New("socket must have an ID digest")
	}

	otherStore.mu.RLock()
	socketVals, ok := otherStore.sockets[socket.IDDigest]
	otherStore.mu.RUnlock()
	if !ok {
		return fmt.Errorf("socket %s not found in other store", socket.IDDigest)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.sockets[socket.IDDigest]; ok {
		// Keep the destination's existing mapping; callers can always explicitly
		// re-register a local socket via AddUnixSocket/AddIPSocket.
		return nil
	}
	store.sockets[socket.IDDigest] = cloneStoredSocket(socketVals)
	return nil
}

// AddSocketAlias registers a new socket ID that resolves to the same underlying
// client resource mapping as an existing source socket ID.
//
// Example: host._sshAuthSocket computes a new digest scoped by SSH key fingerprints
// and calls AddSocketAlias(scoped, sourceDigest). Later, MountSocket(scoped.IDDigest)
// uses the source socket's session/path metadata to forward agent traffic.
func (store *SocketStore) AddSocketAlias(alias *Socket, sourceIDDigest digest.Digest) error {
	if alias == nil {
		return errors.New("socket alias must not be nil")
	}
	if alias.IDDigest == "" {
		return errors.New("socket alias must have an ID digest")
	}
	if sourceIDDigest == "" {
		return errors.New("source socket digest must not be empty")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.sockets[alias.IDDigest]; ok {
		return nil
	}

	source, ok := store.sockets[sourceIDDigest]
	if !ok {
		return fmt.Errorf("source socket %s not found", sourceIDDigest)
	}

	aliased := cloneStoredSocket(source)
	aliased.Socket = cloneSocket(alias)
	store.sockets[alias.IDDigest] = aliased
	return nil
}

func (store *SocketStore) AgentFingerprints(ctx context.Context, idDgst digest.Digest) ([]string, error) {
	sockPath, cleanup, err := store.MountSocket(ctx, idDgst)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mounted SSH socket: %w", err)
	}
	defer conn.Close()

	keys, err := agent.NewClient(conn).List()
	if err != nil {
		return nil, fmt.Errorf("failed to list SSH agent identities: %w", err)
	}

	seen := map[string]struct{}{}
	fingerprints := make([]string, 0, len(keys))
	for _, key := range keys {
		if key == nil {
			continue
		}
		pub, err := ssh.ParsePublicKey(key.Blob)
		if err != nil {
			sum := sha256.Sum256(key.Blob)
			fp := "sha256:" + hex.EncodeToString(sum[:])
			if _, ok := seen[fp]; ok {
				continue
			}
			seen[fp] = struct{}{}
			fingerprints = append(fingerprints, fp)
			continue
		}
		fp := ssh.FingerprintSHA256(pub)
		if _, ok := seen[fp]; ok {
			continue
		}
		seen[fp] = struct{}{}
		fingerprints = append(fingerprints, fp)
	}
	slices.Sort(fingerprints)
	return fingerprints, nil
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
	if sock == nil {
		// Defensive check: avoid panicking on malformed store state and surface
		// a regular error path instead.
		return nil, fmt.Errorf("socket %s has nil metadata", idDgst)
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
	if caller == nil {
		// noWait=true allows Get to return nil when the session is gone; treat this
		// as a normal lookup failure rather than dereferencing a nil caller.
		return nil, fmt.Errorf("failed to get buildkit session: session %q is not active", buildkitSessionID)
	}

	conn := caller.Conn()
	if conn == nil {
		return nil, fmt.Errorf("failed to get buildkit session: session %q has nil connection", buildkitSessionID)
	}

	forwardAgentClient, err := sshforward.NewSSHClient(conn).ForwardAgent(ctx)
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
