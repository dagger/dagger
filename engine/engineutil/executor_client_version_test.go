package engineutil

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

func TestExecDeclaredClientVersion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      map[string]string
		want     string
		declared bool
	}{
		{name: "default", want: engine.Version},
		{name: "declared", env: map[string]string{"DAGGER_CLIENT_VERSION": "v0.21.6"}, want: "v0.21.6", declared: true},
		{name: "empty", env: map[string]string{"DAGGER_CLIENT_VERSION": ""}, want: engine.Version},
		{name: "experimental", env: map[string]string{"_EXPERIMENTAL_DAGGER_VERSION": "v0.21.6"}, want: "v0.21.6"},
		{name: "declared wins", env: map[string]string{"DAGGER_CLIENT_VERSION": "v0.21.6", "_EXPERIMENTAL_DAGGER_VERSION": "v1.0.0-beta.18"}, want: "v0.21.6", declared: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "version test", func() {}, nil)
			defer lease.Release()
			scope, err := engine.NewClientScope(&engine.ClientMetadata{SessionID: "session", ClientID: "parent"}, lease)
			require.NoError(t, err)
			ctx, err := engine.ContextWithClientScope(t.Context(), scope)
			require.NoError(t, err)
			boundary := errors.New("stop before opening nested client listener")
			state := &execState{
				nestedClientMetadata: &engine.ClientMetadata{ClientID: "nested", SessionID: "session", ClientVersion: engine.Version},
				spec:                 &specs.Spec{Process: &specs.Process{}},
				origEnvMap:           tc.env,
				execMD:               &ExecutionMetadata{NoInit: true},
				networkNamespace:     versionTestNamespace{err: boundary},
				cleanups:             &cleanups.Cleanups{},
			}
			t.Cleanup(func() { require.NoError(t, state.cleanups.Run()) })
			client := &Client{Opts: &Opts{SessionHandler: &nestedTransportSessionHandler{}}}
			require.ErrorIs(t, client.setupNestedClient(ctx, state), boundary)
			require.Equal(t, tc.want, state.nestedClientMetadata.ClientVersion)
			require.Equal(t, tc.declared, state.nestedClientMetadata.ClientVersionDeclared)
		})
	}
}

type versionTestNamespace struct {
	network.Namespace
	err error
}

func (ns versionTestNamespace) Set(*specs.Spec) error { return ns.err }

func TestNestedTransportDeclaredClientVersion(t *testing.T) {
	base := &engine.ClientMetadata{
		ClientID: "module-process", SessionID: "session", ClientSecretToken: "secret",
		ClientVersion: "v0.21.6", ClientVersionDeclared: true,
		SSHAuthSocketPath: "/ssh.sock", AllowedLLMModules: []string{"module"},
	}
	handler := &nestedTransportSessionHandler{}
	manager := newNestedClientTransportManager(t.Context(), handler, base, "parent", false)
	defer manager.Close()

	for _, tc := range []struct {
		name        string
		metadata    *engine.ClientMetadata
		wantID      string
		wantVersion string
	}{
		{name: "headerless", wantID: base.ClientID, wantVersion: base.ClientVersion},
		{name: "nested CLI", metadata: &engine.ClientMetadata{ClientID: "cli", ClientVersion: engine.Version, SessionID: "untrusted", ClientSecretToken: "untrusted"}, wantID: "cli", wantVersion: engine.Version},
		{name: "library", metadata: &engine.ClientMetadata{ClientID: "library", ClientVersion: "v0.20.0"}, wantID: "library", wantVersion: "v0.20.0"},
		{name: "empty version", metadata: &engine.ClientMetadata{ClientID: "empty"}, wantID: "empty", wantVersion: base.ClientVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/query", nil)
			if tc.metadata != nil {
				req.Header = tc.metadata.AppendToHTTPHeaders(req.Header)
			}
			_, metadata, status, err := manager.transportForRequest(req)
			require.NoError(t, err)
			require.Zero(t, status)
			require.Equal(t, tc.wantID, metadata.ClientID)
			require.Equal(t, tc.wantVersion, metadata.ClientVersion)
			require.True(t, metadata.ClientVersionDeclared)
			require.Equal(t, base.SessionID, metadata.SessionID)
			require.Equal(t, base.ClientSecretToken, metadata.ClientSecretToken)
			require.Equal(t, base.SSHAuthSocketPath, metadata.SSHAuthSocketPath)
			require.Equal(t, base.AllowedLLMModules, metadata.AllowedLLMModules)
		})
	}
	require.Equal(t, "v0.21.6", base.ClientVersion)
}
