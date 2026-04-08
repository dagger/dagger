package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/metadata"
)

type SocketKind string

const (
	SocketKindSSHHandle  SocketKind = "ssh_handle"
	SocketKindUnixOpaque SocketKind = "unix_opaque"
	SocketKindHostIP     SocketKind = "host_ip"
)

type Socket struct {
	Kind           SocketKind
	Handle         dagql.SessionResourceHandle
	URLVal         string
	PortForwardVal PortForward
	SourceClientID string
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

func HostUnixSocketHandle(ctx context.Context, query *Query, path string) (dagql.SessionResourceHandle, error) {
	accessor, err := GetClientResourceAccessor(ctx, query, path)
	if err != nil {
		return "", err
	}
	return dagql.SessionResourceHandle(hashutil.HashStrings(accessor)), nil
}

type persistedSocketPayload struct {
	Kind        SocketKind                  `json:"kind,omitempty"`
	Handle      dagql.SessionResourceHandle `json:"handle,omitempty"`
	PortForward PortForward                 `json:"portForward,omitempty"`
}

func (socket *Socket) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	payload := persistedSocketPayload{}
	if socket != nil {
		payload.Kind = socket.Kind
		payload.Handle = socket.Handle
		if socket.Kind == SocketKindHostIP {
			payload.PortForward = socket.PortForwardVal
		}
	}
	return json.Marshal(payload)
}

func (*Socket) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, call *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedSocketPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted socket payload: %w", err)
		}
	}
	return &Socket{
		Kind:           persisted.Kind,
		Handle:         persisted.Handle,
		PortForwardVal: persisted.PortForward,
	}, nil
}

func ResolveSessionSocket(ctx context.Context, socket *Socket) (*Socket, error) {
	if socket == nil {
		return nil, nil
	}
	if socket.Handle == "" {
		return socket, nil
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve session socket %q: current client metadata: %w", socket.Handle, err)
	}
	if clientMetadata.SessionID == "" {
		return nil, fmt.Errorf("resolve session socket %q: empty session ID", socket.Handle)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve session socket %q: current dagql cache: %w", socket.Handle, err)
	}
	resolvedAny, err := cache.ResolveSessionResource(ctx, clientMetadata.SessionID, clientMetadata.ClientID, socket.Handle)
	if err != nil {
		return nil, err
	}
	resolved, ok := resolvedAny.(*Socket)
	if !ok {
		return nil, fmt.Errorf("resolve session socket %q: bound value is %T", socket.Handle, resolvedAny)
	}
	if resolved.Handle != "" {
		return nil, fmt.Errorf("resolve session socket %q: bound socket is still a handle", socket.Handle)
	}
	return resolved, nil
}

func (socket *Socket) URL(ctx context.Context) (string, error) {
	resolved, err := ResolveSessionSocket(ctx, socket)
	if err != nil {
		return "", err
	}
	if resolved == nil {
		return "", nil
	}
	return resolved.URLVal, nil
}

func (socket *Socket) PortForward(ctx context.Context) (PortForward, error) {
	resolved, err := ResolveSessionSocket(ctx, socket)
	if err != nil {
		return PortForward{}, err
	}
	if resolved == nil {
		return PortForward{}, nil
	}
	return resolved.PortForwardVal, nil
}

func (socket *Socket) ForwardAgentClient(ctx context.Context) (sshforward.SSH_ForwardAgentClient, error) {
	resolved, err := ResolveSessionSocket(ctx, socket)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, errors.New("socket is nil")
	}
	if resolved.URLVal == "" {
		return nil, fmt.Errorf("socket has no URL")
	}
	if resolved.SourceClientID == "" {
		return nil, fmt.Errorf("socket %q: missing source client ID", resolved.URLVal)
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := query.SpecificClientAttachableConn(ctx, resolved.SourceClientID)
	if err != nil {
		return nil, err
	}
	outgoingMD, _ := metadata.FromOutgoingContext(ctx)
	outgoingMD = outgoingMD.Copy()
	outgoingMD.Set(engine.SocketURLEncodedKey, resolved.URLVal)
	stream, err := sshforward.NewSSHClient(conn).ForwardAgent(metadata.NewOutgoingContext(ctx, outgoingMD))
	if err != nil {
		return nil, err
	}
	return stream, nil
}

func (socket *Socket) MountSSHAgent(ctx context.Context) (string, func() error, error) {
	dir, err := os.MkdirTemp("", ".dagger-ssh-sock")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()

	if err := os.Chmod(dir, 0o711); err != nil {
		return "", nil, fmt.Errorf("failed to chmod temp dir: %w", err)
	}
	sockPath := filepath.Join(dir, "ssh_auth_sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to listen on unix socket: %w", err)
	}

	eg, egCtx := errgroup.WithContext(ctx)
	egCtx, cancel := context.WithCancelCause(egCtx)
	eg.Go(func() error {
		defer l.Close()
		<-egCtx.Done()
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
			stream, err := socket.ForwardAgentClient(egCtx)
			if err != nil {
				_ = conn.Close()
				return fmt.Errorf("failed to connect to socket: %w", err)
			}
			go sshforward.Copy(egCtx, conn, stream, stream.CloseSend)
		}
	})

	return sockPath, func() error {
		cancel(errors.New("cleanup socket mount"))
		err := eg.Wait()
		_ = os.RemoveAll(dir)
		return err
	}, nil
}

func (socket *Socket) AgentFingerprints(ctx context.Context) ([]string, error) {
	sockPath, cleanup, err := socket.MountSSHAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cleanup() }()

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
