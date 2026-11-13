package remotecache

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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
// each dropped before the next is decoded. Heads and wholes merge on lanes
// of their own, each in arrival order, so a head never waits for a whole,
// even another check's that started first. Nothing in a restore fails it,
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
// and a restore of one does nothing.
type restoredFiles struct {
	mu   sync.Mutex
	done []digest.Digest
}

func (r *restoredFiles) has(dgst digest.Digest) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.done, dgst)
}

func (r *restoredFiles) add(dgst digest.Digest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.done, dgst) {
		r.done = append(r.done, dgst)
	}
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
// CommitHold how long the commit held it (dagql.MergeReply).
type RestoredPartReport struct {
	Records    int
	Committed  time.Time
	Late       int
	WorstLate  time.Duration
	LeastLate  time.Duration
	LockWait   time.Duration
	CommitHold time.Duration
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

	// The rest is its lane's alone.
	opened bool
	parts  [][]byte
	next   int
	result protocol.RestoredFile
	report RestoreReport
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
	// heads and wholes are the two lanes' files not done, each in arrival
	// order.
	heads, wholes []*restoreFile
	// headQueued and wholeQueued hold a signal when a file was queued on
	// their lane.
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
		if r.s.cfg.restored.has(file.Digest) {
			f.result.Complete, f.result.Already = true, true
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

// run merges the queued files until the connection ends, on two lanes:
// heads on one and wholes on the other, so a head never waits for a whole.
func (r *restorer) run() {
	var lanes sync.WaitGroup
	lanes.Go(func() { r.lane(true) })
	lanes.Go(func() { r.lane(false) })
	lanes.Wait()
	r.abandon()
}

// lane merges its files' parts one at a time, in arrival order, each once
// it is downloaded, until the connection ends.
func (r *restorer) lane(heads bool) {
	ctx, unlabel := wcprof.WithCPUKind(r.s.ctx, wcprof.CPUKindMerge)
	defer unlabel()
	queued := r.wholeQueued
	if heads {
		queued = r.headQueued
	}
	var dec *zstd.Decoder
	defer func() {
		if dec != nil {
			dec.Close()
		}
	}()
	for ctx.Err() == nil {
		f := r.pick(heads)
		if f == nil {
			select {
			case <-queued:
				continue
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-f.fetched:
		case <-ctx.Done():
			return
		}
		if dec == nil {
			var err error
			if dec, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1)); err != nil {
				r.finish(f, err)
				continue
			}
		}
		if ctx.Err() == nil {
			r.step(ctx, dec, f)
		}
	}
}

// pick returns the first file not done on a lane.
func (r *restorer) pick(heads bool) *restoreFile {
	r.mu.Lock()
	defer r.mu.Unlock()
	files := r.wholes
	if heads {
		files = r.heads
	}
	if len(files) > 0 {
		return files[0]
	}
	return nil
}

// step opens a fetched file, or merges its next part, and finishes it once
// it has no part left or something stopped it.
func (r *restorer) step(ctx context.Context, dec *zstd.Decoder, f *restoreFile) {
	if !f.opened {
		if err := r.open(f); err != nil {
			r.finish(f, err)
			return
		}
		if len(f.parts) == 0 {
			r.finish(f, nil)
			return
		}
	}
	if err := r.mergePart(ctx, dec, f); err != nil {
		r.finish(f, err)
		return
	}
	if f.next == len(f.parts) {
		r.finish(f, nil)
	}
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
	f.report.PeakBefore = r.s.cfg.peakRSS()
	return nil
}

// mergePart decodes the file's next part, fills in its download addresses
// from the file's table, merges it, and counts the misses it would have
// served.
func (r *restorer) mergePart(ctx context.Context, dec *zstd.Decoder, f *restoreFile) error {
	bundle, err := protocol.DecodeSetFilePart(dec, f.parts[f.next])
	if err != nil {
		return fmt.Errorf("part %d: %w", f.next, err)
	}
	fillAddresses(&bundle, f.file.Table)
	records := len(bundle.Values)
	reply, err := r.s.adapter.RestorePart(ctx, bundle, r.s.known)
	if err != nil {
		return fmt.Errorf("part %d: %w", f.next, err)
	}
	committed := time.Unix(0, reply.EngineTimeUnixNano)
	f.parts[f.next] = nil
	f.next++
	f.result.Parts++
	f.result.Records += records
	addRows(&f.result.Rows, reply.Rows)
	if f.result.FirstCommit == 0 {
		f.result.FirstCommit = reply.EngineTimeUnixNano
	}
	part := RestoredPartReport{Records: records, Committed: committed, LockWait: reply.LockWait, CommitHold: reply.CommitHold}
	r.lateMisses(reply, &part)
	f.report.Parts = append(f.report.Parts, part)
	return nil
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
	r.mu.Lock()
	r.heads = slices.DeleteFunc(r.heads, func(g *restoreFile) bool { return g == f })
	r.wholes = slices.DeleteFunc(r.wholes, func(g *restoreFile) bool { return g == f })
	r.mu.Unlock()
	if err == nil && f.next == len(f.parts) {
		f.result.Complete = true
		r.s.cfg.restored.add(f.file.Digest)
	}
	if err != nil {
		f.result.Error = err.Error()
	}
	if f.opened && f.report.PeakBefore != 0 {
		f.report.PeakAfter = r.s.cfg.peakRSS()
	}
	f.report.Complete, f.report.Error = f.result.Complete, f.result.Error
	f.data, f.parts = nil, nil
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

// abandon ends the files left when the connection ends: what merged of
// them stays, and no reply goes.
func (r *restorer) abandon() {
	r.mu.Lock()
	left := slices.Concat(r.heads, r.wholes)
	r.heads, r.wholes = nil, nil
	r.mu.Unlock()
	for _, f := range left {
		f.result.Error = errRestoreConnectionEnded.Error()
		f.report.Error = f.result.Error
		// A download still running is the session's, and is dropped with it.
		select {
		case <-f.fetched:
			f.report.FetchStarted, f.report.Fetched = f.fetchStarted, f.fetchEnded
		default:
		}
		f.parts = nil
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
	}
	if f.result.FirstCommit != 0 {
		args = append(args, "first_commit", time.Unix(0, f.result.FirstCommit).Sub(f.report.Arrived))
	}
	late := 0
	for _, part := range f.report.Parts {
		late += part.Late
	}
	args = append(args, "late_misses", late)
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

// fetch downloads a file whole, and checks its size and digest.
func (r *restorer) fetch(f *restoreFile) {
	f.fetchStarted = time.Now()
	f.data, f.err = r.s.cfg.getFile(r.s.ctx, f.file)
	f.fetchEnded = time.Now()
	close(f.fetched)
}

// getSetFile downloads a set file, failing when the store goes idle longer
// than idle, when it isn't file.Size bytes, or when its bytes aren't the
// ones file.Digest names. Its errors never include the address, which
// carries a signature.
func getSetFile(client *http.Client, idle time.Duration) func(context.Context, protocol.SetFile) ([]byte, error) {
	return func(ctx context.Context, file protocol.SetFile) ([]byte, error) {
		if err := file.Digest.Validate(); err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}
		if file.Size < 0 {
			return nil, fmt.Errorf("fetch: a size of %d bytes", file.Size)
		}
		ctx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		stalled := time.AfterFunc(idle, func() { cancel(errFetchStalled) })
		defer stalled.Stop()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, file.Address.URL, nil)
		if err != nil {
			return nil, errors.New("fetch: invalid address")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fetchError(ctx, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch: %s", resp.Status)
		}
		buf := bytes.NewBuffer(make([]byte, 0, file.Size))
		verifier := file.Digest.Verifier()
		body := &idleBody{Reader: io.LimitReader(resp.Body, file.Size+1), timer: stalled, idle: idle}
		if _, err := io.Copy(io.MultiWriter(buf, verifier), body); err != nil {
			return nil, fetchError(ctx, err)
		}
		if int64(buf.Len()) != file.Size {
			return nil, fmt.Errorf("fetch: %d bytes, the restore says %d", buf.Len(), file.Size)
		}
		if !verifier.Verified() {
			return nil, errors.New("fetch: the bytes don't match the file's digest")
		}
		return buf.Bytes(), nil
	}
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
