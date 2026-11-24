package client

import (
	"context"
	"net"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	"github.com/dagger/dagger/engine"
	controlapi "github.com/dagger/dagger/internal/buildkit/api/services/control"
	apitypes "github.com/dagger/dagger/internal/buildkit/api/types"
)

// Connecting asks the engine for its version and size once: with a remote
// engine, every extra ask is another round trip before the session starts.
func TestNewBuildkitClientAsksForInfoOnce(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	control := &countingControl{}
	controlapi.RegisterControlServer(srv, control)
	go srv.Serve(lis)
	defer srv.Stop()

	remote, err := url.Parse("tcp://engine")
	require.NoError(t, err)
	c, info, err := newBuildkitClient(t.Context(), remote, connectorFunc(lis.DialContext))
	require.NoError(t, err)
	defer c.Close()

	require.Equal(t, engine.Package, info.BuildkitVersion.Package)
	require.Equal(t, "v9.9.9", info.BuildkitVersion.Version)
	require.Equal(t, 7, info.SystemInfo.NumCPU)
	require.EqualValues(t, 1, control.infoCalls.Load())
}

type countingControl struct {
	controlapi.UnimplementedControlServer
	infoCalls atomic.Int32
}

func (c *countingControl) Info(context.Context, *controlapi.InfoRequest) (*controlapi.InfoResponse, error) {
	c.infoCalls.Add(1)
	return &controlapi.InfoResponse{
		BuildkitVersion: &apitypes.BuildkitVersion{Package: engine.Package, Version: "v9.9.9"},
		SystemInfo:      &controlapi.SystemInfo{NumCPU: 7},
	}, nil
}

type connectorFunc func(context.Context) (net.Conn, error)

func (f connectorFunc) Connect(ctx context.Context) (net.Conn, error) { return f(ctx) }
func (f connectorFunc) EngineID() string                              { return "" }
func (f connectorFunc) ClaimID() string                               { return "" }
