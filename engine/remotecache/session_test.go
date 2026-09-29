package remotecache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		renewals: make(chan *dagql.RenewalRequest, 8),
		replies:  make(chan dagql.RenewalReply, 8),
	}
}

func (a *fakeAdapter) CacheIdentity() (string, uint64) { return "cache-1", 3 }

func (a *fakeAdapter) Export(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
	return a.export(ctx, req, consume)
}

func (a *fakeAdapter) Merge(ctx context.Context, req protocol.Merge) (protocol.Merged, error) {
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

// The engine's first message says hello with its identity, numbered 1; the
// session is served once the service welcomes it.
func TestHello(t *testing.T) {
	s, conn := testSession(t, newFakeAdapter())
	served := make(chan bool, 1)
	go func() {
		welcomed, _ := s.serve()
		served <- welcomed
	}()
	hello := conn.next(t)
	require.Equal(t, protocol.TypeHello, hello.Type)
	require.Equal(t, uint64(1), hello.ID, "IDs start at 1")
	require.Equal(t, protocol.Hello{CacheID: "cache-1", Generation: 3, EngineVersion: "v0.20.0", EngineName: "dagger-engine"}, decode[protocol.Hello](t, hello))
	s.dispatch(protocol.Envelope{ID: 1, Re: hello.ID, Type: protocol.TypeWelcome, Body: json.RawMessage(`{}`)})
	s.cancel(errors.New("done"))
	require.True(t, <-served)
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
	put := httpPut(&http.Client{Transport: failingTransport{}})
	err := put(t.Context(), "https://blobs.example/put?X-Amz-Signature=secret", bytes.NewReader(nil), 0)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
}
