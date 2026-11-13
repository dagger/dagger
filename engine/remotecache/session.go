package remotecache

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/pack"
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
	// exportSlots bounds all the exports in flight, outputSlots those that
	// select outputs, and uploadSlots the PUTs of all of them together.
	exportSlots chan struct{}
	outputSlots chan struct{}
	uploadSlots chan struct{}
	// lookupSlots bounds the lookups unanswered.
	lookupSlots chan struct{}
	// restorer merges the files restores name, one part at a time.
	restorer *restorer
	// known are the rows the service merged over this connection, from
	// exports and from its own merges, which exports send as held rows.
	known *dagql.KnownRows
	// opened is the last idle period the service was told of, in the hello
	// or an open, and claimed the sessions whose claims it was told of
	// (protocol.Claims). Only hello and then settles use them.
	opened  uint64
	claimed map[string]bool

	mu sync.Mutex
	// pending are the engine's requests waiting for their replies, by ID.
	pending map[uint64]chan protocol.Envelope
	// exports are the exports whose chains are held, waiting for upload.
	exports map[string]chan protocol.Envelope

	wg sync.WaitGroup
}

func newSession(ctx context.Context, conn wsConn, adapter Adapter, cfg *settings) *session {
	ctx, cancel := context.WithCancelCause(ctx)
	s := &session{
		ctx:         ctx,
		cancel:      cancel,
		conn:        conn,
		adapter:     adapter,
		cfg:         cfg,
		exportSlots: make(chan struct{}, ExportsInFlight+RecordsExportsInFlight),
		outputSlots: make(chan struct{}, ExportsInFlight),
		uploadSlots: make(chan struct{}, UploadConcurrency),
		lookupSlots: make(chan struct{}, LookupsInFlight),
		known:       dagql.NewKnownRows(),
		pending:     map[uint64]chan protocol.Envelope{},
		exports:     map[string]chan protocol.Envelope{},
	}
	s.restorer = newRestorer(s)
	return s
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
	s.go_(s.settles)
	s.go_(s.restorer.run)
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
	settle, _ := s.adapter.SettleState()
	s.opened = settle.Period
	ctx, cancel := context.WithTimeout(s.ctx, helloTimeout)
	defer cancel()
	reply, err := s.request(ctx, protocol.TypeHello, protocol.Hello{
		CacheID:       cacheID,
		Generation:    generation,
		EngineVersion: s.cfg.EngineVersion,
		EngineName:    s.cfg.EngineName,
		Period:        settle.Period,
		Settled:       settle.Settled && !settle.Live,
		Restored:      s.cfg.restored.list(),
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
		env, body, err := s.readEnvelope(data)
		if err != nil {
			s.cfg.Logger.Warn("remote cache: undecodable message", "error", err)
			continue
		}
		s.dispatchBody(env, body)
	}
}

// readEnvelope reads a message, and a merge request's body with it in one
// pass.
func (s *session) readEnvelope(data []byte) (protocol.Envelope, any, error) {
	return protocol.ReadEnvelope(data, func(env protocol.Envelope) any {
		if env.Re == 0 && env.Type == protocol.TypeMerge {
			return &protocol.Merge{}
		}
		return nil
	})
}

// dispatch routes a message: a reply to the engine request it names, an
// upload to the export it continues, any other request to its handler.
func (s *session) dispatch(env protocol.Envelope) {
	s.dispatchBody(env, nil)
}

// dispatchBody is dispatch with the body readEnvelope decoded, if any.
func (s *session) dispatchBody(env protocol.Envelope, body any) {
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
		merge, _ := body.(*protocol.Merge)
		s.go_(func() { s.handleMerge(env, merge) })
	case protocol.TypeOffer:
		s.go_(func() { s.handleOffer(env) })
	case protocol.TypeRestore:
		// Queued here, in the order restores arrive.
		s.restorer.handleRestore(env)
	default:
		s.go_(func() { s.replyError(env, fmt.Errorf("unexpected request %q", env.Type)) })
	}
}

// settles tells the service of each new idle period at once (open), and of
// each session's claim, before the session's settle, and, once no session
// is live, asks it to settle, until the connection ends. A settle is abandoned when its period
// ends or the engine gives up on it.
func (s *session) settles() {
	for s.ctx.Err() == nil {
		state, changed := s.adapter.SettleState()
		claims := s.unclaimed(state.Claims)
		switch {
		case state.Period > s.opened:
			if _, err := s.request(s.ctx, protocol.TypeOpen, protocol.Open{Period: state.Period}); err != nil {
				if s.ctx.Err() != nil {
					return
				}
				s.cfg.Logger.Warn("remote cache: open failed", "period", state.Period, "error", err)
			}
			s.opened = state.Period
			continue
		case len(claims) > 0:
			if _, err := s.request(s.ctx, protocol.TypeClaims, protocol.Claims{Sessions: claims}); err != nil {
				if s.ctx.Err() != nil {
					return
				}
				s.cfg.Logger.Warn("remote cache: claims failed", "sessions", len(claims), "error", err)
			}
			if s.claimed == nil {
				s.claimed = map[string]bool{}
			}
			for _, c := range claims {
				s.claimed[c.ID] = true
			}
			continue
		case state.settling():
			s.settle(state)
			continue
		}
		select {
		case <-changed:
		case <-s.ctx.Done():
			return
		}
	}
}

// unclaimed returns the sessions' claims the service wasn't told of on this
// connection, and forgets those no longer reported.
func (s *session) unclaimed(reported []protocol.SessionClaim) []protocol.SessionClaim {
	var claims []protocol.SessionClaim
	for _, c := range reported {
		if !s.claimed[c.ID] {
			claims = append(claims, c)
		}
	}
	for id := range s.claimed {
		if !slices.ContainsFunc(reported, func(c protocol.SessionClaim) bool { return c.ID == id }) {
			delete(s.claimed, id)
		}
	}
	return claims
}

// settle asks the service to settle state's period, and records its Settled.
// It returns once the service answers, or the period ends, or the engine
// gives up on it.
func (s *session) settle(state SettleState) {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	go func() {
		for {
			current, changed := s.adapter.SettleState()
			if current.Period != state.Period || !current.settling() {
				cancel()
				return
			}
			select {
			case <-changed:
			case <-ctx.Done():
				return
			}
		}
	}()
	reply, err := s.request(ctx, protocol.TypeSettle, protocol.Settle{Period: state.Period, Sessions: state.Sessions})
	switch {
	case err == nil && reply.Type == protocol.TypeSettled:
		s.adapter.Settled(state.Period, state.Sessions)
		return
	case ctx.Err() != nil:
		// Abandoned: the next state says what to do.
		return
	case reply.Type == protocol.TypeError && isStaleSettle(reply):
		// The service has a later period; the engine's open for it is on its way.
	default:
		s.cfg.Logger.Warn("remote cache: settle failed", "period", state.Period, "error", err)
	}
	// Ask again only once something changes.
	_, changed := s.adapter.SettleState()
	select {
	case <-changed:
	case <-ctx.Done():
	}
}

func isStaleSettle(reply protocol.Envelope) bool {
	var e protocol.Error
	return reply.Decode(&e) == nil && e.Message == protocol.StaleSettle
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
		Packs:      req.Packs,
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
	// release. It names the roots asked for, which an export cut off before
	// its reply has no other record of.
	arrived := time.Now()
	profCtx, profOp := wcprof.BeginOp(s.ctx, wcprof.OpKindRemoteCache, "remotecache.export", wcprof.OpOpts{Start: arrived})
	if profOp != nil {
		for _, root := range req.Roots {
			wcprof.Fact(profCtx, profOp.ID(), "export.root", root)
		}
	}
	// An export that selects outputs takes a slot for those, then an export
	// slot. One that selects none copies records only and takes an export
	// slot alone, so that it never waits behind the others' layer builds.
	// These are the protocol's bounds on exports in flight.
	slots, slotsName := []chan struct{}{s.exportSlots}, "remotecache.recordsSlots"
	if len(req.Outputs) > 0 {
		slots, slotsName = []chan struct{}{s.outputSlots, s.exportSlots}, "remotecache.exportSlots"
	}
	for i, slot := range slots {
		select {
		case slot <- struct{}{}:
		case <-s.ctx.Done():
			for _, held := range slots[:i] {
				<-held
			}
			if profOp != nil {
				now := time.Now()
				wcprof.RecordWaitAt(profCtx, slotsName, wcprof.WaitReasonLock, arrived, now)
				profOp.EndAt(wcprof.OutcomeCanceled, 0, now)
			}
			return
		}
	}
	defer func() {
		for _, slot := range slots {
			<-slot
		}
	}()
	slotted := time.Now()
	if profOp != nil {
		wcprof.RecordWaitAt(profCtx, slotsName, wcprof.WaitReasonLock, arrived, slotted)
	}
	ended := exportEnded{roots: len(req.Roots), slotWait: slotted.Sub(arrived)}
	replied := false
	captureCtx, unlabel := wcprof.WithCPUKind(profCtx, wcprof.CPUKindExportCapture)
	defer unlabel()
	err := s.adapter.Export(captureCtx, req, s.known, func(ctx context.Context, exp Export) error {
		// The bundle's reply and its upload send what the capture made.
		ctx, unlabel := wcprof.WithCPUKind(ctx, wcprof.CPUKindExportSend)
		defer unlabel()
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
			var req protocol.Upload
			if err := up.Decode(&req); err != nil {
				return s.replyError(up, err)
			}
			// The service merged the bundle before it asked for the
			// blobs, or it didn't merge it at all: refused as stale, it
			// names the held rows that no longer hold, and only those
			// are forgotten.
			switch {
			case req.Merged:
				s.known.Merged(exp.Rows)
			case len(req.Stale) > 0:
				s.known.Stale(exp.Rows, req.Stale)
			default:
				s.known.NotMerged(exp.Rows)
			}
			uploadCtx, uploadOp := wcprof.BeginOp(ctx, wcprof.OpKindRemoteCache, "remotecache.upload", wcprof.OpOpts{WorkType: wcprof.WorkTypeExternal})
			summary, err := s.upload(uploadCtx, up, req, exp.Blobs)
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
// failed, and the bytes of those uploaded. A pack's members count as blobs,
// and its bytes are the pack's.
type uploadSummary struct {
	blobs, failed int
	bytes         int64
}

// upload PUTs the blobs the service asked for, and answers the upload. At
// most UploadConcurrency PUTs run at a time across all the exports. Each
// blob's line logs how long it queued for a slot, from the request's arrival,
// and how long its PUT took. An upload cut off by ctx answers too, once its
// PUTs in flight end: the blobs that arrived are done, and the others failed.
// It then returns ctx's cause.
func (s *session) upload(ctx context.Context, env protocol.Envelope, req protocol.Upload, blobs BlobSource) (uploadSummary, error) {
	received := time.Now()
	var (
		mu      sync.Mutex
		reply   = protocol.Uploaded{Done: []digest.Digest{}}
		summary = uploadSummary{blobs: len(req.URLs)}
		wg      sync.WaitGroup
	)
	prof := wcprof.Enabled(ctx)
	// cut fails a blob or a pack of members blobs the upload, cut off,
	// never PUT.
	cut := func(dgst digest.Digest, members int) {
		mu.Lock()
		defer mu.Unlock()
		summary.failed += members
		reply.Failed = append(reply.Failed, protocol.UploadFailure{Digest: dgst, Message: context.Cause(ctx).Error()})
	}
	// slot takes an upload slot, recording the wait, or reports false when
	// the upload is cut off first.
	slot := func() bool {
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
			return false
		}
		if prof {
			wcprof.RecordWaitAt(ctx, "remotecache.uploadSlots", wcprof.WaitReasonLock, slotStart, time.Now())
		}
		return true
	}
	for _, up := range req.Packs {
		summary.blobs += len(up.Members)
	}
	for dgst, address := range req.URLs {
		if ctx.Err() != nil || !slot() {
			cut(dgst, 1)
			continue
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
	for _, up := range req.Packs {
		if ctx.Err() != nil || !slot() {
			cut(up.Pack, len(up.Members))
			continue
		}
		queued := time.Since(received)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-s.uploadSlots }()
			start := time.Now()
			size, err := s.uploadPack(ctx, blobs, up)
			end := time.Now()
			level, outcome := uploadOutcome(ctx, err)
			s.logPackUpload(ctx, req.ExportID, up, size, queued, end.Sub(start), err, level, outcome)
			if prof {
				profUploadPack(ctx, up, size, start, end, outcome)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				summary.failed += len(up.Members)
				reply.Failed = append(reply.Failed, protocol.UploadFailure{Digest: up.Pack, Message: err.Error()})
			} else {
				summary.bytes += size
				reply.Done = append(reply.Done, up.Pack)
			}
		}()
	}
	wg.Wait()
	// Whether the session had ended is read when the blobs are done, before
	// the answer: an end that follows the answer doesn't undo the upload.
	cause := context.Cause(ctx)
	if err := s.reply(env, protocol.TypeUploaded, reply); err != nil {
		return summary, err
	}
	return summary, cause
}

// uploadPack PUTs one pack: its header, then each member read from the
// export's chains. It fails with no PUT when the members don't make the pack
// the service named, or when the chains lack a member or hold it at another
// size; and it fails when the PUT didn't take the whole pack.
func (s *session) uploadPack(ctx context.Context, blobs BlobSource, up protocol.UploadPack) (int64, error) {
	id, err := pack.ID(up.Members)
	if err != nil {
		return 0, err
	}
	if id != up.Pack {
		return 0, fmt.Errorf("pack %s: its members make pack %s", up.Pack, id)
	}
	header, err := pack.Header(up.Members)
	if err != nil {
		return 0, err
	}
	size, err := pack.Size(up.Members)
	if err != nil {
		return 0, err
	}
	body := &packUploadBody{parts: []io.Reader{bytes.NewReader(header)}}
	defer body.Close()
	for _, m := range up.Members {
		r, n, err := blobs.ReadBlob(ctx, m.Digest)
		if err != nil {
			return 0, fmt.Errorf("pack member %s: %w", m.Digest, err)
		}
		body.closers = append(body.closers, r)
		if n != m.Size {
			return 0, fmt.Errorf("pack member %s has %d bytes, the pack says %d", m.Digest, n, m.Size)
		}
		body.parts = append(body.parts, newVerifiedBlob(r, m.Digest, n))
	}
	body.reader = io.MultiReader(body.parts...)
	if err := s.cfg.put(ctx, up.URL, body, size); err != nil {
		return 0, err
	}
	read, err := body.result()
	if err != nil {
		return 0, fmt.Errorf("pack %s: %w", up.Pack, err)
	}
	if read != size {
		return 0, fmt.Errorf("pack %s: the upload took %d of %d bytes", up.Pack, read, size)
	}
	return size, nil
}

// packUploadBody is a pack's PUT body: its header, then its members, opened
// before the PUT. A transport may still read a body after the response came,
// and close it from another goroutine, so a close never waits for a read: it
// closes the members, which ends any read.
type packUploadBody struct {
	parts   []io.Reader
	closers []io.Closer
	reader  io.Reader

	read      atomic.Int64
	mu        sync.Mutex
	err       error
	closeOnce sync.Once
}

func (b *packUploadBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read.Add(int64(n))
	if err != nil && !errors.Is(err, io.EOF) {
		b.mu.Lock()
		if b.err == nil {
			b.err = err
		}
		b.mu.Unlock()
	}
	return n, err
}

// result is how many bytes the PUT read, and the error reading them.
func (b *packUploadBody) result() (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.read.Load(), b.err
}

func (b *packUploadBody) Close() error {
	b.closeOnce.Do(func() {
		for _, c := range b.closers {
			_ = c.Close()
		}
	})
	return nil
}

// logPackUpload logs one pack's upload, as logUpload does a blob's.
func (s *session) logPackUpload(ctx context.Context, exportID string, up protocol.UploadPack, size int64, queued, duration time.Duration, err error, level slog.Level, outcome string) {
	args := []any{"export", exportID, "pack", up.Pack.String(), "members", len(up.Members), "bytes", size, "queued", queued, "duration", duration, "outcome", outcome}
	if err != nil {
		args = append(args, "error", err.Error())
	}
	s.cfg.Logger.Log(ctx, level, "remote cache pack uploaded", args...)
}

// uploadBlob PUTs one blob, and returns its size once it is read.
func (s *session) uploadBlob(ctx context.Context, blobs BlobSource, dgst digest.Digest, address string) (int64, error) {
	body, size, err := blobs.ReadBlob(ctx, dgst)
	if err != nil {
		return 0, err
	}
	defer body.Close()
	return size, s.cfg.put(ctx, address, newVerifiedBlob(body, dgst, size), size)
}

// verifiedBlob reads a blob of size bytes for upload, and fails the read
// that would deliver its last bytes when what it read is not the blob its
// digest names: the PUT then fails, and never stores other bytes under the
// digest. The engine writes its blobs without syncing, so a host crash can
// leave a stored blob short or wrong.
type verifiedBlob struct {
	r        io.Reader
	dgst     digest.Digest
	verifier digest.Verifier
	left     int64
	checked  bool
}

func newVerifiedBlob(r io.Reader, dgst digest.Digest, size int64) *verifiedBlob {
	return &verifiedBlob{r: io.LimitReader(r, size), dgst: dgst, verifier: dgst.Verifier(), left: size}
}

func (b *verifiedBlob) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	_, _ = b.verifier.Write(p[:n])
	b.left -= int64(n)
	if b.left == 0 && !b.checked {
		b.checked = true
		if !b.verifier.Verified() {
			return 0, fmt.Errorf("blob %s: the stored bytes don't match its digest", b.dgst)
		}
	}
	if errors.Is(err, io.EOF) && b.left > 0 {
		return n, fmt.Errorf("blob %s: %w", b.dgst, io.ErrUnexpectedEOF)
	}
	return n, err
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

// profUploadPack records one pack's PUT, as profUploadBlob does a blob's.
func profUploadPack(ctx context.Context, up protocol.UploadPack, size int64, start, end time.Time, outcome string) {
	result := wcprof.OutcomeOK
	switch outcome {
	case "canceled":
		result = wcprof.OutcomeCanceled
	case "error":
		result = wcprof.OutcomeError
	}
	wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.upload.pack", wcprof.OpOpts{Ident: up.Pack.String(), Bytes: size, WorkType: wcprof.WorkTypeExternal}, start, end, result)
}

// handleMerge merges a bundle and answers.
func (s *session) handleMerge(env protocol.Envelope, req *protocol.Merge) {
	if req == nil {
		req = &protocol.Merge{}
		if err := env.Decode(req); err != nil {
			s.replyError(env, err)
			return
		}
	}
	ctx, unlabel := wcprof.WithCPUKind(s.ctx, wcprof.CPUKindMerge)
	defer unlabel()
	merged, err := s.adapter.Merge(ctx, env.ID, *req, s.known)
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
