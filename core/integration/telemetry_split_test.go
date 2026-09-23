package core

// These tests cover who publishes a session's telemetry to Dagger Cloud: the
// engine, when its client asked and the engine confirmed on the telemetry
// stream, or the client, forwarding what the engine streams to it. Whatever
// the versions of the client, the engine and a scale-out engine, every
// record reaches Cloud from exactly one writer.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// telemetrySplitReleasedEngine is a release without the split: its engine
// never publishes session telemetry nor confirms it, and its CLI always
// forwards engine telemetry to Cloud.
const telemetrySplitReleasedEngine = "registry.dagger.io/engine:v1.0.0-beta.14"

// telemetrySplitCloud is a fake Dagger Cloud and a container that reads what
// it received.
type telemetrySplitCloud struct {
	service  *dagger.Service
	reader   *dagger.Container
	eventsID string
}

func newTelemetrySplitCloud(t *testctx.T, c *dagger.Client, withs ...dagger.WithContainerFunc) telemetrySplitCloud {
	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)
	code := c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{
			"core/integration/testdata/telemetry/",
			"go.mod",
			"go.sum",
		},
	})
	base := c.Container().
		From(golangImage).
		With(goCache(c)).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithMountedCache("/events", c.CacheVolume("dagger-telemetry-split-events-"+identity.NewID()))
	svc := base
	for _, with := range withs {
		svc = svc.With(with)
	}
	return telemetrySplitCloud{
		service: svc.
			WithDefaultArgs([]string{"go", "run", "./core/integration/testdata/telemetry/"}).
			WithExposedPort(8080).
			AsService(),
		reader:   base,
		eventsID: identity.NewID(),
	}
}

// bind gives a container the fake Cloud as its Dagger Cloud.
func (cloud telemetrySplitCloud) bind(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithServiceBinding("cloud", cloud.service).
		WithEnvVariable("DAGGER_CLOUD_URL", "http://cloud:8080/"+cloud.eventsID)
}

type telemetrySplitSpan struct {
	Writer   string `json:"writer"`
	Service  string `json:"service"`
	Instance string `json:"instance"`
	SpanID   string `json:"spanID"`
	Name     string `json:"name"`
}

type telemetrySplitRecord struct {
	Writer   string `json:"writer"`
	Service  string `json:"service"`
	Instance string `json:"instance"`
	Scope    string `json:"scope"`
	Body     string `json:"body"`
}

func readTelemetrySplitLines[T any](ctx context.Context, t *testctx.T, cloud telemetrySplitCloud, path string) []T {
	raw, err := cloud.reader.
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithExec([]string{"sh", "-c", fmt.Sprintf("cat /events/%s/%s 2>/dev/null || true", cloud.eventsID, path)}).
		Stdout(ctx)
	require.NoError(t, err)
	var out []T
	for line := range strings.SplitSeq(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var v T
		require.NoError(t, json.Unmarshal([]byte(line), &v))
		out = append(out, v)
	}
	return out
}

// telemetrySplitReceived is what the fake Cloud received.
type telemetrySplitReceived struct {
	spans   []telemetrySplitSpan
	records []telemetrySplitRecord
}

func readTelemetrySplit(ctx context.Context, t *testctx.T, cloud telemetrySplitCloud) telemetrySplitReceived {
	return telemetrySplitReceived{
		spans:   readTelemetrySplitLines[telemetrySplitSpan](ctx, t, cloud, "v1/traces.json.spans"),
		records: readTelemetrySplitLines[telemetrySplitRecord](ctx, t, cloud, "v1/logs.json.records"),
	}
}

// cliWriters are the writers of the client's own spans, one per client
// process.
func (got telemetrySplitReceived) cliWriters(t *testctx.T) map[string]bool {
	writers := map[string]bool{}
	for _, span := range got.spans {
		if span.Service == "dagger-cli" {
			writers[span.Writer] = true
		}
	}
	require.NotEmpty(t, writers, "the client's own spans reach Cloud from the client")
	return writers
}

// cliLogWriters are the writers of the client's own log records. Every
// exporter set has one writer per signal, so log records are only ever
// compared with log writers.
func (got telemetrySplitReceived) cliLogWriters(t *testctx.T) map[string]bool {
	writers := map[string]bool{}
	for _, rec := range got.records {
		if rec.Service == "dagger-cli" {
			writers[rec.Writer] = true
		}
	}
	require.NotEmpty(t, writers, "the client's own log records reach Cloud from the client")
	return writers
}

// output returns the log record carrying an exec's output, which must reach
// Cloud exactly once.
func (got telemetrySplitReceived) output(t *testctx.T, body string) telemetrySplitRecord {
	var outputs []telemetrySplitRecord
	for _, rec := range got.records {
		if strings.Contains(rec.Body, body) {
			outputs = append(outputs, rec)
		}
	}
	require.Len(t, outputs, 1, "the exec's output reaches Cloud once: %+v", outputs)
	return outputs[0]
}

// requireSpansFromOneWriter requires that every span reached Cloud from one
// writer: none was published twice.
func (got telemetrySplitReceived) requireSpansFromOneWriter(t *testctx.T) {
	writers := map[string]map[string]bool{}
	for _, span := range got.spans {
		if writers[span.SpanID] == nil {
			writers[span.SpanID] = map[string]bool{}
		}
		writers[span.SpanID][span.Writer] = true
	}
	for spanID, ws := range writers {
		require.Len(t, ws, 1, "span %s reaches Cloud from one writer", spanID)
	}
}

// telemetrySplitEngine is an engine container from ctr that serves on
// tcp://:1234 and reaches the fake Cloud. It has no Cloud token of its own,
// so it exports no cache facts.
func telemetrySplitEngine(c *dagger.Client, ctr *dagger.Container, cloud telemetrySplitCloud) *dagger.Container {
	deviceName, cidr := testutil.GetUniqueNestedEngineNetwork()
	return cloud.bind(ctr).
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-telemetry-split-state-"+identity.NewID())).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithDefaultArgs([]string{
			"--addr", "tcp://0.0.0.0:1234",
			"--network-name", deviceName,
			"--network-cidr", cidr,
		})
}

func telemetrySplitEngineBase(c *dagger.Client, released bool) *dagger.Container {
	if released {
		return c.Container().From(telemetrySplitReleasedEngine)
	}
	return devEngineContainer(c)
}

// telemetrySplitClient is a container running cli against the engine, with
// the fake Cloud as its Dagger Cloud and a token for it.
func telemetrySplitClient(ctx context.Context, t *testctx.T, c *dagger.Client, cli *dagger.File, engine *dagger.Service, cloud telemetrySplitCloud) *dagger.Container {
	endpoint, err := engine.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	require.NoError(t, err)
	return cloud.bind(c.Container().From(alpineImage)).
		WithServiceBinding("dev-engine", engine).
		WithMountedFile("/bin/dagger", cli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithEnvVariable("DAGGER_CLOUD_TOKEN", "test")
}

// TestTelemetrySplitPublishesOnce runs one exec through each pairing of a
// client and an engine with and without the split, and requires its output
// to reach Cloud once, from the engine when both have the split and from the
// client otherwise, and no span to reach Cloud twice.
func (ClientSuite) TestTelemetrySplitPublishesOnce(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	devCLI := daggerCliFile(t, c)
	releasedCLI := c.Container().From(telemetrySplitReleasedEngine).File("/usr/local/bin/dagger")

	for _, tc := range []struct {
		name           string
		releasedCLI    bool
		releasedEngine bool
	}{
		{name: "new client, new engine"},
		{name: "old client, new engine", releasedCLI: true},
		{name: "new client, old engine", releasedEngine: true},
		{name: "old client, old engine", releasedCLI: true, releasedEngine: true},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			cloud := newTelemetrySplitCloud(t, c)
			engine, err := devEngineContainerAsService(telemetrySplitEngine(c, telemetrySplitEngineBase(c, tc.releasedEngine), cloud)).Start(ctx)
			require.NoError(t, err)
			cli := devCLI
			if tc.releasedCLI {
				cli = releasedCLI
			}

			marker := identity.NewID()
			// The exec prints "<marker>-out", which appears nowhere else.
			query := fmt.Sprintf(`{ container { from(address: %q) { withExec(args: ["sh", "-c", "echo $0-out", %q]) { exitCode } } } }`, alpineImage, marker)
			_, err = telemetrySplitClient(ctx, t, c, cli, engine, cloud).
				WithNewFile("/query.graphql", query).
				WithExec([]string{"/bin/dagger", "query", "--doc", "/query.graphql"}).
				Sync(ctx)
			require.NoError(t, err)

			got := readTelemetrySplit(ctx, t, cloud)
			got.requireSpansFromOneWriter(t)
			cliWriters := got.cliWriters(t)
			output := got.output(t, marker+"-out")
			enginePublishes := !tc.releasedCLI && !tc.releasedEngine
			require.Equal(t, !enginePublishes, got.cliLogWriters(t)[output.Writer],
				"the output is published by the engine exactly when both sides have the split")
			var execSpans int
			for _, span := range got.spans {
				if strings.Contains(span.Name, "withExec") {
					execSpans++
					require.Equal(t, !enginePublishes, cliWriters[span.Writer], "span %s %q", span.SpanID, span.Name)
				}
			}
			require.Positive(t, execSpans, "the exec's span reaches Cloud")
		})
	}
}

// telemetrySplitTLS is a test CA's server certificate for the scale-out
// engine's TLS endpoint, and a client certificate for the parent engine.
type telemetrySplitTLS struct {
	caPEM, serverPEM string
	clientCert       *cloud.SerializableCertificate
}

func newTelemetrySplitTLS(t *testctx.T, serverName string) telemetrySplitTLS {
	newKey := func() *ecdsa.PrivateKey {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		return key
	}
	template := func(serial int64, cn string) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
		}
	}
	pemBlock := func(typ string, der []byte) string {
		return string(pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}))
	}

	caKey := newKey()
	caTmpl := template(1, "telemetry split test CA")
	caTmpl.IsCA = true
	caTmpl.BasicConstraintsValid = true
	caTmpl.KeyUsage = x509.KeyUsageCertSign
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	serverKey := newKey()
	serverTmpl := template(2, serverName)
	serverTmpl.DNSNames = []string{serverName}
	serverTmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	serverTmpl.KeyUsage = x509.KeyUsageDigitalSignature
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTmpl, ca, &serverKey.PublicKey, caKey)
	require.NoError(t, err)
	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	require.NoError(t, err)

	clientKey := newKey()
	clientTmpl := template(3, "parent engine")
	clientTmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	clientTmpl.KeyUsage = x509.KeyUsageDigitalSignature
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTmpl, ca, &clientKey.PublicKey, caKey)
	require.NoError(t, err)
	clientKeyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	require.NoError(t, err)

	return telemetrySplitTLS{
		caPEM:      pemBlock("CERTIFICATE", caDER),
		serverPEM:  pemBlock("CERTIFICATE", serverDER) + pemBlock("PRIVATE KEY", serverKeyDER),
		clientCert: &cloud.SerializableCertificate{CertificateChain: [][]byte{clientDER}, PrivateKey: clientKeyDER},
	}
}

// telemetrySplitScaleOutModule is a module with one check, whose exec prints
// "<marker>-out".
func telemetrySplitScaleOutModule(c *dagger.Client, marker string) *dagger.Directory {
	return c.Host().Directory("./testdata/checks/hello-with-checks", dagger.HostDirectoryOpts{
		Include: []string{"dagger.json", "go.mod", "go.sum"},
	}).WithNewFile("main.go", fmt.Sprintf(`// A module with one check for the telemetry split's scale-out test
package main

import "context"

type HelloWithChecks struct{}

// Prints a marker on the engine the check runs on
// +check
func (m *HelloWithChecks) SplitCheck(ctx context.Context) error {
	_, err := dag.Container().From(%q).WithExec([]string{"sh", "-c", "echo $0-out", %q}).Sync(ctx)
	return err
}
`, alpineImage, marker))
}

// TestTelemetrySplitScaleOut runs a check scaled out from a new parent
// engine to a remote engine, for each pairing of an old or new client with
// an old or new remote. The remote publishes its own session only when the
// parent's session publishes and the remote confirms; a parent that
// publishes publishes an unconfirmed remote's stream itself; a client that
// forwards forwards everything. The check's output reaches Cloud once.
func (ClientSuite) TestTelemetrySplitScaleOut(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	devCLI := daggerCliFile(t, c)
	releasedCLI := c.Container().From(telemetrySplitReleasedEngine).File("/usr/local/bin/dagger")

	for _, tc := range []struct {
		name           string
		releasedCLI    bool
		releasedRemote bool
	}{
		{name: "new client, new remote"},
		{name: "new client, old remote", releasedRemote: true},
		{name: "old client, new remote", releasedCLI: true},
		{name: "old client, old remote", releasedCLI: true, releasedRemote: true},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			const remoteHost = "remote-tls"
			tlsFiles := newTelemetrySplitTLS(t, remoteHost)
			spec, err := json.Marshal(cloud.EngineSpec{
				URL:            remoteHost + ":8443",
				CertSerialized: tlsFiles.clientCert,
				InstanceID:     "telemetry-split-remote",
			})
			require.NoError(t, err)
			fakeCloud := newTelemetrySplitCloud(t, c, func(ctr *dagger.Container) *dagger.Container {
				return ctr.WithEnvVariable("FAKE_CLOUD_ENGINE_SPEC", string(spec))
			})

			remote := devEngineContainerAsService(telemetrySplitEngine(c, telemetrySplitEngineBase(c, tc.releasedRemote), fakeCloud))
			// Terminates the parent engine's TLS for the remote engine, as
			// Dagger Cloud's engine endpoint does.
			tlsProxy := c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "socat"}).
				WithServiceBinding("remote-engine", remote).
				WithNewFile("/tls/server.pem", tlsFiles.serverPEM).
				WithExposedPort(8443).
				WithDefaultArgs([]string{"socat", "OPENSSL-LISTEN:8443,reuseaddr,fork,cert=/tls/server.pem,verify=0", "TCP:remote-engine:1234"}).
				AsService()
			parent, err := devEngineContainerAsService(telemetrySplitEngine(c, devEngineContainer(c), fakeCloud).
				WithServiceBinding(remoteHost, tlsProxy).
				// The system roots plus the test CA.
				WithNewFile("/tls/certs/ca.pem", tlsFiles.caPEM).
				WithEnvVariable("SSL_CERT_DIR", "/etc/ssl/certs:/tls/certs")).Start(ctx)
			require.NoError(t, err)

			cli := devCLI
			if tc.releasedCLI {
				cli = releasedCLI
			}
			marker := identity.NewID()
			_, err = telemetrySplitClient(ctx, t, c, cli, parent, fakeCloud).
				WithExec([]string{"apk", "add", "git"}).
				WithWorkdir("/work").
				WithExec([]string{"git", "init"}).
				WithDirectory("/work/mod", telemetrySplitScaleOutModule(c, marker)).
				WithWorkdir("/work/mod").
				WithExec([]string{"/bin/dagger", "--progress=plain", "check", "--scale-out", "split-check"}).
				Sync(ctx)
			require.NoError(t, err)

			got := readTelemetrySplit(ctx, t, fakeCloud)
			got.requireSpansFromOneWriter(t)
			cliLogWriters := got.cliLogWriters(t)
			parentInstances := map[string]bool{}
			parentLogWriters := map[string]bool{}
			for _, span := range got.spans {
				if span.Name == "connect to cloud engine" {
					parentInstances[span.Instance] = true
				}
			}
			require.NotEmpty(t, parentInstances, "the check was scaled out")
			for _, rec := range got.records {
				if parentInstances[rec.Instance] {
					parentLogWriters[rec.Writer] = true
				}
			}
			require.NotEmpty(t, parentLogWriters, "the parent engine's own log records reach Cloud")
			output := got.output(t, marker+"-out")
			require.False(t, parentInstances[output.Instance], "the check ran on the remote engine")

			switch {
			case tc.releasedCLI:
				require.True(t, cliLogWriters[output.Writer], "the client forwards everything")
			case tc.releasedRemote:
				require.False(t, cliLogWriters[output.Writer])
				require.True(t, parentLogWriters[output.Writer], "the parent publishes an unconfirmed remote's stream")
			default:
				require.False(t, cliLogWriters[output.Writer])
				require.False(t, parentLogWriters[output.Writer], "the remote publishes its own session")
			}
		})
	}
}

// telemetrySplitMarkerModule is a module whose function, run as a nested
// client, opens a span of its own, which the SDK posts to the engine from
// inside its container, and prints a marker assembled inside its exec.
func telemetrySplitMarkerModule(c *dagger.Client) *dagger.Directory {
	return c.Directory().
		WithNewFile("dagger.json", `{"name": "marker", "sdk": "go", "source": "."}`).
		WithNewFile("main.go", fmt.Sprintf(`package main

import "context"

type Marker struct{}

func (m *Marker) Emit(ctx context.Context, prefix string, id string) error {
	// A span the SDK posts to the engine from inside the runtime container.
	ctx, span := Tracer().Start(ctx, "posted-span-"+id)
	defer span.End()
	_, err := dag.Container().
		From(%q).
		WithEnvVariable("MARKER_PREFIX", prefix).
		WithEnvVariable("MARKER_ID", id).
		WithExec([]string{"sh", "-c", "echo $MARKER_PREFIX$MARKER_ID"}).
		Sync(ctx)
	return err
}
`, alpineImage))
}

// markerExecArgs runs an exec printing prefix+id through the CLI. Each
// marker ships as two halves, assembled only inside the exec: the client's
// root span is named after its command line, and the client exports its own
// telemetry to the same fake Cloud.
func markerExecArgs(prefix, id string) []string {
	return []string{
		"/bin/dagger", "core", "container",
		"from", "--address=" + alpineImage,
		"with-env-variable", "--name=MARKER_PREFIX", "--value=" + prefix,
		"with-env-variable", "--name=MARKER_ID", "--value=" + id,
		"with-exec", "--args=sh", "--args=-c", "--args=echo $MARKER_PREFIX$MARKER_ID",
		"stdout",
	}
}

// TestEngineTelemetryToCloud (from #13339): the engine publishes the
// session's telemetry, the main client's and a nested client's (a module
// function), including a span the module's SDK posts from inside its
// container, with the credential and Cloud URL the client provides, over all
// three signals.
func (ClientSuite) TestEngineTelemetryToCloud(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cloud := newTelemetrySplitCloud(t, c)
	engine, err := devEngineContainerAsService(telemetrySplitEngine(c, devEngineContainer(c), cloud)).Start(ctx)
	require.NoError(t, err)

	mainID, nestedID := identity.NewID(), identity.NewID()
	_, err = telemetrySplitClient(ctx, t, c, daggerCliFile(t, c), engine, cloud).
		WithExec(markerExecArgs("main-marker-", mainID)).
		WithDirectory("/work/marker", telemetrySplitMarkerModule(c)).
		WithWorkdir("/work/marker").
		WithExec([]string{"/bin/dagger", "call", "-m", ".", "emit", "--prefix=nested-marker-", "--id=" + nestedID}).
		Sync(ctx)
	require.NoError(t, err)

	got := readTelemetrySplit(ctx, t, cloud)
	got.requireSpansFromOneWriter(t)
	cliLogWriters := got.cliLogWriters(t)
	for name, marker := range map[string]string{
		"main client":                     "main-marker-" + mainID,
		"nested client (module function)": "nested-marker-" + nestedID,
	} {
		output := got.output(t, marker)
		require.False(t, cliLogWriters[output.Writer], "the engine publishes the %s's output", name)
	}

	// Telemetry posted from inside a container reaches the engine's client
	// routing, not its providers; the engine publishes it too.
	cliWriters := got.cliWriters(t)
	var posted int
	for _, span := range got.spans {
		if span.Name == "posted-span-"+nestedID {
			posted++
			require.False(t, cliWriters[span.Writer], "the engine publishes the span the SDK posted")
		}
	}
	require.Positive(t, posted, "the span the SDK posted reaches Cloud")

	// Each signal has its own writers: an engine resource's metrics that
	// the client forwarded would carry one of the client's metric writers.
	type metricWriter struct {
		Writer  string `json:"writer"`
		Service string `json:"service"`
	}
	metricWriters := readTelemetrySplitLines[metricWriter](ctx, t, cloud, "v1/metrics.json.writers")
	cliMetricWriters := map[string]bool{}
	for _, mw := range metricWriters {
		if mw.Service == "dagger-cli" {
			cliMetricWriters[mw.Writer] = true
		}
	}
	var engineMetrics bool
	for _, mw := range metricWriters {
		if mw.Service != "dagger-cli" {
			require.False(t, cliMetricWriters[mw.Writer], "the client does not forward the engine's metrics")
			engineMetrics = true
		}
	}
	require.True(t, engineMetrics, "the engine publishes metrics")
}

// TestEngineTelemetryCloudOAuthRefresh (from #13339): with a `dagger login`
// token that outlives its access token, the engine refreshes it from the
// client host's credentials file, through the session's attachables, exports
// all three signals with the refreshed token, and writes it back. The fake
// Cloud issues tokens that expire at once and refuses any it did not issue,
// and the engine and the client refresh through different token endpoints,
// so the request log pins each export to the engine.
func (ClientSuite) TestEngineTelemetryCloudOAuthRefresh(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cloud := newTelemetrySplitCloud(t, c)
	engine, err := devEngineContainerAsService(telemetrySplitEngine(c, devEngineContainer(c), cloud).
		WithEnvVariable("DAGGER_CLOUD_AUTH_URL", "http://cloud:8080/"+cloud.eventsID+"/engine")).Start(ctx)
	require.NoError(t, err)
	endpoint, err := engine.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	require.NoError(t, err)

	markerID := identity.NewID()
	staleCreds := `{"access_token":"stale-token","token_type":"Bearer","refresh_token":"test-refresh-token","expiry":"2020-01-01T00:00:00Z"}`
	clientCtr := cloud.bind(c.Container().From(alpineImage)).
		WithServiceBinding("dev-engine", engine).
		WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithEnvVariable("XDG_CONFIG_HOME", "/root/.config").
		WithNewFile("/root/.config/dagger/credentials.json", staleCreds).
		WithNewFile("/root/.config/dagger/org", `{"id":"org-telemetry-test","name":"telemetry-test"}`).
		WithEnvVariable("DAGGER_CLOUD_AUTH_URL", "http://cloud:8080/"+cloud.eventsID+"/client").
		WithExec(markerExecArgs("refresh-marker-", markerID))
	_, err = clientCtr.Sync(ctx)
	require.NoError(t, err)

	got := readTelemetrySplit(ctx, t, cloud)
	output := got.output(t, "refresh-marker-"+markerID)
	require.False(t, got.cliLogWriters(t)[output.Writer], "the engine publishes the output")

	events := cloud.reader.WithEnvVariable("CACHEBUSTER", identity.NewID())
	_, err = events.
		WithExec([]string{"test", "-s", fmt.Sprintf("/events/%s/engine/issued-tokens.txt", cloud.eventsID)}).
		Sync(ctx)
	require.NoError(t, err, "the engine never refreshed the OAuth token")
	for _, signal := range []string{"traces", "logs", "metrics"} {
		_, err := events.
			WithExec([]string{"grep", "-E",
				fmt.Sprintf("^fresh-token-.*-engine-[0-9]+ /%s/v1/%s$", cloud.eventsID, signal),
				"/events/requests.log",
			}).
			Sync(ctx)
		require.NoError(t, err, "the engine exported no %s with a refreshed token", signal)
	}

	creds, err := clientCtr.WithExec([]string{"cat", "/root/.config/dagger/credentials.json"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, creds, "fresh-token-", "the refreshed token is written back")
	require.NotContains(t, creds, "stale-token")
}

// TestEngineTelemetryCloudOutage (from #13339): a Cloud that accepts
// requests and never answers costs telemetry, never the build. The fake
// Cloud's /hang/ paths read each request and then sit on it for 60s, longer
// than any exporter timeout: a hanging Cloud, not a refusing one. The client
// fails the command if the engine takes over 10s to answer /shutdown, so the
// command's success shows that all of the engine's waits on Cloud during
// shutdown stayed within that.
func (ClientSuite) TestEngineTelemetryCloudOutage(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cloud := newTelemetrySplitCloud(t, c)
	cloud.eventsID = "hang/" + cloud.eventsID
	engine, err := devEngineContainerAsService(telemetrySplitEngine(c, devEngineContainer(c), cloud)).Start(ctx)
	require.NoError(t, err)
	_, err = telemetrySplitClient(ctx, t, c, daggerCliFile(t, c), engine, cloud).
		WithExec([]string{
			"/bin/dagger", "core", "container",
			"from", "--address=" + alpineImage,
			"with-exec", "--args=true",
			"stdout",
		}).
		Sync(ctx)
	require.NoError(t, err, "a hanging Cloud must never fail the build")
}
