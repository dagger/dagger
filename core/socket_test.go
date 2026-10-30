package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	daggerotel "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"google.golang.org/grpc"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
)

// testAgentSSHServer serves an SSH agent to the engine, as a client's
// session attachables do.
type testAgentSSHServer struct {
	sshforward.UnimplementedSSHServer
	agent agent.Agent
}

func (srv *testAgentSSHServer) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	engineSide, agentSide := net.Pipe()
	go agent.ServeAgent(srv.agent, agentSide)
	return sshforward.Copy(stream.Context(), engineSide, stream, nil)
}

// The time limit on listing an SSH agent's identities is for the agent to
// answer. The engine's own setup before it asks, which a loaded engine can
// stall, doesn't count against it.
func TestAgentFingerprintsOutlastStalledSetup(t *testing.T) {
	t.Parallel()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	assert.NilError(t, err)
	keyring := agent.NewKeyring()
	assert.NilError(t, keyring.Add(agent.AddedKey{PrivateKey: key}))
	pub, err := ssh.NewPublicKey(key.Public())
	assert.NilError(t, err)

	conn := newTestAttachableConn(t, func(server *grpc.Server) {
		sshforward.RegisterSSHServer(server, &testAgentSSHServer{agent: keyring})
	})
	ctx, _ := newSessionResourceFallbackTestContext(t, map[string]*grpc.ClientConn{
		"agent-client": conn,
	})

	// A metrics collection holds its meter provider while its callbacks run,
	// so the first instrument that mounting the agent creates waits for it.
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	collecting := make(chan struct{})
	release := make(chan struct{})
	_, err = provider.Meter("stall").Int64ObservableGauge("stall", metric.WithInt64Callback(
		func(context.Context, metric.Int64Observer) error {
			close(collecting)
			<-release
			return nil
		},
	))
	assert.NilError(t, err)
	go reader.Collect(context.Background(), &metricdata.ResourceMetrics{})
	<-collecting
	time.AfterFunc(sshAgentIdentityTimeout+time.Second, func() { close(release) })
	ctx = daggerotel.WithMeterProvider(ctx, provider)

	socket := &Socket{
		Kind:           SocketKindUnixOpaque,
		URLVal:         "unix:///run/agent.sock",
		SourceClientID: "agent-client",
	}
	fingerprints, err := socket.AgentFingerprints(ctx)
	assert.NilError(t, err)
	assert.DeepEqual(t, fingerprints, []string{ssh.FingerprintSHA256(pub)})
}

// silentSSHServer forwards to an agent that never answers.
type silentSSHServer struct {
	sshforward.UnimplementedSSHServer
}

func (*silentSSHServer) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	<-stream.Context().Done()
	return nil
}

// An agent that never answers fails the lookup once its time is up, rather
// than holding up module loading.
func TestAgentFingerprintsGiveUpOnSilentAgent(t *testing.T) {
	t.Parallel()
	conn := newTestAttachableConn(t, func(server *grpc.Server) {
		sshforward.RegisterSSHServer(server, &silentSSHServer{})
	})
	ctx, _ := newSessionResourceFallbackTestContext(t, map[string]*grpc.ClientConn{
		"agent-client": conn,
	})
	socket := &Socket{
		Kind:           SocketKindUnixOpaque,
		URLVal:         "unix:///run/agent.sock",
		SourceClientID: "agent-client",
	}
	ctx, cancel := context.WithTimeout(ctx, 3*sshAgentIdentityTimeout)
	defer cancel()
	_, err := socket.AgentFingerprints(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "failed to list SSH agent identities")
	assert.NilError(t, ctx.Err(), "the lookup's own time limit ended it")
}
