package core

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/git"
)

// testGitServer stands in for the host-side git session attachable that
// fronts the user's git credential helper.
type testGitServer struct {
	creds map[string]*git.CredentialInfo // keyed by host
}

func (srv *testGitServer) GetCredential(_ context.Context, req *git.GitCredentialRequest) (*git.GitCredentialResponse, error) {
	cred, ok := srv.creds[req.Host]
	if !ok {
		return &git.GitCredentialResponse{Result: &git.GitCredentialResponse_Error{Error: &git.ErrorInfo{
			Type:    git.CREDENTIAL_RETRIEVAL_FAILED,
			Message: "no matching credentials",
		}}}, nil
	}
	return &git.GitCredentialResponse{Result: &git.GitCredentialResponse_Credential{Credential: cred}}, nil
}

func (srv *testGitServer) GetConfig(context.Context, *git.GitConfigRequest) (*git.GitConfigResponse, error) {
	return &git.GitConfigResponse{Result: &git.GitConfigResponse_Config{Config: &git.GitConfig{}}}, nil
}

func newGitAttachableConn(t *testing.T, creds map[string]*git.CredentialInfo) *grpc.ClientConn {
	t.Helper()
	return newTestAttachableConn(t, func(server *grpc.Server) {
		git.RegisterGitServer(server, &testGitServer{creds: creds})
	})
}

func newGitCredentialTestContext(t *testing.T, attachables map[string]*grpc.ClientConn) (context.Context, *dagql.Cache) {
	t.Helper()
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)
	ctx := ContextWithQuery(t.Context(), &Query{Server: &mockServer{attachables: attachables}})
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		SessionID: "test-session",
		ClientID:  "test-client",
	})
	return ctx, cache
}

func mountGitCredentialSocket(t *testing.T, ctx context.Context, handle dagql.SessionResourceHandle) string {
	t.Helper()
	sockPath, cleanup, err := (&Socket{Kind: SocketKindGitCredential, Handle: handle}).MountGitCredentialSocket(ctx)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = cleanup() })
	return sockPath
}

// credentialFill exchanges one request over the socket exactly like the
// in-container helper: write, half-close, read the reply.
func credentialFill(t *testing.T, sockPath, request string) string {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	assert.NilError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte(request))
	assert.NilError(t, err)
	assert.NilError(t, conn.(*net.UnixConn).CloseWrite())

	reply, err := io.ReadAll(conn)
	assert.NilError(t, err)
	return string(reply)
}

func TestGitCredentialSocketRoundTrip(t *testing.T) {
	ctx, cache := newGitCredentialTestContext(t, map[string]*grpc.ClientConn{
		"cred-client": newGitAttachableConn(t, map[string]*git.CredentialInfo{
			"github.com": {Protocol: "https", Host: "github.com", Username: "x-token-auth", Password: "s3cret"},
		}),
	})

	handle := dagql.SessionResourceHandle("git-credential-handle")
	assert.NilError(t, cache.BindSessionResource(ctx, "test-session", "test-client", handle, &Socket{
		Kind:               SocketKindGitCredential,
		SourceClientID:     "cred-client",
		GitCredentialHosts: []string{"github.com"},
	}))
	sockPath := mountGitCredentialSocket(t, ctx, handle)

	assert.Equal(t,
		credentialFill(t, sockPath, "protocol=https\nhost=github.com\ncapability[]=authtype\n\n"),
		"protocol=https\nhost=github.com\nusername=x-token-auth\npassword=s3cret\n\n")

	// anything not allowlisted gets an empty reply: no credentials
	assert.Equal(t, credentialFill(t, sockPath, "protocol=https\nhost=evil.com\n\n"), "")
	assert.Equal(t, credentialFill(t, sockPath, "protocol=ftp\nhost=github.com\n\n"), "")
}

func TestGitCredentialSocketFallsBackToAvailableSessionResourceBinding(t *testing.T) {
	ctx, cache := newGitCredentialTestContext(t, map[string]*grpc.ClientConn{
		"live-client": newGitAttachableConn(t, map[string]*git.CredentialInfo{
			"gitlab.com": {Protocol: "https", Host: "gitlab.com", Username: "user", Password: "pass"},
		}),
	})

	handle := dagql.SessionResourceHandle("git-credential-handle")
	for _, clientID := range []string{"dead-client", "live-client"} {
		assert.NilError(t, cache.BindSessionResource(ctx, "test-session", clientID, handle, &Socket{
			Kind:               SocketKindGitCredential,
			SourceClientID:     clientID,
			GitCredentialHosts: []string{"gitlab.com"},
		}))
	}
	sockPath := mountGitCredentialSocket(t, ctx, handle)

	reply := credentialFill(t, sockPath, "protocol=https\nhost=gitlab.com\n\n")
	assert.Assert(t, is.Contains(reply, "password=pass\n"))
}

func TestParseGitCredentialRequest(t *testing.T) {
	// git sends attributes we don't know (capability[], wwwauth[]); ignore them
	req, err := parseGitCredentialRequest(strings.NewReader("protocol=https\nhost=github.com\npath=org/repo.git\ncapability[]=authtype\n\n"))
	assert.NilError(t, err)
	assert.Equal(t, *req, gitCredentialRequest{protocol: "https", host: "github.com", path: "org/repo.git"})

	_, err = parseGitCredentialRequest(strings.NewReader("host=github.com\n\n"))
	assert.ErrorContains(t, err, "protocol and host are required")

	_, err = parseGitCredentialRequest(strings.NewReader("garbage\n\n"))
	assert.ErrorContains(t, err, "key=value")
}

func TestNormalizeGitCredentialHosts(t *testing.T) {
	assert.DeepEqual(t,
		NormalizeGitCredentialHosts([]string{" GitHub.com ", "gitlab.com", "github.com", ""}),
		[]string{"github.com", "gitlab.com"},
	)
}

func TestScopedGitCredentialSocketHandle(t *testing.T) {
	salt := []byte("salt")
	hosts := []string{"github.com", "gitlab.com"}

	clients := []string{"client-a"}
	handle := ScopedGitCredentialSocketHandle(salt, clients, hosts)
	assert.Equal(t, handle, ScopedGitCredentialSocketHandle(salt, clients, hosts))
	assert.Assert(t, handle != ScopedGitCredentialSocketHandle(salt, []string{"client-b"}, hosts))
	assert.Assert(t, handle != ScopedGitCredentialSocketHandle(salt, clients, []string{"github.com"}))
}
