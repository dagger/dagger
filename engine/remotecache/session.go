package remotecache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
)

var (
	errConnectionClosed = errors.New("remote cache connection closed")
	errTooLarge         = errors.New(protocol.TooLargeMessage)
)

// session is one connection to the service. When it ends, every request in
// flight on the engine's side fails, and the chains held for exports are
// released at once; the next connection starts afresh with hello.
type session struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	conn    wsConn
	adapter Adapter
	cfg     *settings

	// lastID numbers the engine's messages; the first is 1, so a reply's
	// re of 0 never names one.
	lastID atomic.Uint64
	// exportSlots bounds the exports in flight, and uploadSlots the PUTs
	// of all of them together.
	exportSlots chan struct{}
	uploadSlots chan struct{}

	mu sync.Mutex
	// pending are the engine's requests waiting for their replies, by ID.
	pending map[uint64]chan protocol.Envelope
	// exports are the exports whose chains are held, waiting for upload.
	exports map[string]chan protocol.Envelope

	wg sync.WaitGroup
}

func newSession(ctx context.Context, conn wsConn, adapter Adapter, cfg *settings) *session {
	ctx, cancel := context.WithCancelCause(ctx)
	return &session{
		ctx:         ctx,
		cancel:      cancel,
		conn:        conn,
		adapter:     adapter,
		cfg:         cfg,
		exportSlots: make(chan struct{}, ExportsInFlight),
		uploadSlots: make(chan struct{}, UploadConcurrency),
		pending:     map[uint64]chan protocol.Envelope{},
		exports:     map[string]chan protocol.Envelope{},
	}
}

// serve reads the connection, says hello, and serves requests until the
// connection or ctx ends. It reports whether the service welcomed the engine.
func (s *session) serve() (bool, error) {
	s.go_(s.read)
	if err := s.hello(); err != nil {
		s.cancel(err)
		return false, err
	}
	s.go_(s.ping)
	s.go_(s.renewals)
	<-s.ctx.Done()
	return true, context.Cause(s.ctx)
}

// close ends the connection without waiting for the service and waits for
// the session's goroutines, whose work is all bound to the session's context.
func (s *session) close() {
	s.cancel(errConnectionClosed)
	_ = s.conn.CloseNow()
	s.wg.Wait()
}

func (s *session) go_(f func()) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		f()
	}()
}

func (s *session) hello() error {
	cacheID, generation := s.adapter.CacheIdentity()
	ctx, cancel := context.WithTimeout(s.ctx, helloTimeout)
	defer cancel()
	reply, err := s.request(ctx, protocol.TypeHello, protocol.Hello{
		CacheID:       cacheID,
		Generation:    generation,
		EngineVersion: s.cfg.EngineVersion,
		EngineName:    s.cfg.EngineName,
	})
	if err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if reply.Type != protocol.TypeWelcome {
		return fmt.Errorf("hello: unexpected %s", reply.Type)
	}
	return nil
}

func (s *session) read() {
	for {
		data, err := s.conn.Read(s.ctx)
		if err != nil {
			s.cancel(fmt.Errorf("read: %w", err))
			return
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			s.cfg.Logger.Warn("remote cache: undecodable message", "error", err)
			continue
		}
		s.dispatch(env)
	}
}

// dispatch routes a message: a reply to the engine request it names, an
// upload to the export it continues, any other request to its handler.
func (s *session) dispatch(env protocol.Envelope) {
	if env.Re != 0 {
		s.mu.Lock()
		waiter := s.pending[env.Re]
		delete(s.pending, env.Re)
		s.mu.Unlock()
		if waiter != nil {
			waiter <- env
		}
		return
	}
	switch env.Type {
	case protocol.TypeExport:
		s.go_(func() { s.handleExport(env) })
	case protocol.TypeUpload:
		s.handleUpload(env)
	case protocol.TypeMerge:
		s.go_(func() { s.handleMerge(env) })
	case protocol.TypeOffer:
		s.go_(func() { s.handleOffer(env) })
	default:
		s.go_(func() { s.replyError(env, fmt.Errorf("unexpected request %q", env.Type)) })
	}
}

// ping checks the connection every PingInterval, and ends the session on a
// ping without a pong within PingTimeout.
func (s *session) ping() {
	t := time.NewTicker(protocol.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(s.ctx, protocol.PingTimeout)
		err := s.conn.Ping(ctx)
		cancel()
		if err != nil {
			s.cancel(fmt.Errorf("ping: %w", err))
			return
		}
	}
}

// renewals takes the reads that need fresh download addresses and asks the
// service, each within the read's own deadline.
func (s *session) renewals() {
	for {
		req, err := s.adapter.TakeRenewalRequest(s.ctx)
		if err != nil {
			return
		}
		s.go_(func() { s.renew(req) })
	}
}

func (s *session) renew(req *dagql.RenewalRequest) {
	ctx, cancel := context.WithDeadline(s.ctx, req.Deadline)
	defer cancel()
	// The read may give up before its deadline.
	go func() {
		select {
		case <-req.Done:
			cancel()
		case <-ctx.Done():
		}
	}()
	reply := dagql.RenewalReply{ID: req.ID, Chain: req.Chain, Unavailable: true}
	env, err := s.request(ctx, protocol.TypeRenew, protocol.Renew{
		RenewalKey: req.RenewalKey,
		Layers:     req.Layers,
		NeededBlob: req.NeededBlob,
	})
	if err == nil && env.Type == protocol.TypeRenewed {
		var renewed protocol.Renewed
		if err := env.Decode(&renewed); err == nil && !renewed.Unavailable {
			reply.Unavailable = false
			reply.Addresses = renewed.Addresses
		}
	}
	s.adapter.ReplyRenewal(reply)
}

func (s *session) handleExport(env protocol.Envelope) {
	var req protocol.Export
	if err := env.Decode(&req); err != nil {
		s.replyError(env, err)
		return
	}
	select {
	case s.exportSlots <- struct{}{}:
	case <-s.ctx.Done():
		return
	}
	defer func() { <-s.exportSlots }()
	replied := false
	err := s.adapter.Export(s.ctx, req, func(ctx context.Context, exp Export) error {
		replied = true
		if exp.Bundle == nil {
			return s.reply(env, protocol.TypeExported, protocol.Exported{Skipped: exp.Skipped})
		}
		exportID, err := newExportID()
		if err != nil {
			return s.replyError(env, err)
		}
		upload := make(chan protocol.Envelope, 1)
		s.mu.Lock()
		s.exports[exportID] = upload
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.exports, exportID)
			s.mu.Unlock()
		}()
		if err := s.reply(env, protocol.TypeExported, protocol.Exported{ExportID: exportID, Bundle: exp.Bundle, Skipped: exp.Skipped}); err != nil {
			return err
		}
		select {
		case up := <-upload:
			return s.upload(ctx, up, exp.Blobs)
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	})
	if err != nil && !replied {
		s.replyError(env, err)
	}
}

// handleUpload hands an upload to the export whose chains are held for it.
func (s *session) handleUpload(env protocol.Envelope) {
	var req protocol.Upload
	if err := env.Decode(&req); err != nil {
		s.go_(func() { s.replyError(env, err) })
		return
	}
	s.mu.Lock()
	upload := s.exports[req.ExportID]
	delete(s.exports, req.ExportID)
	if upload != nil {
		upload <- env
	}
	s.mu.Unlock()
	if upload == nil {
		s.go_(func() { s.replyError(env, fmt.Errorf("no export %q", req.ExportID)) })
	}
}

// upload PUTs the blobs the service asked for, and answers the upload. At
// most UploadConcurrency PUTs run at a time across all the exports.
func (s *session) upload(ctx context.Context, env protocol.Envelope, blobs BlobSource) error {
	var req protocol.Upload
	if err := env.Decode(&req); err != nil {
		return s.replyError(env, err)
	}
	var (
		mu    sync.Mutex
		reply = protocol.Uploaded{Done: []digest.Digest{}}
		wg    sync.WaitGroup
	)
	for dgst, address := range req.URLs {
		select {
		case s.uploadSlots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return context.Cause(ctx)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-s.uploadSlots }()
			err := s.uploadBlob(ctx, blobs, dgst, address)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				reply.Failed = append(reply.Failed, protocol.UploadFailure{Digest: dgst, Message: err.Error()})
			} else {
				reply.Done = append(reply.Done, dgst)
			}
		}()
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return s.reply(env, protocol.TypeUploaded, reply)
}

func (s *session) uploadBlob(ctx context.Context, blobs BlobSource, dgst digest.Digest, address string) error {
	body, size, err := blobs.ReadBlob(ctx, dgst)
	if err != nil {
		return err
	}
	defer body.Close()
	return s.cfg.put(ctx, address, body, size)
}

func (s *session) handleMerge(env protocol.Envelope) {
	var req protocol.Merge
	if err := env.Decode(&req); err != nil {
		s.replyError(env, err)
		return
	}
	merged, err := s.adapter.Merge(s.ctx, req)
	if err != nil {
		s.replyError(env, err)
		return
	}
	_ = s.reply(env, protocol.TypeMerged, merged)
}

func (s *session) handleOffer(env protocol.Envelope) {
	var req protocol.Offer
	if err := env.Decode(&req); err != nil {
		s.replyError(env, err)
		return
	}
	offered, err := s.adapter.OfferParts(s.ctx, req)
	if err != nil {
		s.replyError(env, err)
		return
	}
	_ = s.reply(env, protocol.TypeOffered, offered)
}

// request sends an engine request and waits for its reply, which may be an
// error reply. It fails when ctx or the session ends first.
func (s *session) request(ctx context.Context, typ protocol.Type, body any) (protocol.Envelope, error) {
	id := s.lastID.Add(1)
	env, err := protocol.NewEnvelope(id, 0, typ, body)
	if err != nil {
		return protocol.Envelope{}, err
	}
	waiter := make(chan protocol.Envelope, 1)
	s.mu.Lock()
	s.pending[id] = waiter
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()
	if err := s.write(ctx, env); err != nil {
		return protocol.Envelope{}, err
	}
	select {
	case reply := <-waiter:
		if reply.Type == protocol.TypeError {
			var e protocol.Error
			_ = reply.Decode(&e)
			return reply, fmt.Errorf("%s: %s", typ, e.Message)
		}
		return reply, nil
	case <-ctx.Done():
		return protocol.Envelope{}, context.Cause(ctx)
	}
}

// reply answers a request. A reply over the message limit is not sent: the
// request is answered with an error reply "too large" instead, which the
// connection survives, and the reply is reported as errTooLarge.
func (s *session) reply(to protocol.Envelope, typ protocol.Type, body any) error {
	env, err := protocol.NewEnvelope(s.lastID.Add(1), to.ID, typ, body)
	if err != nil {
		return s.replyError(to, err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		return s.replyError(to, err)
	}
	if len(data) > s.cfg.maxMessage {
		_ = s.replyError(to, errTooLarge)
		return errTooLarge
	}
	return s.writeData(s.ctx, data)
}

// replyError answers a request with an error reply, and returns err.
func (s *session) replyError(to protocol.Envelope, err error) error {
	env, encErr := protocol.NewEnvelope(s.lastID.Add(1), to.ID, protocol.TypeError, protocol.Error{Message: err.Error()})
	if encErr == nil {
		_ = s.write(s.ctx, env)
	}
	return err
}

func (s *session) write(ctx context.Context, env protocol.Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return s.writeData(ctx, data)
}

// writeData writes one message. A write that fails while its own context is
// still live ends the session. One whose context has ended belongs to that
// request alone, such as a renewal whose read gave up while another message
// held the writer: the connection is left to the reader, which sees it fail
// if the library closed it mid-frame.
func (s *session) writeData(ctx context.Context, data []byte) error {
	if err := s.conn.Write(ctx, data); err != nil {
		if ctx.Err() == nil {
			s.cancel(fmt.Errorf("write: %w", err))
		}
		return err
	}
	return nil
}

func newExportID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
