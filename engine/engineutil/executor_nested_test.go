package engineutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

type nestedTransportSessionHandler struct {
	registered atomic.Int32
	closed     atomic.Int32
	served     atomic.Int32
}

func (handler *nestedTransportSessionHandler) RegisterNestedClientTransportForExec(
	_ context.Context,
	metadata *engine.ClientMetadata,
	_ string,
	_ string,
) (*engine.NestedClientTransport, error) {
	handler.registered.Add(1)
	transport := engine.NewNestedClientTransport(func() { handler.closed.Add(1) })
	return transport, nil
}

func (handler *nestedTransportSessionHandler) ServeHTTPToNestedClient(
	http.ResponseWriter,
	*http.Request,
	*engine.NestedClientTransport,
	*engine.ClientMetadata,
	string,
	bool,
	dagql.AnyObjectResult,
	dagql.Typed,
) {
	handler.served.Add(1)
}

func TestContainerNestedTransportsAreExactPerRequestClient(t *testing.T) {
	t.Parallel()

	handler := &nestedTransportSessionHandler{}
	manager := newNestedClientTransportManager(t.Context(), handler, &engine.ClientMetadata{
		SessionID:         "session",
		ClientID:          "proxy-template",
		ClientSecretToken: "proxy-secret",
	}, "parent")

	request := func(metadata engine.ClientMetadata) (*engine.NestedClientTransport, *engine.ClientMetadata, int, error) {
		req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
		req.Header = metadata.AppendToHTTPHeaders(req.Header)
		return manager.transportForRequest(req)
	}

	first, firstMetadata, status, err := request(engine.ClientMetadata{
		ClientID:          "first",
		SessionID:         "untrusted-session",
		ClientSecretToken: "untrusted-secret",
	})
	require.NoError(t, err)
	require.Zero(t, status)
	require.Equal(t, "first", firstMetadata.ClientID)
	require.Equal(t, "session", firstMetadata.SessionID)
	require.Equal(t, "proxy-secret", firstMetadata.ClientSecretToken)
	require.False(t, first.Closed())
	require.Equal(t, int32(1), handler.registered.Load())

	same, sameMetadata, _, err := request(engine.ClientMetadata{ClientID: "first"})
	require.NoError(t, err)
	require.Same(t, first, same)
	require.Same(t, firstMetadata, sameMetadata)
	require.Equal(t, int32(1), handler.registered.Load())

	second, secondMetadata, _, err := request(engine.ClientMetadata{ClientID: "second"})
	require.NoError(t, err)
	require.NotSame(t, first, second)
	require.Equal(t, "second", secondMetadata.ClientID)
	require.Equal(t, int32(2), handler.registered.Load())

	first.Close()
	closed, _, _, err := request(engine.ClientMetadata{ClientID: "first"})
	require.NoError(t, err)
	require.Same(t, first, closed, "a closed client ID must never be rebound")
	require.True(t, closed.Closed())
	require.Equal(t, int32(2), handler.registered.Load())

	manager.Close()
	require.True(t, second.Closed())
	require.Equal(t, int32(2), handler.closed.Load())
	manager.Close()
	require.Equal(t, int32(2), handler.closed.Load(), "manager cleanup must be idempotent")

	_, _, status, err = request(engine.ClientMetadata{ClientID: "third"})
	require.Error(t, err)
	require.Equal(t, http.StatusGone, status)
}

func TestContainerNestedTransportManagerUsesExactHeaderlessProxyIdentity(t *testing.T) {
	t.Parallel()

	handler := &nestedTransportSessionHandler{}
	manager := newNestedClientTransportManager(t.Context(), handler, &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "proxy-template",
	}, "parent")

	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	transport, metadata, status, err := manager.transportForRequest(req)
	require.NoError(t, err)
	require.Equal(t, "proxy-template", metadata.ClientID)
	require.False(t, transport.Closed())
	require.Zero(t, status)
	require.Equal(t, int32(1), handler.registered.Load())

	malformed := httptest.NewRequest(http.MethodGet, "/query", nil)
	malformed.Header.Set(engine.ClientMetadataMetaKey, "not-base64")
	_, _, status, err = manager.transportForRequest(malformed)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, int32(1), handler.registered.Load(), "malformed metadata must not fall through to the proxy identity")

	emptyID := httptest.NewRequest(http.MethodGet, "/query", nil)
	emptyID.Header = (engine.ClientMetadata{}).AppendToHTTPHeaders(emptyID.Header)
	_, _, status, err = manager.transportForRequest(emptyID)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, int32(1), handler.registered.Load(), "an explicit empty ID must not fall through to the proxy identity")

	manager.Close()
	require.Equal(t, int32(1), handler.closed.Load())
	manager.Close()
	require.Equal(t, int32(1), handler.closed.Load())
}
