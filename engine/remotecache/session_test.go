package remotecache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// These tests drive a session's handlers directly, through dispatch, over a
// connection that records what the engine writes. No transport is involved:
// the wire itself is tested where the service lives.

type recordingConn struct {
	sent   chan protocol.Envelope
	closed atomic.Bool
}

func newRecordingConn() *recordingConn {
	return &recordingConn{sent: make(chan protocol.Envelope, 64)}
}

func (c *recordingConn) Read(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, context.Cause(ctx)
}

func (c *recordingConn) Write(_ context.Context, data []byte) error {
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	c.sent <- env
	return nil
}

func (c *recordingConn) Ping(context.Context) error { return nil }

func (c *recordingConn) CloseNow() error {
	c.closed.Store(true)
	return nil
}

// next returns the next message the engine sent.
func (c *recordingConn) next(t *testing.T) protocol.Envelope {
	t.Helper()
	select {
	case env := <-c.sent:
		return env
	case <-time.After(10 * time.Second):
		t.Fatal("the engine sent nothing")
		return protocol.Envelope{}
	}
}

// none checks the engine sent nothing more within a short wait.
func (c *recordingConn) none(t *testing.T) {
	t.Helper()
	select {
	case env := <-c.sent:
		t.Fatalf("unexpected %s", env.Type)
	case <-time.After(50 * time.Millisecond):
	}
}

// serviceConn is a recordingConn the test also writes to, as the service
// would.
type serviceConn struct {
	*recordingConn
	inbound chan []byte
}

func newServiceConn() *serviceConn {
	return &serviceConn{recordingConn: newRecordingConn(), inbound: make(chan []byte, 8)}
}

func (c *serviceConn) Read(ctx context.Context) ([]byte, error) {
	select {
	case data := <-c.inbound:
		return data, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (c *serviceConn) send(t *testing.T, env protocol.Envelope) {
	t.Helper()
	data, err := json.Marshal(env)
	require.NoError(t, err)
	c.inbound <- data
}

type fakeAdapter struct {
	export   func(context.Context, protocol.Export, func(context.Context, Export) error) error
	merge    func(context.Context, protocol.Merge) (protocol.Merged, error)
	offer    func(context.Context, protocol.Offer) (protocol.Offered, error)
	renewals chan *dagql.RenewalRequest
	replies  chan dagql.RenewalReply
	// misses are what TakeLookupMisses returns, lookups receive true on
	// StartLookups and false on StopLookups, and expected the ExpectMerge
	// calls.
	misses    chan []dagql.RemoteMiss
	lookups   chan bool
	expected  chan expectedMerge
	takeLimit atomic.Int64
	mergeIDs  chan uint64
}

type expectedMerge struct {
	recipe   digest.Digest
	wait     *dagql.RemoteMergeWait
	deadline time.Time
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		renewals: make(chan *dagql.RenewalRequest, 8),
		replies:  make(chan dagql.RenewalReply, 8),
		misses:   make(chan []dagql.RemoteMiss, 8),
		lookups:  make(chan bool, 8),
		expected: make(chan expectedMerge, 8),
		mergeIDs: make(chan uint64, 64),
	}
}

func (a *fakeAdapter) CacheIdentity() (string, uint64) { return "cache-1", 3 }

func (a *fakeAdapter) Export(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
	return a.export(ctx, req, consume)
}

func (a *fakeAdapter) Merge(ctx context.Context, id uint64, req protocol.Merge) (protocol.Merged, error) {
	a.mergeIDs <- id
	return a.merge(ctx, req)
}

func (a *fakeAdapter) OfferParts(ctx context.Context, req protocol.Offer) (protocol.Offered, error) {
	return a.offer(ctx, req)
}

func (a *fakeAdapter) TakeRenewalRequest(ctx context.Context) (*dagql.RenewalRequest, error) {
	select {
	case req := <-a.renewals:
		return req, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (a *fakeAdapter) ReplyRenewal(reply dagql.RenewalReply) dagql.RenewalReplyDisposition {
	a.replies <- reply
	return dagql.RenewalReplyAccepted
}

func (a *fakeAdapter) StartLookups() { a.lookups <- true }
func (a *fakeAdapter) StopLookups()  { a.lookups <- false }

func (a *fakeAdapter) TakeLookupMisses(ctx context.Context, limit int, _ time.Duration) ([]dagql.RemoteMiss, uint64, error) {
	a.takeLimit.Store(int64(limit))
	select {
	case misses := <-a.misses:
		return misses, 1, nil
	case <-ctx.Done():
		return nil, 0, context.Cause(ctx)
	}
}

func (a *fakeAdapter) ExpectMerge(recipe digest.Digest, wait *dagql.RemoteMergeWait, deadline time.Time) {
	a.expected <- expectedMerge{recipe: recipe, wait: wait, deadline: deadline}
}

type blobs map[digest.Digest]string

func (b blobs) ReadBlob(_ context.Context, dgst digest.Digest) (io.ReadCloser, int64, error) {
	data, ok := b[dgst]
	if !ok {
		return nil, 0, errors.New("no such blob")
	}
	return io.NopCloser(strings.NewReader(data)), int64(len(data)), nil
}

func testSession(t *testing.T, adapter Adapter) (*session, *recordingConn) {
	t.Helper()
	conn := newRecordingConn()
	cfg := newSettings(Config{EngineVersion: "v0.20.0", EngineName: "dagger-engine"})
	s := newSession(t.Context(), conn, adapter, cfg)
	t.Cleanup(s.close)
	return s, conn
}

func request(t *testing.T, id uint64, typ protocol.Type, body any) protocol.Envelope {
	t.Helper()
	env, err := protocol.NewEnvelope(id, 0, typ, body)
	require.NoError(t, err)
	return env
}

func decode[T any](t *testing.T, env protocol.Envelope) T {
	t.Helper()
	var v T
	require.NoError(t, env.Decode(&v))
	return v
}

func testBundle() *dagql.ValueBundle {
	return &dagql.ValueBundle{Version: 3, Roots: []dagql.TransferredRoot{{Ordinal: 1}}, Values: []dagql.TransferredValue{{Ordinal: 1, SenderNumber: 12}}}
}

// Reconnection waits start at a second, double to 30 seconds, move by at
// most a fifth either way, and start again after a welcome.
func TestBackoff(t *testing.T) {
	var b backoff
	var mid []time.Duration
	for range 8 {
		mid = append(mid, b.next(0.5))
	}
	require.Equal(t, []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second,
	}, mid)
	b.reset()
	require.Equal(t, 800*time.Millisecond, b.next(0))
	require.InDelta(t, float64(2400*time.Millisecond), float64(b.next(0.99999)), float64(time.Millisecond))
}

// The engine's first message says hello with its identity and that it merges
// held rows, numbered 1; the session is served once the service welcomes it.
func TestHello(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.renewals <- renewalRequest(time.Minute)
	s, conn := testSession(t, adapter)
	served := make(chan bool, 1)
	go func() {
		welcomed, _ := s.serve()
		served <- welcomed
	}()
	hello := conn.next(t)
	require.Equal(t, protocol.TypeHello, hello.Type)
	require.Equal(t, uint64(1), hello.ID, "IDs start at 1")
	require.Equal(t, protocol.Hello{CacheID: "cache-1", Generation: 3, EngineVersion: "v0.20.0", EngineName: "dagger-engine", HeldRows: true}, decode[protocol.Hello](t, hello))
	s.dispatch(protocol.Envelope{ID: 1, Re: hello.ID, Type: protocol.TypeWelcome, Body: json.RawMessage(`{}`)})
	// Renewals start only after the welcome is consumed. Wait for one before
	// canceling so the hello reply cannot race with cancellation.
	require.Equal(t, protocol.TypeRenew, conn.next(t).Type)
	s.cancel(errors.New("done"))
	require.True(t, <-served)
}

// An engine configured as ephemeral says so in its hello.
func TestHelloEphemeral(t *testing.T) {
	conn := newRecordingConn()
	s := newSession(t.Context(), conn, newFakeAdapter(), newSettings(Config{EngineVersion: "v0.20.0", EngineName: "dagger-engine", Ephemeral: true}))
	t.Cleanup(s.close)
	go func() { _, _ = s.serve() }()
	hello := conn.next(t)
	require.Equal(t, protocol.TypeHello, hello.Type)
	require.Equal(t, protocol.Hello{CacheID: "cache-1", Generation: 3, EngineVersion: "v0.20.0", EngineName: "dagger-engine", HeldRows: true, Ephemeral: true}, decode[protocol.Hello](t, hello))
	s.cancel(errors.New("done"))
}

// A service that answers hello with an error does not welcome the engine.
func TestHelloRefused(t *testing.T) {
	s, conn := testSession(t, newFakeAdapter())
	served := make(chan error, 1)
	go func() {
		_, err := s.serve()
		served <- err
	}()
	hello := conn.next(t)
	reply, err := protocol.NewEnvelope(1, hello.ID, protocol.TypeError, protocol.Error{Message: "refused"})
	require.NoError(t, err)
	s.dispatch(reply)
	require.ErrorContains(t, <-served, "refused")
}

// An export whose roots are all gone or busy is answered with skipped roots
// only, and holds nothing.
func TestExportWithNoSurvivingRoot(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
		return consume(ctx, Export{Skipped: []protocol.SkippedRoot{{Number: 12, Reason: protocol.SkipGone}, {Number: 13, Reason: protocol.SkipBusy}}})
	}
	s, conn := testSession(t, adapter)
	s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12, 13}}))
	reply := conn.next(t)
	require.Equal(t, protocol.TypeExported, reply.Type)
	require.Equal(t, uint64(7), reply.Re)
	exported := decode[protocol.Exported](t, reply)
	require.Nil(t, exported.Bundle)
	require.Empty(t, exported.ExportID)
	require.Len(t, exported.Skipped, 2)
}

// An export holds its chains until the service's upload, uploads what it
// asks for, answers with what succeeded and failed, and then releases them.
func TestExportUpload(t *testing.T) {
	d1, d2 := digest.FromString("one"), digest.FromString("two")
	released := make(chan struct{})
	adapter := newFakeAdapter()
	adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
		defer close(released)
		return consume(ctx, Export{Bundle: testBundle(), Blobs: blobs{d1: "first"}})
	}
	s, conn := testSession(t, adapter)
	var puts sync.Map
	s.cfg.put = func(_ context.Context, address string, body io.Reader, size int64) error {
		data, err := io.ReadAll(body)
		require.NoError(t, err)
		require.Equal(t, int64(len(data)), size)
		puts.Store(address, string(data))
		return nil
	}
	s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
	exported := decode[protocol.Exported](t, conn.next(t))
	require.NotEmpty(t, exported.ExportID)
	require.Equal(t, testBundle(), exported.Bundle)
	select {
	case <-released:
		t.Fatal("the chains were released before the upload")
	default:
	}

	s.dispatch(request(t, 8, protocol.TypeUpload, protocol.Upload{ExportID: exported.ExportID, URLs: map[digest.Digest]string{d1: "put-1", d2: "put-2"}}))
	reply := conn.next(t)
	require.Equal(t, protocol.TypeUploaded, reply.Type)
	require.Equal(t, uint64(8), reply.Re)
	uploaded := decode[protocol.Uploaded](t, reply)
	require.Equal(t, []digest.Digest{d1}, uploaded.Done)
	require.Len(t, uploaded.Failed, 1)
	require.Equal(t, d2, uploaded.Failed[0].Digest, "a blob the chains lack fails alone")
	got, ok := puts.Load("put-1")
	require.True(t, ok)
	require.Equal(t, "first", got)
	<-released
}

// At most UploadConcurrency PUTs run at once across all the exports, not
// per export, so exports do not starve the engine's own pulls; the queued
// ones proceed as slots open.
func TestUploadConcurrencyIsPerEngine(t *testing.T) {
	const perExport = UploadConcurrency
	adapter := newFakeAdapter()
	var mu sync.Mutex
	exportBlobs := map[uint64]blobs{}
	for root := uint64(1); root <= ExportsInFlight; root++ {
		b := blobs{}
		for i := range perExport {
			b[digest.FromString(fmt.Sprintf("%d-%d", root, i))] = "data"
		}
		exportBlobs[root] = b
	}
	adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
		mu.Lock()
		b := exportBlobs[req.Roots[0]]
		mu.Unlock()
		return consume(ctx, Export{Bundle: testBundle(), Blobs: b})
	}
	s, conn := testSession(t, adapter)
	var (
		running, peak atomic.Int64
		release       = make(chan struct{})
		reached       = make(chan struct{})
		once          sync.Once
		releaseOnce   sync.Once
	)
	// Release the held PUTs however the test ends, before the session's
	// cleanup waits for them.
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	s.cfg.put = func(context.Context, string, io.Reader, int64) error {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		if n == UploadConcurrency {
			once.Do(func() { close(reached) })
		}
		<-release
		return nil
	}
	var exportIDs []string
	for root := uint64(1); root <= ExportsInFlight; root++ {
		s.dispatch(request(t, root, protocol.TypeExport, protocol.Export{Roots: []uint64{root}}))
		exportIDs = append(exportIDs, decode[protocol.Exported](t, conn.next(t)).ExportID)
	}
	for i, exportID := range exportIDs {
		urls := map[digest.Digest]string{}
		for dgst := range exportBlobs[uint64(i+1)] {
			urls[dgst] = "put-" + dgst.String()
		}
		s.dispatch(request(t, uint64(10+i), protocol.TypeUpload, protocol.Upload{ExportID: exportID, URLs: urls}))
	}
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the uploads never reached the limit")
	}
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int64(UploadConcurrency), peak.Load(), "no more than the limit at once, across exports")
	releaseAll()
	for range exportIDs {
		reply := conn.next(t)
		require.Equal(t, protocol.TypeUploaded, reply.Type)
		require.Len(t, decode[protocol.Uploaded](t, reply).Done, perExport)
	}
}

// uploadLogs captures a session's JSON log lines.
type uploadLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *uploadLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *uploadLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// lines returns the decoded lines with msg.
func (l *uploadLogs) lines(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	dec := json.NewDecoder(strings.NewReader(l.String()))
	dec.UseNumber()
	for dec.More() {
		var line map[string]any
		require.NoError(t, dec.Decode(&line))
		if line["msg"] == msg {
			lines = append(lines, line)
		}
	}
	return lines
}

func logDuration(t *testing.T, line map[string]any, key string) time.Duration {
	t.Helper()
	n, ok := line[key].(json.Number)
	require.True(t, ok, "%s is a JSON number: %v", key, line[key])
	v, err := n.Int64()
	require.NoError(t, err)
	return time.Duration(v)
}

// loggedSession is a session whose JSON log is captured.
func loggedSession(t *testing.T, adapter Adapter) (*session, *recordingConn, *uploadLogs) {
	t.Helper()
	s, conn := testSession(t, adapter)
	logs := &uploadLogs{}
	s.cfg.Logger = slog.New(slog.NewJSONHandler(logs, nil))
	return s, conn, logs
}

// waitLines waits for n lines with msg, and returns them.
func (l *uploadLogs) waitLines(t *testing.T, msg string, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		lines := l.lines(t, msg)
		if len(lines) >= n {
			require.Len(t, lines, n)
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d %q lines, want %d", len(lines), msg, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// uploadSession is a session whose JSON log is captured, with one export of
// data held for upload. It returns the export's ID.
func uploadSession(t *testing.T, data blobs) (*session, *recordingConn, *uploadLogs, string) {
	t.Helper()
	adapter := newFakeAdapter()
	adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
		return consume(ctx, Export{Bundle: testBundle(), Blobs: data})
	}
	s, conn, logs := loggedSession(t, adapter)
	s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
	exportID := decode[protocol.Exported](t, conn.next(t)).ExportID
	require.NotEmpty(t, exportID)
	return s, conn, logs, exportID
}

// Each uploaded blob is logged once, at info level, with its export, digest
// and size, how long it queued for a slot and how long its upload took.
func TestUploadLogged(t *testing.T) {
	data := blobs{}
	urls := map[digest.Digest]string{}
	for i := range 3 {
		value := strings.Repeat("x", i+1)
		dgst := digest.FromString(value)
		data[dgst] = value
		urls[dgst] = "put-" + dgst.String()
	}
	s, conn, logs, exportID := uploadSession(t, data)
	s.cfg.put = func(_ context.Context, _ string, body io.Reader, _ int64) error {
		_, err := io.Copy(io.Discard, body)
		return err
	}
	s.dispatch(request(t, 8, protocol.TypeUpload, protocol.Upload{ExportID: exportID, URLs: urls}))
	require.Len(t, decode[protocol.Uploaded](t, conn.next(t)).Done, len(data))
	lines := logs.lines(t, "remote cache blob uploaded")
	require.Len(t, lines, len(data))
	seen := map[digest.Digest]bool{}
	for _, line := range lines {
		require.Equal(t, "INFO", line["level"])
		require.Equal(t, exportID, line["export"])
		dgst := digest.Digest(line["digest"].(string))
		value, ok := data[dgst]
		require.True(t, ok, "a requested digest: %s", dgst)
		require.False(t, seen[dgst], "one line per blob")
		seen[dgst] = true
		require.Equal(t, json.Number(fmt.Sprint(len(value))), line["bytes"])
		require.Equal(t, "ok", line["outcome"])
		require.GreaterOrEqual(t, logDuration(t, line, "queued"), time.Duration(0))
		require.GreaterOrEqual(t, logDuration(t, line, "duration"), time.Duration(0))
		require.NotContains(t, line, "error")
	}
}

// A blob that waits for an upload slot logs the wait: with every slot held,
// the blobs past UploadConcurrency queue until the held uploads end, and the
// others do not. The bounds are the observed intervals: a free slot is taken
// before the slots fill, and a held one after the release.
func TestUploadLoggedQueued(t *testing.T) {
	const hold = 200 * time.Millisecond
	data := blobs{}
	urls := map[digest.Digest]string{}
	for i := range UploadConcurrency + 2 {
		dgst := digest.FromString(fmt.Sprint(i))
		data[dgst] = "data"
		urls[dgst] = "put-" + dgst.String()
	}
	s, conn, logs, exportID := uploadSession(t, data)
	var (
		running     atomic.Int64
		release     = make(chan struct{})
		reached     = make(chan struct{})
		once        sync.Once
		releaseOnce sync.Once
	)
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	s.cfg.put = func(context.Context, string, io.Reader, int64) error {
		if running.Add(1) == UploadConcurrency {
			once.Do(func() { close(reached) })
		}
		<-release
		return nil
	}
	dispatched := time.Now()
	s.dispatch(request(t, 8, protocol.TypeUpload, protocol.Upload{ExportID: exportID, URLs: urls}))
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the uploads never filled the slots")
	}
	filled := time.Since(dispatched)
	time.Sleep(hold)
	releaseAll()
	require.Len(t, decode[protocol.Uploaded](t, conn.next(t)).Done, len(data))
	lines := logs.lines(t, "remote cache blob uploaded")
	require.Len(t, lines, len(data))
	var queued []time.Duration
	for _, line := range lines {
		queued = append(queued, logDuration(t, line, "queued"))
	}
	slices.Sort(queued)
	for _, q := range queued[:UploadConcurrency] {
		require.LessOrEqual(t, q, filled, "a free slot is taken before the slots fill")
	}
	for _, q := range queued[UploadConcurrency:] {
		require.GreaterOrEqual(t, q, hold, "the extra blobs waited for the held slots")
	}
}

// A failed upload is logged at warn level with its error, which never names
// the signed address.
func TestUploadLoggedFailure(t *testing.T) {
	dgst := digest.FromString("one")
	s, conn, logs, exportID := uploadSession(t, blobs{dgst: "one"})
	s.cfg.put = httpPut(&http.Client{Transport: failingTransport{}}, time.Minute)
	address := "https://blobs.example/put?X-Amz-Signature=secret"
	s.dispatch(request(t, 8, protocol.TypeUpload, protocol.Upload{ExportID: exportID, URLs: map[digest.Digest]string{dgst: address}}))
	require.Len(t, decode[protocol.Uploaded](t, conn.next(t)).Failed, 1)
	lines := logs.lines(t, "remote cache blob uploaded")
	require.Len(t, lines, 1)
	line := lines[0]
	require.Equal(t, "WARN", line["level"])
	require.Equal(t, "error", line["outcome"])
	require.Equal(t, dgst.String(), line["digest"])
	require.Equal(t, json.Number("3"), line["bytes"])
	require.Contains(t, line["error"], "connection refused")
	require.NotContains(t, logs.String(), "X-Amz-Signature=secret")
	require.NotContains(t, logs.String(), "blobs.example")
}

// Each export logs one line when its chains are released: what it waited
// for, what it uploaded and how it ended. The waits are a slot, then the
// service's upload after the bundle is sent; the chains are held over both
// and the upload.
func TestExportEndedLogged(t *testing.T) {
	d1, d2 := digest.FromString("one"), digest.FromString("two")
	exportOf := func(data blobs) func(context.Context, protocol.Export, func(context.Context, Export) error) error {
		return func(ctx context.Context, _ protocol.Export, consume func(context.Context, Export) error) error {
			return consume(ctx, Export{Bundle: testBundle(), Blobs: data})
		}
	}
	okPut := func(_ context.Context, _ string, body io.Reader, _ int64) error {
		_, err := io.Copy(io.Discard, body)
		return err
	}
	t.Run("uploaded", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			const wait = 200 * time.Millisecond
			adapter := newFakeAdapter()
			adapter.export = exportOf(blobs{d1: "first", d2: "second"})
			s, conn, logs := loggedSession(t, adapter)
			s.cfg.put = okPut
			requested := time.Now()
			s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12, 13}}))
			exportID := decode[protocol.Exported](t, conn.next(t)).ExportID
			// Once the session waits for the upload, the wait has started.
			synctest.Wait()
			time.Sleep(wait)
			s.dispatch(request(t, 8, protocol.TypeUpload, protocol.Upload{ExportID: exportID, URLs: map[digest.Digest]string{d1: "put-1", d2: "put-2"}}))
			require.Equal(t, protocol.TypeUploaded, conn.next(t).Type)
			answered := time.Since(requested)
			line := logs.waitLines(t, "remote cache export ended", 1)[0]
			require.Equal(t, "INFO", line["level"])
			require.Equal(t, "uploaded", line["outcome"])
			require.Equal(t, exportID, line["export"])
			require.Equal(t, json.Number("2"), line["roots"])
			require.Equal(t, json.Number("2"), line["blobs"])
			require.Equal(t, json.Number("0"), line["failed"])
			require.Equal(t, json.Number(fmt.Sprint(len("first")+len("second"))), line["bytes"])
			require.GreaterOrEqual(t, logDuration(t, line, "slot_wait"), time.Duration(0))
			require.GreaterOrEqual(t, logDuration(t, line, "upload_wait"), wait)
			require.LessOrEqual(t, logDuration(t, line, "upload_wait"), answered)
			require.GreaterOrEqual(t, logDuration(t, line, "held"), logDuration(t, line, "upload_wait")+logDuration(t, line, "upload"))
			require.NotContains(t, line, "error")
		})
	})
	t.Run("no bundle", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.export = func(ctx context.Context, _ protocol.Export, consume func(context.Context, Export) error) error {
			return consume(ctx, Export{Skipped: []protocol.SkippedRoot{{Number: 12, Reason: protocol.SkipGone}}})
		}
		s, conn, logs := loggedSession(t, adapter)
		s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
		require.Empty(t, decode[protocol.Exported](t, conn.next(t)).ExportID)
		line := logs.waitLines(t, "remote cache export ended", 1)[0]
		require.Equal(t, "no_bundle", line["outcome"])
		require.Equal(t, "", line["export"])
		require.Zero(t, logDuration(t, line, "upload_wait"))
		require.Equal(t, json.Number("0"), line["blobs"])
	})
	t.Run("empty", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.export = exportOf(blobs{d1: "first"})
		s, conn, logs := loggedSession(t, adapter)
		s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
		exportID := decode[protocol.Exported](t, conn.next(t)).ExportID
		s.dispatch(request(t, 8, protocol.TypeUpload, protocol.Upload{ExportID: exportID, URLs: map[digest.Digest]string{}}))
		require.Equal(t, protocol.TypeUploaded, conn.next(t).Type)
		line := logs.waitLines(t, "remote cache export ended", 1)[0]
		require.Equal(t, "empty", line["outcome"], "the service asked for no blobs")
		require.Equal(t, json.Number("0"), line["blobs"])
	})
	t.Run("connection dropped", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.export = exportOf(blobs{})
		s, conn, logs := loggedSession(t, adapter)
		s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
		exportID := decode[protocol.Exported](t, conn.next(t)).ExportID
		s.cancel(errors.New("connection dropped"))
		line := logs.waitLines(t, "remote cache export ended", 1)[0]
		require.Equal(t, "INFO", line["level"])
		require.Equal(t, "canceled", line["outcome"])
		require.Equal(t, exportID, line["export"])
		require.Equal(t, "connection dropped", line["error"])
	})
	t.Run("uploaded, then the session ended", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.export = func(ctx context.Context, _ protocol.Export, consume func(context.Context, Export) error) error {
			err := consume(ctx, Export{Bundle: testBundle(), Blobs: blobs{d1: "first"}})
			// The chains are released after the session ends.
			<-ctx.Done()
			return err
		}
		s, conn, logs := loggedSession(t, adapter)
		s.cfg.put = okPut
		s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
		exportID := decode[protocol.Exported](t, conn.next(t)).ExportID
		s.dispatch(request(t, 8, protocol.TypeUpload, protocol.Upload{ExportID: exportID, URLs: map[digest.Digest]string{d1: "put-1"}}))
		require.Equal(t, protocol.TypeUploaded, conn.next(t).Type)
		s.cancel(errors.New("session ended"))
		line := logs.waitLines(t, "remote cache export ended", 1)[0]
		require.Equal(t, "uploaded", line["outcome"], "an export that succeeded is not canceled by a later end")
		require.NotContains(t, line, "error")
	})
	t.Run("failed put", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.export = exportOf(blobs{d1: "first"})
		s, conn, logs := loggedSession(t, adapter)
		s.cfg.put = httpPut(&http.Client{Transport: failingTransport{}}, time.Minute)
		s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
		exportID := decode[protocol.Exported](t, conn.next(t)).ExportID
		s.dispatch(request(t, 8, protocol.TypeUpload, protocol.Upload{ExportID: exportID, URLs: map[digest.Digest]string{d1: "https://blobs.example/put?X-Amz-Signature=secret"}}))
		require.Equal(t, protocol.TypeUploaded, conn.next(t).Type)
		line := logs.waitLines(t, "remote cache export ended", 1)[0]
		require.Equal(t, "uploaded", line["outcome"], "a blob's failure is counted, not the export's")
		require.Equal(t, json.Number("1"), line["blobs"])
		require.Equal(t, json.Number("1"), line["failed"])
		require.Equal(t, json.Number("0"), line["bytes"])
		require.NotContains(t, logs.String(), "X-Amz-Signature=secret")
		require.NotContains(t, logs.String(), "blobs.example")
	})
}

// An export that waits for a slot logs the wait. The exports that find a
// free slot take it before the slots fill.
func TestExportEndedLoggedSlotWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const hold = 200 * time.Millisecond
		entered := make(chan struct{}, 8)
		finish := make(chan struct{})
		adapter := newFakeAdapter()
		adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
			entered <- struct{}{}
			<-finish
			return consume(ctx, Export{Skipped: []protocol.SkippedRoot{{Number: req.Roots[0], Reason: protocol.SkipGone}}})
		}
		s, conn, logs := loggedSession(t, adapter)
		dispatched := time.Now()
		for i := range ExportsInFlight + 1 {
			s.dispatch(request(t, uint64(i+1), protocol.TypeExport, protocol.Export{Roots: []uint64{uint64(i + 1)}}))
		}
		for range ExportsInFlight {
			<-entered
		}
		filled := time.Since(dispatched)
		// Once the fifth handler waits for a slot, its wait has started.
		synctest.Wait()
		time.Sleep(hold)
		close(finish)
		<-entered
		for range ExportsInFlight + 1 {
			require.Equal(t, protocol.TypeExported, conn.next(t).Type)
		}
		lines := logs.waitLines(t, "remote cache export ended", ExportsInFlight+1)
		var waits []time.Duration
		for _, line := range lines {
			waits = append(waits, logDuration(t, line, "slot_wait"))
		}
		slices.Sort(waits)
		for _, w := range waits[:ExportsInFlight] {
			require.LessOrEqual(t, w, filled, "a free slot is taken before the slots fill")
		}
		require.GreaterOrEqual(t, waits[ExportsInFlight], hold, "the fifth export waited for a slot")
	})
}

// An exported reply over the message limit is not sent: the export is
// answered "too large", its chains are released, and the connection lives.
func TestExportTooLarge(t *testing.T) {
	released := make(chan error, 1)
	adapter := newFakeAdapter()
	adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
		err := consume(ctx, Export{Bundle: testBundle(), Blobs: blobs{}})
		released <- err
		return err
	}
	s, conn := testSession(t, adapter)
	s.cfg.maxMessage = 64
	s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
	reply := conn.next(t)
	require.Equal(t, protocol.TypeError, reply.Type)
	require.Equal(t, uint64(7), reply.Re)
	require.Equal(t, protocol.TooLargeMessage, decode[protocol.Error](t, reply).Message)
	require.ErrorIs(t, <-released, errTooLarge)
	conn.none(t)
	require.NoError(t, s.ctx.Err(), "the connection survives")
}

// When the connection drops, an export waiting for its upload releases its
// chains at once.
func TestExportReleasedWhenTheConnectionDrops(t *testing.T) {
	released := make(chan struct{})
	adapter := newFakeAdapter()
	adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
		defer close(released)
		return consume(ctx, Export{Bundle: testBundle(), Blobs: blobs{}})
	}
	s, conn := testSession(t, adapter)
	s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
	conn.next(t)
	s.cancel(errors.New("connection dropped"))
	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("the chains were not released")
	}
}

// At most ExportsInFlight exports run at once; the next waits for a slot.
func TestExportsInFlight(t *testing.T) {
	entered := make(chan struct{}, 8)
	finish := make(chan struct{})
	adapter := newFakeAdapter()
	adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
		entered <- struct{}{}
		<-finish
		return consume(ctx, Export{Skipped: []protocol.SkippedRoot{{Number: req.Roots[0], Reason: protocol.SkipGone}}})
	}
	s, conn := testSession(t, adapter)
	for i := range ExportsInFlight + 1 {
		s.dispatch(request(t, uint64(i+1), protocol.TypeExport, protocol.Export{Roots: []uint64{uint64(i + 1)}}))
	}
	for range ExportsInFlight {
		<-entered
	}
	select {
	case <-entered:
		t.Fatal("an export beyond the limit started")
	case <-time.After(50 * time.Millisecond):
	}
	close(finish)
	<-entered
	for range ExportsInFlight + 1 {
		require.Equal(t, protocol.TypeExported, conn.next(t).Type)
	}
}

// A merge is answered merged, or with an error reply when it fails.
func TestMerge(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.merge = func(_ context.Context, req protocol.Merge) (protocol.Merged, error) {
		if len(req.Bundle.Roots) == 0 {
			return protocol.Merged{}, errors.New("empty bundle")
		}
		return protocol.Merged{Generation: 3, EngineTime: 42, Values: []protocol.MergedValue{{Ordinal: 1, Number: 40}}, Retained: []protocol.RetainedRoot{{Ordinal: 1}}}, nil
	}
	s, conn := testSession(t, adapter)
	s.dispatch(request(t, 7, protocol.TypeMerge, protocol.Merge{Bundle: *testBundle()}))
	reply := conn.next(t)
	require.Equal(t, protocol.TypeMerged, reply.Type)
	require.Equal(t, uint64(7), reply.Re)
	require.Equal(t, uint64(40), decode[protocol.Merged](t, reply).Values[0].Number)

	s.dispatch(request(t, 8, protocol.TypeMerge, protocol.Merge{}))
	reply = conn.next(t)
	require.Equal(t, protocol.TypeError, reply.Type)
	require.Equal(t, uint64(8), reply.Re)
	require.Equal(t, "empty bundle", decode[protocol.Error](t, reply).Message)
}

// An offer is answered offered, or with an error reply when it fails.
func TestOffer(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.offer = func(_ context.Context, req protocol.Offer) (protocol.Offered, error) {
		if len(req.Items) == 0 {
			return protocol.Offered{}, errors.New("no items")
		}
		return protocol.Offered{Items: []protocol.OfferedItem{{Number: req.Items[0].Number, Gone: true}}}, nil
	}
	s, conn := testSession(t, adapter)
	s.dispatch(request(t, 7, protocol.TypeOffer, protocol.Offer{Items: []protocol.OfferItem{{Number: 40}}}))
	reply := conn.next(t)
	require.Equal(t, protocol.TypeOffered, reply.Type)
	require.True(t, decode[protocol.Offered](t, reply).Items[0].Gone)

	s.dispatch(request(t, 8, protocol.TypeOffer, protocol.Offer{}))
	reply = conn.next(t)
	require.Equal(t, protocol.TypeError, reply.Type)
	require.Equal(t, "no items", decode[protocol.Error](t, reply).Message)
}

// A request the engine does not serve is answered with an error reply.
func TestUnexpectedRequest(t *testing.T) {
	s, conn := testSession(t, newFakeAdapter())
	s.dispatch(request(t, 7, protocol.TypeRenew, protocol.Renew{}))
	reply := conn.next(t)
	require.Equal(t, protocol.TypeError, reply.Type)
	require.Equal(t, uint64(7), reply.Re)
}

func renewalRequest(deadline time.Duration) *dagql.RenewalRequest {
	return &dagql.RenewalRequest{
		ID:         dagql.RenewalRequestID{Sequence: 1},
		Chain:      digest.FromString("chain"),
		RenewalKey: "12/fs",
		NeededBlob: digest.FromString("blob"),
		Deadline:   time.Now().Add(deadline),
		Done:       make(chan struct{}),
	}
}

// A read that needs fresh addresses gets the service's, or unavailable when
// the service has none, errs, or does not answer within the read's deadline.
func TestRenewal(t *testing.T) {
	blob := digest.FromString("blob")
	for _, tc := range []struct {
		name     string
		deadline time.Duration
		answer   func(t *testing.T, renew protocol.Envelope) *protocol.Envelope
		want     dagql.RenewalReply
	}{
		{
			name:     "renewed",
			deadline: 10 * time.Second,
			answer: func(t *testing.T, renew protocol.Envelope) *protocol.Envelope {
				env, err := protocol.NewEnvelope(1, renew.ID, protocol.TypeRenewed, protocol.Renewed{Addresses: map[digest.Digest]dagql.BlobAddress{blob: {URL: "get", ExpiresAtUnix: 9}}})
				require.NoError(t, err)
				return &env
			},
			want: dagql.RenewalReply{Addresses: map[digest.Digest]dagql.BlobAddress{blob: {URL: "get", ExpiresAtUnix: 9}}},
		},
		{
			name:     "unavailable",
			deadline: 10 * time.Second,
			answer: func(t *testing.T, renew protocol.Envelope) *protocol.Envelope {
				env, err := protocol.NewEnvelope(1, renew.ID, protocol.TypeRenewed, protocol.Renewed{Unavailable: true})
				require.NoError(t, err)
				return &env
			},
			want: dagql.RenewalReply{Unavailable: true},
		},
		{
			name:     "error",
			deadline: 10 * time.Second,
			answer: func(t *testing.T, renew protocol.Envelope) *protocol.Envelope {
				env, err := protocol.NewEnvelope(1, renew.ID, protocol.TypeError, protocol.Error{Message: "no"})
				require.NoError(t, err)
				return &env
			},
			want: dagql.RenewalReply{Unavailable: true},
		},
		{
			name:     "deadline",
			deadline: 20 * time.Millisecond,
			answer:   func(*testing.T, protocol.Envelope) *protocol.Envelope { return nil },
			want:     dagql.RenewalReply{Unavailable: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := newFakeAdapter()
			s, conn := testSession(t, adapter)
			s.go_(s.renewals)
			req := renewalRequest(tc.deadline)
			adapter.renewals <- req
			renew := conn.next(t)
			require.Equal(t, protocol.TypeRenew, renew.Type)
			require.Equal(t, protocol.Renew{RenewalKey: "12/fs", NeededBlob: req.NeededBlob}, decode[protocol.Renew](t, renew))
			if env := tc.answer(t, renew); env != nil {
				s.dispatch(*env)
			}
			reply := <-adapter.replies
			tc.want.ID, tc.want.Chain = req.ID, req.Chain
			require.Equal(t, tc.want, reply)
		})
	}
}

// queuedWriterConn has one writer at a time, and a writer waiting for it can
// give up on its own context without touching the connection, as
// coder/websocket's writer lock does.
type queuedWriterConn struct {
	*recordingConn
	writeSlot chan struct{}
}

func (c *queuedWriterConn) Write(ctx context.Context, data []byte) error {
	select {
	case c.writeSlot <- struct{}{}:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	defer func() { <-c.writeSlot }()
	return c.recordingConn.Write(ctx, data)
}

// A renewal whose read gives up while another message holds the writer fails
// alone: the read gets "unavailable", and the connection and its other
// requests go on.
func TestRenewalTimingOutBehindTheWriterKeepsTheSession(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.merge = func(context.Context, protocol.Merge) (protocol.Merged, error) {
		return protocol.Merged{Generation: 3}, nil
	}
	conn := &queuedWriterConn{recordingConn: newRecordingConn(), writeSlot: make(chan struct{}, 1)}
	s := newSession(t.Context(), conn, adapter, newSettings(Config{}))
	t.Cleanup(s.close)
	// A large reply has the connection's writer.
	conn.writeSlot <- struct{}{}
	s.go_(s.renewals)
	adapter.renewals <- renewalRequest(20 * time.Millisecond)
	select {
	case reply := <-adapter.replies:
		require.True(t, reply.Unavailable)
	case <-time.After(10 * time.Second):
		t.Fatal("the renewal did not give up")
	}
	<-conn.writeSlot
	require.NoError(t, s.ctx.Err(), "the connection survives the renewal's timeout")
	s.dispatch(request(t, 7, protocol.TypeMerge, protocol.Merge{Bundle: *testBundle()}))
	reply := conn.next(t)
	require.Equal(t, protocol.TypeMerged, reply.Type, "other requests go on")
}

// When the connection drops, the engine's requests in flight fail at once.
func TestRequestsFailWhenTheConnectionDrops(t *testing.T) {
	adapter := newFakeAdapter()
	s, conn := testSession(t, adapter)
	s.go_(s.renewals)
	adapter.renewals <- renewalRequest(time.Minute)
	conn.next(t)
	s.cancel(errors.New("connection dropped"))
	select {
	case reply := <-adapter.replies:
		require.True(t, reply.Unavailable)
	case <-time.After(10 * time.Second):
		t.Fatal("the renewal did not fail")
	}
}

// Run returns promptly when cancelled, while it waits to reconnect and while
// it serves a connection, and closes the connection without waiting.
func TestRunReturnsWhenCancelled(t *testing.T) {
	t.Run("reconnecting", func(t *testing.T) {
		cfg := newSettings(Config{})
		dials := make(chan struct{}, 8)
		cfg.dial = func(context.Context) (wsConn, error) {
			dials <- struct{}{}
			return nil, errors.New("refused")
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- cfg.run(ctx, newFakeAdapter()) }()
		<-dials
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return")
		}
	})
	t.Run("serving", func(t *testing.T) {
		cfg := newSettings(Config{})
		conn := newRecordingConn()
		cfg.dial = func(context.Context) (wsConn, error) { return conn, nil }
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- cfg.run(ctx, newFakeAdapter()) }()
		conn.next(t)
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return")
		}
		require.True(t, conn.closed.Load())
	})
	// Run cancels the adapter's operations in flight and returns once they
	// do, however long the service would have taken.
	serving := func(t *testing.T, adapter *fakeAdapter, req protocol.Envelope, entered <-chan struct{}) {
		t.Helper()
		cfg := newSettings(Config{})
		conn := newServiceConn()
		cfg.dial = func(context.Context) (wsConn, error) { return conn, nil }
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- cfg.run(ctx, adapter) }()
		hello := conn.next(t)
		conn.send(t, protocol.Envelope{ID: 1, Re: hello.ID, Type: protocol.TypeWelcome, Body: json.RawMessage(`{}`)})
		conn.send(t, req)
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("the request never reached the adapter")
		}
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return")
		}
	}
	t.Run("mid-merge", func(t *testing.T) {
		adapter := newFakeAdapter()
		entered, returned := make(chan struct{}), make(chan error, 1)
		adapter.merge = func(ctx context.Context, _ protocol.Merge) (protocol.Merged, error) {
			close(entered)
			<-ctx.Done()
			returned <- context.Cause(ctx)
			return protocol.Merged{}, context.Cause(ctx)
		}
		serving(t, adapter, request(t, 2, protocol.TypeMerge, protocol.Merge{}), entered)
		require.Error(t, <-returned, "the merge's context was cancelled")
	})
	t.Run("mid-export", func(t *testing.T) {
		adapter := newFakeAdapter()
		entered, returned := make(chan struct{}), make(chan error, 1)
		adapter.export = func(ctx context.Context, _ protocol.Export, consume func(context.Context, Export) error) error {
			close(entered)
			// consume sends exported and waits for the service's upload.
			err := consume(ctx, Export{Bundle: testBundle(), Blobs: blobs{}})
			returned <- err
			return err
		}
		serving(t, adapter, request(t, 2, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}), entered)
		require.Error(t, <-returned, "the wait for the upload ended, and the chains were released")
	})
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

// An upload's error names the blob's failure, never the signed address.
func TestHTTPPutHidesTheAddress(t *testing.T) {
	put := httpPut(&http.Client{Transport: failingTransport{}}, time.Minute)
	err := put(t.Context(), "https://blobs.example/put?X-Amz-Signature=secret", bytes.NewReader(nil), 0)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
}

// lookupSession is a session that serves lookups, with its lookup loop
// started.
func lookupSession(t *testing.T, adapter *fakeAdapter, wait time.Duration) (*session, *recordingConn) {
	t.Helper()
	conn := newRecordingConn()
	return lookupSessionOn(t, adapter, conn, wait), conn
}

func lookupSessionOn(t *testing.T, adapter *fakeAdapter, conn wsConn, wait time.Duration) *session {
	t.Helper()
	cfg := newSettings(Config{Lookups: true, LookupWait: wait})
	s := newSession(t.Context(), conn, adapter, cfg)
	t.Cleanup(s.close)
	s.go_(s.lookups)
	require.True(t, within(t, adapter.lookups), "the session starts lookups")
	return s
}

func within[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting")
		panic("unreachable")
	}
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func lookupMisses(n int) []dagql.RemoteMiss {
	misses := make([]dagql.RemoteMiss, n)
	for i := range misses {
		misses[i] = dagql.RemoteMiss{Recipe: digest.FromString(fmt.Sprint("recipe", i)), Field: "withExec"}
	}
	misses[0].Extra = []digest.Digest{digest.FromString("extra")}
	return misses
}

// answerLookup answers the next lookup the engine sends.
func answerLookup(t *testing.T, s *session, conn *recordingConn, answer protocol.LookedUp) protocol.Lookup {
	t.Helper()
	env := conn.next(t)
	require.Equal(t, protocol.TypeLookup, env.Type)
	reply, err := protocol.NewEnvelope(900+env.ID, env.ID, protocol.TypeLookedUp, answer)
	require.NoError(t, err)
	s.dispatch(reply)
	return decode[protocol.Lookup](t, env)
}

// The engine sends its cache's misses as a lookup, by recipe, extra digests
// and field, with its wait bound. Each miss the answer says a merge carries
// with some of its parts' bytes is expected: its evaluation waits for that
// merge, until the bound. No other miss is.
func TestLookup(t *testing.T) {
	adapter := newFakeAdapter()
	s, conn := lookupSession(t, adapter, 10*time.Second)
	misses := lookupMisses(6)
	adapter.misses <- misses
	answered := time.Now()
	sent := answerLookup(t, s, conn, protocol.LookedUp{Misses: []protocol.LookedUpMiss{
		{Status: protocol.LookupMerging, Merge: 40, Parts: protocol.LookupPartsComplete},
		{Status: protocol.LookupInFlight, Merge: 38, Parts: protocol.LookupPartsPartial},
		{Status: protocol.LookupMerging, Merge: 41, Parts: protocol.LookupPartsNone},
		{Status: protocol.LookupDeferred},
		{Status: protocol.LookupNotStored},
		{Status: protocol.LookupUnknown},
	}})
	require.Equal(t, int64(protocol.LookupMisses), adapter.takeLimit.Load())
	require.Equal(t, int64(10000), sent.WaitMillis)
	require.Len(t, sent.Misses, 6)
	require.Equal(t, protocol.LookupMiss{Recipe: misses[0].Recipe, Extra: misses[0].Extra, Field: "withExec"}, sent.Misses[0])
	require.Equal(t, misses[5].Recipe, sent.Misses[5].Recipe)

	first, second := within(t, adapter.expected), within(t, adapter.expected)
	require.Equal(t, misses[0].Recipe, first.recipe)
	require.Equal(t, misses[1].Recipe, second.recipe)
	require.WithinDuration(t, answered.Add(10*time.Second), first.deadline, 5*time.Second)
	require.False(t, closed(first.wait.Done()))
	require.False(t, closed(second.wait.Done()))
	select {
	case got := <-adapter.expected:
		t.Fatalf("unexpected expectation for %s", got.recipe)
	case <-time.After(50 * time.Millisecond):
	}

	// The merge named merging arrives after the answer; its end releases
	// the wait.
	adapter.merge = func(context.Context, protocol.Merge) (protocol.Merged, error) {
		return protocol.Merged{Generation: 3}, nil
	}
	s.dispatch(request(t, 40, protocol.TypeMerge, protocol.Merge{Bundle: *testBundle(), Lookup: 1}))
	require.Equal(t, protocol.TypeMerged, conn.next(t).Type)
	within(t, first.wait.Done())
	require.Equal(t, dagql.RemoteMergeMerged, first.wait.Outcome())
	require.False(t, closed(second.wait.Done()), "only the named merge releases its wait")
}

// A merge the answer names is tracked by its exact ID: one that ended before
// the answer needs no wait, one still running releases the wait when it
// ends, whatever IDs came between.
func TestLookupTracksMergesByID(t *testing.T) {
	adapter := newFakeAdapter()
	s, conn := lookupSession(t, adapter, 10*time.Second)
	release := make(chan struct{})
	adapter.merge = func(_ context.Context, req protocol.Merge) (protocol.Merged, error) {
		if len(req.Bundle.Roots) == 0 {
			<-release
		}
		return protocol.Merged{Generation: 3}, nil
	}
	s.dispatch(request(t, 52, protocol.TypeMerge, protocol.Merge{Bundle: *testBundle()}))
	require.Equal(t, protocol.TypeMerged, conn.next(t).Type)
	s.dispatch(request(t, 50, protocol.TypeMerge, protocol.Merge{}))
	within(t, adapter.mergeIDs)
	within(t, adapter.mergeIDs)

	adapter.misses <- lookupMisses(3)
	answerLookup(t, s, conn, protocol.LookedUp{Misses: []protocol.LookedUpMiss{
		{Status: protocol.LookupInFlight, Merge: 52, Parts: protocol.LookupPartsComplete},
		{Status: protocol.LookupInFlight, Merge: 50, Parts: protocol.LookupPartsComplete},
		{Status: protocol.LookupMerging, Merge: 51, Parts: protocol.LookupPartsComplete},
	}})
	ended, running, coming := within(t, adapter.expected), within(t, adapter.expected), within(t, adapter.expected)
	require.True(t, closed(ended.wait.Done()), "a merge that ended needs no wait")
	require.Equal(t, dagql.RemoteMergeMerged, ended.wait.Outcome())
	require.False(t, closed(running.wait.Done()))
	require.False(t, closed(coming.wait.Done()), "a merge not received yet, with a lower ID than one received, is waited for")
	close(release)
	require.Equal(t, protocol.TypeMerged, conn.next(t).Type)
	within(t, running.wait.Done())
	require.False(t, closed(coming.wait.Done()))
}

// A merge named and never received stops being waited for at the bound, and
// so does every wait when the session ends.
func TestLookupWaitsEnd(t *testing.T) {
	adapter := newFakeAdapter()
	s, conn := lookupSession(t, adapter, 200*time.Millisecond)
	adapter.misses <- lookupMisses(1)
	answerLookup(t, s, conn, protocol.LookedUp{Misses: []protocol.LookedUpMiss{{Status: protocol.LookupMerging, Merge: 70, Parts: protocol.LookupPartsComplete}}})
	deadlined := within(t, adapter.expected)
	within(t, deadlined.wait.Done())
	require.Equal(t, dagql.RemoteMergeDeadline, deadlined.wait.Outcome(), "a merge never received ends the wait at its bound")

	adapter = newFakeAdapter()
	s, conn = lookupSession(t, adapter, time.Minute)
	adapter.misses <- lookupMisses(1)
	answerLookup(t, s, conn, protocol.LookedUp{Misses: []protocol.LookedUpMiss{{Status: protocol.LookupMerging, Merge: 71, Parts: protocol.LookupPartsComplete}}})
	pending := within(t, adapter.expected)
	require.False(t, closed(pending.wait.Done()))
	s.close()
	within(t, pending.wait.Done())
	require.Equal(t, dagql.RemoteMergeDisconnected, pending.wait.Outcome())
	require.False(t, within(t, adapter.lookups), "the session stops lookups when it ends")
}

// With no wait, the engine still sends lookups, and expects nothing.
func TestLookupWithoutWait(t *testing.T) {
	adapter := newFakeAdapter()
	s, conn := lookupSession(t, adapter, 0)
	adapter.misses <- lookupMisses(1)
	sent := answerLookup(t, s, conn, protocol.LookedUp{Misses: []protocol.LookedUpMiss{{Status: protocol.LookupMerging, Merge: 40, Parts: protocol.LookupPartsComplete}}})
	require.Zero(t, sent.WaitMillis)
	select {
	case got := <-adapter.expected:
		t.Fatalf("unexpected expectation for %s", got.recipe)
	case <-time.After(50 * time.Millisecond):
	}
}

// With the default wait, a lookup whose answer names a merge carrying the
// value with its parts' bytes asks the service not to wait and expects
// nothing: no evaluation waits for it.
func TestDefaultLookupWaitWaitsForNothing(t *testing.T) {
	require.Zero(t, DefaultLookupWait)
	merging := protocol.LookedUpMiss{Status: protocol.LookupMerging, Merge: 41, Parts: protocol.LookupPartsComplete}
	adapter := newFakeAdapter()
	s, conn := lookupSession(t, adapter, DefaultLookupWait)
	require.False(t, s.waitsFor(merging))
	adapter.misses <- lookupMisses(1)
	sent := answerLookup(t, s, conn, protocol.LookedUp{Misses: []protocol.LookedUpMiss{merging}})
	require.Zero(t, sent.WaitMillis)
	select {
	case got := <-adapter.expected:
		t.Fatalf("unexpected expectation for %s", got.recipe)
	case <-time.After(50 * time.Millisecond):
	}
}

// A service that refuses a lookup isn't sent another on the connection, and
// an engine with lookups off sends none.
func TestLookupRefused(t *testing.T) {
	adapter := newFakeAdapter()
	s, conn := lookupSession(t, adapter, 10*time.Second)
	adapter.misses <- lookupMisses(1)
	env := conn.next(t)
	require.Equal(t, protocol.TypeLookup, env.Type)
	s.dispatch(protocol.Envelope{ID: 3, Re: env.ID, Type: protocol.TypeError, Body: json.RawMessage(`{"message": "unexpected request \"lookup\""}`)})
	require.False(t, within(t, adapter.lookups), "the session stops lookups")
	adapter.misses <- lookupMisses(1)
	conn.none(t)
	require.NoError(t, s.ctx.Err(), "the connection survives")

	off := newFakeAdapter()
	s, _ = testSession(t, off)
	s.lookups()
	select {
	case <-off.lookups:
		t.Fatal("an engine with lookups off started them")
	default:
	}
}

// A merge ends, for the lookups waiting on it, once the cache has merged it,
// not once its reply is written: another message holding the writer doesn't
// hold the wait.
func TestLookupMergeEndsBeforeItsReply(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.merge = func(context.Context, protocol.Merge) (protocol.Merged, error) {
		return protocol.Merged{Generation: 3}, nil
	}
	conn := &queuedWriterConn{recordingConn: newRecordingConn(), writeSlot: make(chan struct{}, 1)}
	s := lookupSessionOn(t, adapter, conn, time.Minute)
	adapter.misses <- lookupMisses(1)
	answerLookup(t, s, conn.recordingConn, protocol.LookedUp{Misses: []protocol.LookedUpMiss{{Status: protocol.LookupMerging, Merge: 80, Parts: protocol.LookupPartsComplete}}})
	expected := within(t, adapter.expected)
	conn.writeSlot <- struct{}{}
	s.dispatch(request(t, 80, protocol.TypeMerge, protocol.Merge{Bundle: *testBundle(), Lookup: 1}))
	within(t, expected.wait.Done())
	<-conn.writeSlot
	require.Equal(t, protocol.TypeMerged, conn.next(t).Type)
}

// A session's end releases every wait at once, while its handlers still
// drain.
func TestLookupSessionEndReleasesWaitsFirst(t *testing.T) {
	adapter := newFakeAdapter()
	entered, release := make(chan struct{}), make(chan struct{})
	adapter.merge = func(context.Context, protocol.Merge) (protocol.Merged, error) {
		close(entered)
		<-release
		return protocol.Merged{}, errors.New("released")
	}
	s, conn := lookupSession(t, adapter, time.Minute)
	s.dispatch(request(t, 90, protocol.TypeMerge, protocol.Merge{}))
	within(t, entered)
	adapter.misses <- lookupMisses(1)
	answerLookup(t, s, conn, protocol.LookedUp{Misses: []protocol.LookedUpMiss{{Status: protocol.LookupMerging, Merge: 91, Parts: protocol.LookupPartsComplete}}})
	pending := within(t, adapter.expected)
	closedSession := make(chan struct{})
	go func() {
		s.close()
		close(closedSession)
	}()
	within(t, pending.wait.Done())
	select {
	case <-closedSession:
		t.Fatal("the session closed before its merge handler returned")
	default:
	}
	close(release)
	within(t, closedSession)
}

// A wait ends with how its merge ended: merged when it committed, stale when
// the engine refused its held rows and committed nothing, failed when it
// failed. A merge that ended before the answer named it keeps its outcome.
func TestLookupMergeOutcomes(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.merge = func(_ context.Context, req protocol.Merge) (protocol.Merged, error) {
		switch req.Bundle.Version {
		case 1:
			return protocol.Merged{Generation: 3, Stale: []dagql.TransferOrdinal{2}, Values: []protocol.MergedValue{}, Retained: []protocol.RetainedRoot{}}, nil
		case 2:
			return protocol.Merged{}, errors.New("refused")
		}
		return protocol.Merged{Generation: 3}, nil
	}
	s, conn := lookupSession(t, adapter, time.Minute)
	s.dispatch(request(t, 110, protocol.TypeMerge, protocol.Merge{Bundle: dagql.ValueBundle{Version: 1}, Lookup: 1}))
	require.Equal(t, protocol.TypeMerged, conn.next(t).Type)
	adapter.misses <- lookupMisses(4)
	answerLookup(t, s, conn, protocol.LookedUp{Misses: []protocol.LookedUpMiss{
		{Status: protocol.LookupMerging, Merge: 100, Parts: protocol.LookupPartsComplete},
		{Status: protocol.LookupMerging, Merge: 101, Parts: protocol.LookupPartsComplete},
		{Status: protocol.LookupMerging, Merge: 102, Parts: protocol.LookupPartsComplete},
		{Status: protocol.LookupInFlight, Merge: 110, Parts: protocol.LookupPartsComplete},
	}})
	committed, stale, failed, endedStale := within(t, adapter.expected), within(t, adapter.expected), within(t, adapter.expected), within(t, adapter.expected)
	s.dispatch(request(t, 100, protocol.TypeMerge, protocol.Merge{Bundle: *testBundle(), Lookup: 1}))
	s.dispatch(request(t, 101, protocol.TypeMerge, protocol.Merge{Bundle: dagql.ValueBundle{Version: 1}, Lookup: 1}))
	s.dispatch(request(t, 102, protocol.TypeMerge, protocol.Merge{Bundle: dagql.ValueBundle{Version: 2}, Lookup: 1}))
	for _, tc := range []struct {
		got  expectedMerge
		want string
	}{
		{committed, dagql.RemoteMergeMerged},
		{stale, dagql.RemoteMergeStale},
		{failed, dagql.RemoteMergeFailed},
		{endedStale, dagql.RemoteMergeStale},
	} {
		within(t, tc.got.wait.Done())
		require.Equal(t, tc.want, tc.got.wait.Outcome(), "merge for %s", tc.got.recipe)
	}
}

// A merge read from the connection reaches the adapter whole, with its
// envelope's ID, whether the session decodes its body in one pass
// (_EXPERIMENTAL_DAGGER_REMOTE_CACHE_DECODE_ONCE) or not.
func TestMergeReadFromTheConnection(t *testing.T) {
	for _, once := range []bool{true, false} {
		t.Run(fmt.Sprint("once=", once), func(t *testing.T) {
			adapter := newFakeAdapter()
			got := make(chan protocol.Merge, 1)
			adapter.merge = func(_ context.Context, req protocol.Merge) (protocol.Merged, error) {
				got <- req
				return protocol.Merged{Generation: 3}, nil
			}
			conn := newServiceConn()
			cfg := newSettings(Config{})
			require.True(t, cfg.decodeOnce, "on by default")
			cfg.decodeOnce = once
			s := newSession(t.Context(), conn, adapter, cfg)
			t.Cleanup(s.close)
			s.go_(s.read)
			sent := protocol.Merge{Bundle: *testBundle(), Lookup: 5}
			conn.send(t, request(t, 21, protocol.TypeMerge, sent))
			require.Equal(t, sent, within(t, got))
			require.Equal(t, uint64(21), within(t, adapter.mergeIDs))
			reply := conn.next(t)
			require.Equal(t, protocol.TypeMerged, reply.Type)
			require.Equal(t, uint64(21), reply.Re)
		})
	}
}
