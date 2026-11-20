package remotecache

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	"github.com/dagger/dagger/engine/wcprof"
)

// A restore merges the files the service published of a check's set
// (protocol.Restore): the head, the check's own results and what its runs
// used before them, and the whole set. Each file is downloaded as soon as its
// restore arrives, checked against its digest, and its parts are decoded and
// merged one at a time through the path a merge takes (Adapter.RestorePart),
// each dropped before the next is decoded. Heads merge on a lane of their
// own, in arrival order, so a head never waits for a whole, even another
// check's that started first; each whole merges on a lane of its own, so a
// whole never waits for another check's either. Nothing in a restore fails it,
// nor anything else: a file that can't be fetched, read or merged leaves the
// engine with what merged of it, and the reply says where it stopped.

const (
	// restoreIdleTimeout bounds the time a file's download goes without a
	// byte, as a part's content read's is bounded (dagql's
	// partContentIdleTimeout). It is not a bound on the whole download.
	restoreIdleTimeout = 30 * time.Second
)

// errRestoreConnectionEnded stops a restore whose connection ended.
var errRestoreConnectionEnded = errors.New("the connection ended")

// restoredFiles are the files this engine process merged whole, by digest,
// across its connections: each hello lists them (protocol.Hello.Restored),
// and a restore of one merges nothing and is answered already, with where
// the file's merge answered its records landed. The service offers such a
// file again only when it never read that answer, as when the connection
// dropped first: without it, it can't hold the file's records by file.
type restoredFiles struct {
	mu   sync.Mutex
	done []digest.Digest
	// answers are each file's entries and complete parts as its merge
	// answered them (protocol.RestoredFile.Entries), kept as that answer
	// carries them on the wire: two to five bytes a record.
	answers map[digest.Digest][]byte
}

// answer returns the answer to a restore of a file this process merged
// whole: already, with the entries and complete parts its merge answered.
// It reports false for a file it didn't merge whole.
func (r *restoredFiles) answer(dgst digest.Digest) (protocol.RestoredFile, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.done, dgst) {
		return protocol.RestoredFile{}, false
	}
	answer := protocol.RestoredFile{Digest: dgst, Complete: true, Already: true}
	var noted protocol.RestoredFile
	if err := json.Unmarshal(r.answers[dgst], &noted); err == nil {
		answer.Entries, answer.PartsTable, answer.CompleteParts = noted.Entries, noted.PartsTable, noted.CompleteParts
	}
	return answer, true
}

// add records a file this process merged whole, with its merge's answer,
// and returns how many bytes it keeps of that answer.
func (r *restoredFiles) add(answer protocol.RestoredFile) int {
	kept, err := json.Marshal(protocol.RestoredFile{Entries: answer.Entries, PartsTable: answer.PartsTable, CompleteParts: answer.CompleteParts})
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.done, answer.Digest) {
		r.done = append(r.done, answer.Digest)
	}
	if err != nil {
		// Answered again without entries: the service covers the file by
		// placement, as it would without them.
		return 0
	}
	if r.answers == nil {
		r.answers = map[digest.Digest][]byte{}
	}
	r.answers[answer.Digest] = kept
	return len(kept)
}

func (r *restoredFiles) list() []digest.Digest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.done)
}

// RestoreReport is what a restore did with one file, for the engine's stop
// event: the file, when its restore arrived and its download began and
// ended, each part that merged, and how it ended. PeakBefore and PeakAfter
// are the engine process's peak resident memory, in bytes, before its first
// part was decoded and after its last merged, or 0 when unknown: a peak that
// grew between them is the restore's.
type RestoreReport struct {
	Check   string
	Kind    string
	Digest  digest.Digest
	Bytes   int64
	Arrived time.Time
	// FetchStarted and Fetched are zero for a file not fetched.
	FetchStarted time.Time
	Fetched      time.Time
	Parts        []RestoredPartReport
	Complete     bool
	Already      bool
	Error        string
	PeakBefore   int64
	PeakAfter    int64
	// Decode, Prepare and Commit are the wall time its parts took to decode,
	// to prepare their merges and to commit them, in all.
	Decode, Prepare, Commit time.Duration
	// Window is what the engine did from the file's arrival to its end, for
	// a file that was downloaded and ended; nil for one answered at once or
	// left when its connection ended.
	Window *RestoreWindow
}

// RestoredPartReport is one merged part: its records, when it committed on
// the engine's clock, and the misses it would have served: lookups that
// missed before it committed and now find an entry its merge landed on
// (Adapter.LateMisses), each counted once on a connection, by the first
// part that lands it. It is an upper bound: the lookup is redone without
// the missed session's own checks (dagql.Cache.LateRemoteMisses). WorstLate and LeastLate are the longest and the
// shortest of those misses' times from the miss to the commit: the earliest
// miss's and the latest's.
//
// LockWait is how long its commit waited for the cache's lock, and
// CommitHold how long the commit held it (dagql.MergeReply). Decode,
// Prepare and Commit are the part's own decode, the preparation of its
// merge and the merge's commit, and PriorityWait how long its merge waited
// behind the merges that answer lookups before it began (Adapter.RestorePart).
// PrepareLockWait is how long the preparation waited for the cache's lock,
// and PrepareLockWaits that time by the kind of writer that let each wait in
// (dagql.MergeReply).
type RestoredPartReport struct {
	Records          int
	Committed        time.Time
	Late             int
	WorstLate        time.Duration
	LeastLate        time.Duration
	LockWait         time.Duration
	CommitHold       time.Duration
	Decode           time.Duration
	Prepare          time.Duration
	Commit           time.Duration
	PriorityWait     time.Duration
	PrepareLockWait  time.Duration
	PrepareLockWaits map[string]time.Duration
}

// restoreFile is one file of a restore: queued, being fetched, being merged,
// or done.
type restoreFile struct {
	req   *restoreRequest
	index int
	head  bool
	check string
	file  protocol.SetFile

	// fetched closes once data or err is set; nothing else writes them.
	fetched      chan struct{}
	data         []byte
	err          error
	fetchStarted time.Time
	fetchEnded   time.Time
	timing       fetchTiming
	// windowStart is the engine's counters as the file arrived, read beside
	// its download (RestoreReport.Window); set before fetched closes.
	windowStart restoreReading
	// landed ends the cache's mark of a whole landing (Adapter.Landing),
	// from its arrival to its end; nil for a head.
	landed func()

	// started is set, under the restorer's lock, once a lane of its own
	// takes a whole.
	started bool

	// The rest is its lane's alone. unretained are each part's roots to
	// merge with no retention edge, and releases the holds each committed
	// part's merge took on what it landed, kept until the file ends.
	// entries are the entries the file's records landed on, in the file's
	// order (protocol.RestoredFile.Entries), and placed the records already
	// among them, by Cloud number.
	opened     bool
	parts      [][]byte
	unretained [][]dagql.TransferOrdinal
	next       int
	result     protocol.RestoredFile
	report     RestoreReport
	releases   []func(context.Context) error
	entries    protocol.EntryNumbers
	complete   [][]dagql.PersistedPartAddress
	placed     map[uint64]struct{}
	// kept is how many bytes of its answer the process keeps, for a later
	// connection's restore of it (restoredFiles).
	kept int
}

// restoreRequest is one Restore, answered once its files are done. left
// counts the files not done, under the restorer's lock.
type restoreRequest struct {
	env   protocol.Envelope
	files []*restoreFile
	left  int
}

// restorer is a connection's restores: its files waiting or in progress,
// heads and wholes.
type restorer struct {
	s *session

	mu sync.Mutex
	// heads and wholes are the heads and the wholes not done, each in
	// arrival order: the head lane's queue, and the wholes that have a lane
	// of their own (started) or wait for one.
	heads, wholes []*restoreFile
	// headQueued and wholeQueued hold a signal when a head or a whole was
	// queued.
	headQueued, wholeQueued chan struct{}
}

func newRestorer(s *session) *restorer {
	return &restorer{s: s, headQueued: make(chan struct{}, 1), wholeQueued: make(chan struct{}, 1)}
}

// handleRestore queues a restore's files, and starts fetching each at once.
// A file this engine process merged whole is answered at once.
func (r *restorer) handleRestore(env protocol.Envelope) {
	var restore protocol.Restore
	if err := env.Decode(&restore); err != nil {
		r.s.go_(func() { r.s.replyError(env, err) })
		return
	}
	arrived := time.Now()
	req := &restoreRequest{env: env}
	var queue []*restoreFile
	add := func(set protocol.RestoreSet, file *protocol.SetFile, head bool) {
		if file == nil {
			return
		}
		f := &restoreFile{req: req, index: len(req.files), head: head, check: set.Check, file: *file, fetched: make(chan struct{})}
		f.result = protocol.RestoredFile{Digest: file.Digest}
		f.report = RestoreReport{Check: set.Check, Kind: fileKind(head), Digest: file.Digest, Bytes: file.Size, Arrived: arrived}
		req.files = append(req.files, f)
		if answer, ok := r.s.cfg.restored.answer(file.Digest); ok {
			f.result = answer
			f.report.Complete, f.report.Already = true, true
			r.s.adapter.Restored(f.report)
			return
		}
		queue = append(queue, f)
	}
	for _, set := range restore.Sets {
		add(set, set.Head, true)
	}
	for _, set := range restore.Sets {
		add(set, set.Whole, false)
	}
	req.left = len(queue)
	if req.left == 0 {
		r.s.go_(func() { r.reply(req) })
		return
	}
	r.mu.Lock()
	for _, f := range queue {
		if f.head {
			r.heads = append(r.heads, f)
		} else {
			f.landed = r.s.adapter.Landing()
			r.wholes = append(r.wholes, f)
		}
	}
	r.mu.Unlock()
	for _, f := range queue {
		r.s.go_(func() { r.fetch(f) })
		queued := r.wholeQueued
		if f.head {
			queued = r.headQueued
		}
		select {
		case queued <- struct{}{}:
		default:
		}
	}
}

func fileKind(head bool) string {
	if head {
		return "head"
	}
	return "whole"
}

// source names the file, for the log of a record of it whose payload fails
// at its first use: its check, its kind and its digest.
func (f *restoreFile) source() string {
	return f.check + " " + fileKind(f.head) + " " + f.file.Digest.String()
}

// run merges the queued files until the connection ends: heads on a lane of
// their own, so a head never waits for a whole, and each whole on a lane of
// its own, so a whole never waits for another check's.
func (r *restorer) run() {
	var lanes sync.WaitGroup
	lanes.Go(r.headLane)
	r.wholeLanes(&lanes)
	lanes.Wait()
	r.abandon()
}

// headLane merges the heads' parts one at a time, in arrival order, each
// once it is downloaded, until the connection ends.
func (r *restorer) headLane() {
	ctx, unlabel := wcprof.WithCPUKind(r.s.ctx, wcprof.CPUKindMerge)
	defer unlabel()
	for ctx.Err() == nil {
		f := r.firstHead()
		if f == nil {
			select {
			case <-r.headQueued:
				continue
			case <-ctx.Done():
				return
			}
		}
		r.merge(ctx, f, false)
	}
}

// firstHead returns the first head not done.
func (r *restorer) firstHead() *restoreFile {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.heads) > 0 {
		return r.heads[0]
	}
	return nil
}

// wholeLanes starts a lane for each whole as it is queued, until the
// connection ends, each lane on lanes.
func (r *restorer) wholeLanes(lanes *sync.WaitGroup) {
	for {
		r.mu.Lock()
		var start []*restoreFile
		for _, f := range r.wholes {
			if !f.started {
				f.started = true
				start = append(start, f)
			}
		}
		r.mu.Unlock()
		for _, f := range start {
			lanes.Go(func() {
				ctx, unlabel := wcprof.WithCPUKind(r.s.ctx, wcprof.CPUKindMerge)
				defer unlabel()
				r.merge(ctx, f, true)
			})
		}
		select {
		case <-r.wholeQueued:
		case <-r.s.ctx.Done():
			return
		}
	}
}

// merge merges a file's parts once it is downloaded: its next part, or all
// of them, one at a time, until it is done or the connection ends.
func (r *restorer) merge(ctx context.Context, f *restoreFile, all bool) {
	select {
	case <-f.fetched:
	case <-ctx.Done():
		return
	}
	for ctx.Err() == nil {
		if r.step(ctx, f) || !all {
			return
		}
	}
}

// step opens a fetched file, or merges its next part, and finishes it once
// it has no part left or something stopped it. It reports whether the file
// is done.
func (r *restorer) step(ctx context.Context, f *restoreFile) bool {
	if !f.opened {
		if err := r.open(f); err != nil {
			r.finish(f, err)
			return true
		}
		if len(f.parts) == 0 {
			r.finish(f, nil)
			return true
		}
	}
	if err := r.mergePart(ctx, f); err != nil {
		r.finish(f, err)
		return true
	}
	if f.next == len(f.parts) {
		r.finish(f, nil)
		return true
	}
	return false
}

// open reads a fetched file's header and splits its parts, and refuses a
// file of another bundle version before decoding any part.
func (r *restorer) open(f *restoreFile) error {
	f.opened = true
	f.report.FetchStarted, f.report.Fetched = f.fetchStarted, f.fetchEnded
	if f.err != nil {
		return f.err
	}
	header, parts, err := protocol.ReadSetFile(f.data)
	if err != nil {
		return err
	}
	if header.Version != dagql.ValueBundleVersion {
		return fmt.Errorf("unsupported value bundle version %d", header.Version)
	}
	f.parts = parts
	for _, part := range header.Parts {
		f.unretained = append(f.unretained, part.Unretained)
	}
	f.report.PeakBefore = r.s.cfg.peakRSS()
	return nil
}

// mergePart decodes the file's next part, fills in its download addresses
// from the file's table, merges it, and counts the misses it would have
// served.
func (r *restorer) mergePart(ctx context.Context, f *restoreFile) error {
	began := time.Now()
	bundle, err := decodePart(f.parts[f.next])
	decoded := time.Since(began)
	f.report.Decode += decoded
	if err != nil {
		return fmt.Errorf("part %d: %w", f.next, err)
	}
	fillAddresses(&bundle, f.file.Table)
	records := len(bundle.Values)
	reply, priorityWait, err := r.s.adapter.RestorePart(ctx, bundle, f.unretained[f.next], r.s.known, f.source())
	f.report.Prepare += reply.Prepare
	f.report.Commit += reply.Commit
	if reply.Release != nil {
		f.releases = append(f.releases, reply.Release)
	}
	if err != nil {
		return fmt.Errorf("part %d: %w", f.next, err)
	}
	f.noteEntries(bundle, reply)
	committed := time.Unix(0, reply.EngineTimeUnixNano)
	f.parts[f.next] = nil
	f.next++
	f.result.Parts++
	f.result.Records += records
	addRows(&f.result.Rows, reply.Rows)
	if f.result.FirstCommit == 0 {
		f.result.FirstCommit = reply.EngineTimeUnixNano
	}
	part := RestoredPartReport{
		Records:          records,
		Committed:        committed,
		LockWait:         reply.LockWait,
		CommitHold:       reply.CommitHold,
		Decode:           decoded,
		Prepare:          reply.Prepare,
		Commit:           reply.Commit,
		PriorityWait:     priorityWait,
		PrepareLockWait:  reply.PrepareLockWait,
		PrepareLockWaits: reply.PrepareLockWaits,
	}
	r.lateMisses(reply, &part)
	f.report.Parts = append(f.report.Parts, part)
	return nil
}

// noteEntries records, for a committed part, the entry each record it
// carries in full landed on, at the record's first appearance in the file,
// and 0 for one its merge landed nowhere, with the parts that entry had
// complete as the merge left it.
func (f *restoreFile) noteEntries(part dagql.ValueBundle, reply dagql.MergeReply) {
	if f.placed == nil {
		f.placed = map[uint64]struct{}{}
	}
	landed := make(map[dagql.TransferOrdinal]dagql.MergedValue, len(reply.Values))
	for _, value := range reply.Values {
		landed[value.Ordinal] = value
	}
	for _, value := range part.Values {
		if value.Held != "" {
			continue
		}
		if _, ok := f.placed[value.SenderNumber]; ok {
			continue
		}
		f.placed[value.SenderNumber] = struct{}{}
		merged := landed[value.Ordinal]
		f.entries = append(f.entries, merged.Number)
		f.complete = append(f.complete, merged.Parts)
	}
}

// decodePart decodes one part of a set file with a decoder of its own,
// closed once the part is decoded: a decoder kept from one part to the next
// keeps the last part's decompressed bytes, 236 MB for test-base's whole
// set.
func decodePart(part []byte) (dagql.ValueBundle, error) {
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return dagql.ValueBundle{}, err
	}
	defer dec.Close()
	return protocol.DecodeSetFilePart(dec, part)
}

// lateMisses counts, for a committed part, the misses before its commit
// that a lookup now finds on an entry its merge landed on, and that no
// earlier part found (Adapter.LateMisses).
func (r *restorer) lateMisses(reply dagql.MergeReply, part *RestoredPartReport) {
	entries := make(map[uint64]struct{}, len(reply.Values))
	for _, value := range reply.Values {
		entries[value.Number] = struct{}{}
	}
	for _, miss := range r.s.adapter.LateMisses(part.Committed, entries) {
		late := part.Committed.Sub(miss.At)
		if part.Late == 0 {
			part.WorstLate, part.LeastLate = late, late
		}
		part.WorstLate, part.LeastLate = max(part.WorstLate, late), min(part.LeastLate, late)
		part.Late++
	}
}

// finish ends a file's restore, with the error that stopped it if any, and
// answers its restore once its last file is done.
func (r *restorer) finish(f *restoreFile, err error) {
	f.report.Window = readRestoreWindow(r.s.adapter.CacheWork).since(f.windowStart)
	if f.landed != nil {
		f.landed()
	}
	r.mu.Lock()
	r.heads = slices.DeleteFunc(r.heads, func(g *restoreFile) bool { return g == f })
	r.wholes = slices.DeleteFunc(r.wholes, func(g *restoreFile) bool { return g == f })
	r.mu.Unlock()
	if err == nil && f.next == len(f.parts) {
		f.result.Complete = true
		f.result.Entries = f.entries
		f.result.PartsTable, f.result.CompleteParts = protocol.PackCompleteParts(f.complete)
		f.kept = r.s.cfg.restored.add(f.result)
	}
	if err != nil {
		f.result.Error = err.Error()
	}
	if f.opened && f.report.PeakBefore != 0 {
		f.report.PeakAfter = r.s.cfg.peakRSS()
	}
	f.report.Complete, f.report.Error = f.result.Complete, f.result.Error
	f.data, f.parts, f.placed, f.complete = nil, nil, nil, nil
	r.release(f)
	r.s.adapter.Restored(f.report)
	level := "info"
	if err != nil {
		level = "warn"
	}
	r.logFile(f, level)
	r.mu.Lock()
	f.req.left--
	last := f.req.left == 0
	r.mu.Unlock()
	if last {
		r.reply(f.req)
	}
}

// release releases the holds a file's parts took on what they landed, once
// the file is done: what its roots retain stays, and the rest, such as the
// closure of a root no part of it landed, goes as unowned values do.
func (r *restorer) release(f *restoreFile) {
	for _, release := range f.releases {
		if err := release(context.WithoutCancel(r.s.ctx)); err != nil {
			r.s.cfg.Logger.Warn("remote cache restore: releasing a part's hold failed", "digest", f.file.Digest.String(), "error", err)
		}
	}
	f.releases = nil
}

// abandon ends the files left when the connection ends: what merged of
// them stays, and no reply goes.
func (r *restorer) abandon() {
	r.mu.Lock()
	left := slices.Concat(r.heads, r.wholes)
	r.heads, r.wholes = nil, nil
	r.mu.Unlock()
	for _, f := range left {
		if f.landed != nil {
			f.landed()
		}
		f.result.Error = errRestoreConnectionEnded.Error()
		f.report.Error = f.result.Error
		// A download still running is the session's, and is dropped with it.
		select {
		case <-f.fetched:
			f.report.FetchStarted, f.report.Fetched = f.fetchStarted, f.fetchEnded
		default:
		}
		f.parts = nil
		r.release(f)
		r.s.adapter.Restored(f.report)
		r.logFile(f, "warn")
	}
}

func (r *restorer) logFile(f *restoreFile, level string) {
	args := []any{
		"check", f.check,
		"kind", f.report.Kind,
		"digest", f.file.Digest.String(),
		"bytes", f.file.Size,
		"parts", f.result.Parts,
		"records", f.result.Records,
		"kept", f.result.Rows.Kept,
		"created", f.result.Rows.Created,
		"complete", f.result.Complete,
	}
	if !f.report.Fetched.IsZero() {
		args = append(args, "fetch", f.report.Fetched.Sub(f.report.FetchStarted))
		if t := f.timing; t.connected {
			args = append(args, "fetch_conn", t.conn, "fetch_new_conn", t.newConn)
		}
		if t := f.timing; t.firstByte {
			args = append(args, "fetch_ttfb", t.ttfb)
		}
	}
	if f.result.FirstCommit != 0 {
		args = append(args, "first_commit", time.Unix(0, f.result.FirstCommit).Sub(f.report.Arrived))
	}
	late := 0
	for _, part := range f.report.Parts {
		late += part.Late
	}
	args = append(args, "late_misses", late)
	if f.kept > 0 {
		args = append(args, "kept_answer_bytes", f.kept)
	}
	if f.result.Error != "" {
		args = append(args, "error", f.result.Error)
	}
	if level == "warn" {
		r.s.cfg.Logger.Warn("remote cache restore", args...)
		return
	}
	r.s.cfg.Logger.Info("remote cache restore", args...)
}

// reply answers a restore with its files, as the restore named them, heads
// first, unless the connection has ended.
func (r *restorer) reply(req *restoreRequest) {
	if r.s.ctx.Err() != nil {
		return
	}
	files := make([]protocol.RestoredFile, len(req.files))
	for i, f := range req.files {
		files[i] = f.result
	}
	_, generation := r.s.adapter.CacheIdentity()
	_ = r.s.reply(req.env, protocol.TypeRestored, protocol.Restored{Generation: generation, EngineTime: time.Now().UnixNano(), Files: files})
}

// fetch downloads a file whole, and checks its size and digest. Beside the
// download, it reads the engine's counters at the file's arrival, for its
// window (RestoreReport.Window).
func (r *restorer) fetch(f *restoreFile) {
	read := make(chan restoreReading, 1)
	go func() { read <- readRestoreWindow(r.s.adapter.CacheWork) }()
	f.fetchStarted = time.Now()
	f.data, f.timing, f.err = r.s.cfg.getFile(r.s.ctx, f.file)
	f.fetchEnded = time.Now()
	f.windowStart = <-read
	close(f.fetched)
}

// getSetFile downloads a set file, failing when the store goes idle longer
// than idle, when it isn't file.Size bytes, or when its bytes aren't the
// ones file.Digest names. Its errors never include the address, which
// carries a signature.
func getSetFile(client *http.Client, idle time.Duration) func(context.Context, protocol.SetFile) ([]byte, fetchTiming, error) {
	return func(ctx context.Context, file protocol.SetFile) ([]byte, fetchTiming, error) {
		// The transport may call the trace from its own goroutines.
		var mu sync.Mutex
		var traced fetchTiming
		timing := func() fetchTiming {
			mu.Lock()
			defer mu.Unlock()
			return traced
		}
		if err := file.Digest.Validate(); err != nil {
			return nil, timing(), fmt.Errorf("fetch: %w", err)
		}
		if file.Size < 0 {
			return nil, timing(), fmt.Errorf("fetch: a size of %d bytes", file.Size)
		}
		ctx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		stalled := time.AfterFunc(idle, func() { cancel(errFetchStalled) })
		defer stalled.Stop()
		began := time.Now()
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) {
				mu.Lock()
				defer mu.Unlock()
				traced.conn, traced.newConn, traced.connected = time.Since(began), !info.Reused, true
			},
			GotFirstResponseByte: func() {
				mu.Lock()
				defer mu.Unlock()
				traced.ttfb, traced.firstByte = time.Since(began), true
			},
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, file.Address.URL, nil)
		if err != nil {
			return nil, timing(), errors.New("fetch: invalid address")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, timing(), fetchError(ctx, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, timing(), fmt.Errorf("fetch: %s", resp.Status)
		}
		buf := bytes.NewBuffer(make([]byte, 0, file.Size))
		verifier := file.Digest.Verifier()
		body := &idleBody{Reader: io.LimitReader(resp.Body, file.Size+1), timer: stalled, idle: idle}
		if _, err := io.Copy(io.MultiWriter(buf, verifier), body); err != nil {
			return nil, timing(), fetchError(ctx, err)
		}
		if int64(buf.Len()) != file.Size {
			return nil, timing(), fmt.Errorf("fetch: %d bytes, the restore says %d", buf.Len(), file.Size)
		}
		if !verifier.Verified() {
			return nil, timing(), errors.New("fetch: the bytes don't match the file's digest")
		}
		return buf.Bytes(), timing(), nil
	}
}

// fetchTiming is how a file's download began, for its restore's line: its
// GET's wait for a connection and whether that connection was new, once it
// had one (connected), and its time to the response's first byte, once that
// came (firstByte), each from the GET's start.
type fetchTiming struct {
	conn, ttfb           time.Duration
	newConn              bool
	connected, firstByte bool
}

// errFetchStalled is the error of a download the store stopped sending.
var errFetchStalled = errors.New("the blob store stopped sending the file")

func fetchError(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); errors.Is(cause, errFetchStalled) {
		return fmt.Errorf("fetch: %w", cause)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return fmt.Errorf("fetch: %w", err)
}

// fillAddresses gives a part's outputs the download addresses of their
// layers from the file's table, where the part names them by place
// (dagql.ShareChainLayers): a layer its chain stores in a pack gets the
// pack's address on that chain, since another chain may store the same
// layer alone, and any other layer its blob's address in the bundle's. An
// address the table lacks is left out, for a read to renew.
func fillAddresses(bundle *dagql.ValueBundle, table protocol.AddressTable) {
	for i := range bundle.Outputs {
		out := &bundle.Outputs[i]
		if out.Chain == nil {
			continue
		}
		blobs := make([]digest.Digest, 0, len(out.Chain.Layers)+len(out.Layers))
		for _, layer := range out.Chain.Layers {
			blobs = append(blobs, layer.Descriptor.Digest)
		}
		for _, place := range out.Layers {
			if place >= 0 && place < len(bundle.Layers) {
				blobs = append(blobs, bundle.Layers[place].Descriptor.Digest)
			}
		}
		for _, blob := range blobs {
			if pack, ok := out.Chain.Packs[blob]; ok {
				if address, ok := table.Packs[pack]; ok {
					if out.Chain.Addresses == nil {
						out.Chain.Addresses = map[digest.Digest]dagql.BlobAddress{}
					}
					out.Chain.Addresses[blob] = address
				}
				continue
			}
			if address, ok := table.Blobs[blob]; ok {
				if bundle.Addresses == nil {
					bundle.Addresses = map[digest.Digest]dagql.BlobAddress{}
				}
				bundle.Addresses[blob] = address
			}
		}
	}
}

func addRows(rows *protocol.RowCounts, counts dagql.MergeRowCounts) {
	rows.Held += counts.Held
	rows.Kept += counts.Kept
	rows.Created += counts.Created
	rows.Stored += counts.Stored
	rows.Replaced += counts.Replaced
	rows.Retired += counts.Retired
}

// PeakResident is the process's peak resident memory, in bytes (VmHWM),
// or 0 when it can't be read.
func PeakResident() int64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		rest, ok := bytes.CutPrefix([]byte(line), []byte("VmHWM:"))
		if !ok {
			continue
		}
		fields := bytes.Fields(rest)
		if len(fields) != 2 || string(fields[1]) != "kB" {
			return 0
		}
		kb, err := strconv.ParseInt(string(fields[0]), 10, 64)
		if err != nil {
			return 0
		}
		return kb << 10
	}
	return 0
}
