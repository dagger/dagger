package core

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/slog"
)

const gitCredentialSocketDigestVersion = "git-credential-socket-v1"

// gitCredentialRequestTimeout bounds a single credential exchange over the
// socket, including the round-trip to the host's git credential helper (which
// itself has a 30s timeout).
const gitCredentialRequestTimeout = 60 * time.Second

// gitCredentialMaxRequestSize bounds a single request read from the socket.
const gitCredentialMaxRequestSize = 64 << 10

// ScopedGitCredentialSocketHandle derives the session resource handle for a
// git-credential socket serving the given clients' credentials for the given
// hosts. hosts must already be normalized (see NormalizeGitCredentialHosts).
func ScopedGitCredentialSocketHandle(secretSalt []byte, clientIDs []string, hosts []string) dagql.SessionResourceHandle {
	mac := hmac.New(sha256.New, secretSalt)
	mac.Write([]byte(gitCredentialSocketDigestVersion))
	mac.Write([]byte{0})
	for _, clientID := range clientIDs {
		mac.Write([]byte(clientID))
		mac.Write([]byte{0})
	}
	mac.Write([]byte{0})
	for _, host := range hosts {
		mac.Write([]byte(host))
		mac.Write([]byte{0})
	}
	return dagql.SessionResourceHandle(digest.NewDigestFromBytes(digest.SHA256, mac.Sum(nil)))
}

// NormalizeGitCredentialHosts lowercases, dedupes and sorts the given host
// allowlist, dropping empty entries.
func NormalizeGitCredentialHosts(hosts []string) []string {
	normalized := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		normalized = append(normalized, host)
	}
	slices.Sort(normalized)
	return slices.Compact(normalized)
}

// gitCredentialRequest is a parsed git-credential protocol request
// (https://git-scm.com/docs/git-credential#IOFMT).
type gitCredentialRequest struct {
	protocol string
	host     string
	path     string
}

func parseGitCredentialRequest(r io.Reader) (*gitCredentialRequest, error) {
	req := &gitCredentialRequest{}
	scanner := bufio.NewScanner(io.LimitReader(r, gitCredentialMaxRequestSize))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid git credential request: line doesn't match key=value pattern")
		}
		switch key {
		case "protocol":
			req.protocol = value
		case "host":
			req.host = value
		case "path":
			req.path = value
		default:
			// ignore unknown attributes (e.g. capability, wwwauth[])
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read git credential request: %w", err)
	}
	if req.protocol == "" || req.host == "" {
		return nil, errors.New("invalid git credential request: protocol and host are required")
	}
	return req, nil
}

func formatGitCredentialResponse(cred *git.CredentialInfo) []byte {
	var sb strings.Builder
	if cred.Protocol != "" {
		fmt.Fprintf(&sb, "protocol=%s\n", cred.Protocol)
	}
	if cred.Host != "" {
		fmt.Fprintf(&sb, "host=%s\n", cred.Host)
	}
	fmt.Fprintf(&sb, "username=%s\n", cred.Username)
	fmt.Fprintf(&sb, "password=%s\n", cred.Password)
	sb.WriteString("\n")
	return []byte(sb.String())
}

// MountGitCredentialSocket serves the git-credential protocol on a fresh unix
// socket. Each connection carries a single request; allowed requests are
// relayed to the source client's git session attachable, everything else gets
// an empty reply (no credentials).
func (socket *Socket) MountGitCredentialSocket(ctx context.Context) (string, func() error, error) {
	dir, err := os.MkdirTemp("", ".dagger-git-credential-sock")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()

	if err = os.Chmod(dir, 0o711); err != nil {
		return "", nil, fmt.Errorf("failed to chmod temp dir: %w", err)
	}
	sockPath := filepath.Join(dir, "git_credential_sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to listen on unix socket: %w", err)
	}

	srvCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer l.Close()
		<-srvCtx.Done()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		// close the listener on any accept failure so later connection
		// attempts fail fast instead of queueing against a dead loop
		defer cancel()
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go socket.serveGitCredentialConn(srvCtx, conn)
		}
	}()

	return sockPath, func() error {
		cancel()
		wg.Wait()
		_ = os.RemoveAll(dir)
		return nil
	}, nil
}

func (socket *Socket) serveGitCredentialConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, gitCredentialRequestTimeout)
	defer cancel()
	_ = conn.SetDeadline(time.Now().Add(gitCredentialRequestTimeout))

	req, err := parseGitCredentialRequest(conn)
	if err != nil {
		slog.Warn("git credential socket: invalid request", "error", err)
		return
	}

	cred, err := socket.fetchGitCredential(ctx, req)
	if err != nil {
		// reply with nothing: the helper produces no output and git treats it
		// as "no credentials from this helper"
		slog.Warn("git credential socket: no credentials", "host", req.host, "error", err)
		return
	}

	if _, err := conn.Write(formatGitCredentialResponse(cred)); err != nil {
		slog.Warn("git credential socket: failed to write response", "host", req.host, "error", err)
		return
	}
	slog.Debug("git credential socket: served credentials", "host", req.host)
}

func (socket *Socket) fetchGitCredential(ctx context.Context, req *gitCredentialRequest) (*git.CredentialInfo, error) {
	if req.protocol != "http" && req.protocol != "https" {
		return nil, fmt.Errorf("unsupported protocol %q", req.protocol)
	}

	if socket.Handle == "" {
		return socket.fetchGitCredentialFromClient(ctx, req)
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
	candidates, err := cache.ResolveSessionResourceCandidates(ctx, clientMetadata.SessionID, clientMetadata.ClientID, socket.Handle)
	if err != nil {
		return nil, err
	}

	var errs error
	for _, candidate := range candidates {
		resolved, ok := candidate.Value.(*Socket)
		if !ok {
			return nil, fmt.Errorf("resolve session socket %q: bound value for client %q is %T", socket.Handle, candidate.ClientID, candidate.Value)
		}
		cred, err := resolved.fetchGitCredentialFromClient(ctx, req)
		if err == nil {
			return cred, nil
		}
		errs = errors.Join(errs, fmt.Errorf("client %q: %w", candidate.ClientID, err))
	}
	if errs != nil {
		return nil, errs
	}
	return nil, fmt.Errorf("resolve session socket %q: no available client binding", socket.Handle)
}

func (socket *Socket) fetchGitCredentialFromClient(ctx context.Context, req *gitCredentialRequest) (*git.CredentialInfo, error) {
	if !slices.Contains(socket.GitCredentialHosts, strings.ToLower(req.host)) {
		return nil, fmt.Errorf("host %q is not in the allowed hosts list", req.host)
	}
	if socket.SourceClientID == "" {
		return nil, errors.New("git credential socket: missing source client ID")
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	conn, ok, err := query.SpecificClientAttachableConn(ctx, socket.SourceClientID, SpecificClientAttachableConnOpts{
		IfAvailable: true,
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no active session attachables for client %q", socket.SourceClientID)
	}

	resp, err := git.NewGitClient(conn).GetCredential(ctx, &git.GitCredentialRequest{
		Protocol: req.protocol,
		Host:     req.host,
		Path:     req.path,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query credentials: %w", err)
	}
	switch result := resp.Result.(type) {
	case *git.GitCredentialResponse_Credential:
		return result.Credential, nil
	case *git.GitCredentialResponse_Error:
		return nil, fmt.Errorf("git credential error: %s", result.Error.Message)
	default:
		return nil, errors.New("unexpected git credential response type")
	}
}
