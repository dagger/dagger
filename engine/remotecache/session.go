package remotecache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	"github.com/dagger/dagger/engine/wcprof"
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
	// lookupSlots bounds the lookups unanswered, and merges follows the
	// service's merges for the lookups that wait on them.
	lookupSlots chan struct{}
	merges      *mergeTracker

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
		lookupSlots: make(chan struct{}, LookupsInFlight),
		merges:      newMergeTracker(),
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
	s.go_(s.lookups)
	<-s.ctx.Done()
	return true, context.Cause(s.ctx)
}

// close ends the connection without waiting for the service and waits for
// the session's goroutines, whose work is all bound to the session's context.
// Evaluations waiting for the session's merges stop waiting at once.
func (s *session) close() {
	s.cancel(errConnectionClosed)
	_ = s.conn.CloseNow()
	s.merges.close()
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
		HeldRows:      true,
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
		// Recorded in arrival order, before any later answer that names it
		// is read.
		s.merges.start(env.ID)
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
	// The export's profile op: the service's work, not a client's, so it is
	// recorded when the engine records everything. It spans what the export's
	// line times: from the request, before the wait for a slot, to the chains'
	// release.
	arrived := time.Now()
	profCtx, profOp := wcprof.BeginOp(s.ctx, wcprof.OpKindRemoteCache, "remotecache.export", wcprof.OpOpts{Start: arrived})
	select {
	case s.exportSlots <- struct{}{}:
	case <-s.ctx.Done():
		if profOp != nil {
			now := time.Now()
			wcprof.RecordWaitAt(profCtx, "remotecache.exportSlots", wcprof.WaitReasonLock, arrived, now)
			profOp.EndAt(wcprof.OutcomeCanceled, 0, now)
		}
		return
	}
	defer func() { <-s.exportSlots }()
	slotted := time.Now()
	if profOp != nil {
		wcprof.RecordWaitAt(profCtx, "remotecache.exportSlots", wcprof.WaitReasonLock, arrived, slotted)
	}
	ended := exportEnded{roots: len(req.Roots), slotWait: slotted.Sub(arrived)}
	replied := false
	err := s.adapter.Export(profCtx, req, func(ctx context.Context, exp Export) error {
		replied = true
		if exp.Bundle == nil {
			ended.noBundle = true
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
		ended.export = exportID
		profOp.SetIdent(exportID)
		repliedAt := time.Now()
		select {
		case up := <-upload:
			uploadAt := time.Now()
			ended.uploadWait = uploadAt.Sub(repliedAt)
			if profOp != nil {
				wcprof.RecordWaitAt(ctx, "remotecache:service-upload", wcprof.WaitReasonIO, repliedAt, uploadAt)
			}
			uploadCtx, uploadOp := wcprof.BeginOp(ctx, wcprof.OpKindRemoteCache, "remotecache.upload", wcprof.OpOpts{WorkType: wcprof.WorkTypeExternal})
			summary, err := s.upload(uploadCtx, up, exp.Blobs)
			uploadOp.SetBytes(summary.bytes)
			uploadOp.EndErr(err)
			ended.upload, ended.summary = time.Since(uploadAt), summary
			return err
		case <-ctx.Done():
			ended.uploadWait = time.Since(repliedAt)
			if profOp != nil {
				wcprof.RecordWaitAt(ctx, "remotecache:service-upload", wcprof.WaitReasonIO, repliedAt, repliedAt.Add(ended.uploadWait))
			}
			return context.Cause(ctx)
		}
	})
	released := time.Now()
	ended.held = released.Sub(slotted)
	// One classification for the line and the profile.
	level, outcome := s.exportOutcome(ended, err)
	s.logExportEnded(ended, err, level, outcome)
	s.profExportEnded(profCtx, profOp, ended, outcome, released)
	if err != nil && !replied {
		s.replyError(env, err)
	}
}

// exportEnded is what one export took, from its request to the release of
// its chains.
type exportEnded struct {
	// export is the ID of the bundle sent, if one was.
	export string
	roots  int
	// slotWait is the wait for an export slot, uploadWait the wait from
	// sending the bundle to the service's upload, upload the upload, and
	// held the time from the slot to the chains' release.
	slotWait   time.Duration
	uploadWait time.Duration
	upload     time.Duration
	held       time.Duration
	summary    uploadSummary
	noBundle   bool
}

// logExportEnded logs one export's end, at the level and with the outcome
// exportOutcome gave it.
func (s *session) logExportEnded(ended exportEnded, err error, level slog.Level, outcome string) {
	args := []any{
		"export", ended.export,
		"roots", ended.roots,
		"slot_wait", ended.slotWait,
		"upload_wait", ended.uploadWait,
		"upload", ended.upload,
		"blobs", ended.summary.blobs,
		"failed", ended.summary.failed,
		"bytes", ended.summary.bytes,
		"held", ended.held,
		"outcome", outcome,
	}
	if err != nil {
		args = append(args, "error", err.Error())
	}
	s.cfg.Logger.Log(s.ctx, level, "remote cache export ended", args...)
}

// exportOutcome classifies an export's end, for its log line and its
// profile. A failed export is canceled when the session ended; one that
// succeeded is classified by what it sent and what the service asked for,
// even if the session has ended since.
func (s *session) exportOutcome(ended exportEnded, err error) (slog.Level, string) {
	switch {
	case err != nil && context.Cause(s.ctx) != nil:
		return slog.LevelInfo, "canceled"
	case err != nil:
		return slog.LevelWarn, "error"
	case ended.noBundle:
		return slog.LevelInfo, "no_bundle"
	case ended.summary.blobs == 0:
		// The service asked for no blobs: it had them all, or it released
		// the bundle.
		return slog.LevelInfo, "empty"
	}
	return slog.LevelInfo, "uploaded"
}

// profExportEnded ends an export's profile op at end, the chains' release,
// with its outcome, as exportOutcome gave it, and the bytes it uploaded. An
// export that sent no bundle, or uploaded nothing, says so as a fact.
func (s *session) profExportEnded(ctx context.Context, op *wcprof.Op, ended exportEnded, outcome string, end time.Time) {
	if op == nil {
		return
	}
	result := wcprof.OutcomeOK
	switch outcome {
	case "canceled":
		result = wcprof.OutcomeCanceled
	case "error":
		result = wcprof.OutcomeError
	case "no_bundle", "empty":
		wcprof.Fact(ctx, op.ID(), "export."+outcome, 0)
	}
	op.SetBytes(ended.summary.bytes)
	op.EndAt(result, 0, end)
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

// uploadSummary counts an upload's blobs: those asked for, those that
// failed, and the bytes of those uploaded.
type uploadSummary struct {
	blobs, failed int
	bytes         int64
}

// upload PUTs the blobs the service asked for, and answers the upload. At
// most UploadConcurrency PUTs run at a time across all the exports. Each
// blob's line logs how long it queued for a slot, from the request's arrival,
// and how long its PUT took.
func (s *session) upload(ctx context.Context, env protocol.Envelope, blobs BlobSource) (uploadSummary, error) {
	received := time.Now()
	var req protocol.Upload
	if err := env.Decode(&req); err != nil {
		return uploadSummary{}, s.replyError(env, err)
	}
	var (
		mu      sync.Mutex
		reply   = protocol.Uploaded{Done: []digest.Digest{}}
		summary = uploadSummary{blobs: len(req.URLs)}
		wg      sync.WaitGroup
	)
	prof := wcprof.Enabled(ctx)
	for dgst, address := range req.URLs {
		var slotStart time.Time
		if prof {
			slotStart = time.Now()
		}
		select {
		case s.uploadSlots <- struct{}{}:
		case <-ctx.Done():
			if prof {
				wcprof.RecordWaitAt(ctx, "remotecache.uploadSlots", wcprof.WaitReasonLock, slotStart, time.Now())
			}
			wg.Wait()
			return summary, context.Cause(ctx)
		}
		if prof {
			wcprof.RecordWaitAt(ctx, "remotecache.uploadSlots", wcprof.WaitReasonLock, slotStart, time.Now())
		}
		queued := time.Since(received)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-s.uploadSlots }()
			start := time.Now()
			size, err := s.uploadBlob(ctx, blobs, dgst, address)
			// One end and one classification for the line and the profile.
			end := time.Now()
			level, outcome := uploadOutcome(ctx, err)
			s.logUpload(ctx, req.ExportID, dgst, size, queued, end.Sub(start), err, level, outcome)
			if prof {
				profUploadBlob(ctx, dgst, size, start, end, outcome)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				summary.failed++
				reply.Failed = append(reply.Failed, protocol.UploadFailure{Digest: dgst, Message: err.Error()})
			} else {
				summary.bytes += size
				reply.Done = append(reply.Done, dgst)
			}
		}()
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return summary, err
	}
	return summary, s.reply(env, protocol.TypeUploaded, reply)
}

// uploadBlob PUTs one blob, and returns its size once it is read.
func (s *session) uploadBlob(ctx context.Context, blobs BlobSource, dgst digest.Digest, address string) (int64, error) {
	body, size, err := blobs.ReadBlob(ctx, dgst)
	if err != nil {
		return 0, err
	}
	defer body.Close()
	return size, s.cfg.put(ctx, address, body, size)
}

// logUpload logs one blob's upload. It never logs the address, which carries
// a signature; put's errors never include it.
func (s *session) logUpload(ctx context.Context, exportID string, dgst digest.Digest, size int64, queued, duration time.Duration, err error, level slog.Level, outcome string) {
	args := []any{"export", exportID, "digest", dgst.String(), "bytes", size, "queued", queued, "duration", duration, "outcome", outcome}
	if err != nil {
		args = append(args, "error", err.Error())
	}
	s.cfg.Logger.Log(ctx, level, "remote cache blob uploaded", args...)
}

// uploadOutcome classifies a blob's upload, for its log line and its
// profile.
func uploadOutcome(ctx context.Context, err error) (slog.Level, string) {
	switch {
	case context.Cause(ctx) != nil:
		return slog.LevelWarn, "canceled"
	case err != nil:
		return slog.LevelWarn, "error"
	}
	return slog.LevelInfo, "ok"
}

// profUploadBlob records a blob's upload, from start to end, as a profile op
// with the blob's digest and size, and its outcome as uploadOutcome gave it.
func profUploadBlob(ctx context.Context, dgst digest.Digest, size int64, start, end time.Time, outcome string) {
	result := wcprof.OutcomeOK
	switch outcome {
	case "canceled":
		result = wcprof.OutcomeCanceled
	case "error":
		result = wcprof.OutcomeError
	}
	wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.upload.blob", wcprof.OpOpts{Ident: dgst.String(), Bytes: size, WorkType: wcprof.WorkTypeExternal}, start, end, result)
}

// handleMerge merges a bundle and answers. The merge ends, for the lookups
// waiting on it, before the reply is written: its offers are in the cache.
func (s *session) handleMerge(env protocol.Envelope) {
	var req protocol.Merge
	if err := env.Decode(&req); err != nil {
		s.merges.end(env.ID)
		s.replyError(env, err)
		return
	}
	merged, err := s.adapter.Merge(s.ctx, env.ID, req)
	s.merges.end(env.ID)
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
	return s.requestAs(ctx, s.lastID.Add(1), typ, body)
}

// requestAs is request with the ID the caller took for it.
func (s *session) requestAs(ctx context.Context, id uint64, typ protocol.Type, body any) (protocol.Envelope, error) {
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
