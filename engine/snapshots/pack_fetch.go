package snapshots

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/remotecache/pack"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/wcprof"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	// packLeaseTTL is how long a read's lease keeps the pack's members, from
	// before the read, for the imports that need them; packReadLimit bounds
	// the read itself, waiting for a slot included, so that the lease always
	// outlives it.
	packLeaseTTL = 10 * time.Minute
	// packReads is how many packs the engine reads at once, and packMaxBytes
	// the largest pack it reads: as large as the service packs, 16 MiB, so
	// that a chain's content blobs and listings, most far smaller than a
	// layer's tar, travel in as few objects as today's layers.
	packReads    = 4
	packMaxBytes = 16 << 20
	// packFetchMisses bounds the reads one import makes for a member a read
	// stored that is gone again before the import links it.
	packFetchMisses = 3
)

// packReadLimit is half the lease's time; tests shorten it.
var packReadLimit = packLeaseTTL / 2

// PackProvider is a content provider some of whose blobs are stored in
// packs: one object holding several small blobs, read whole.
type PackProvider interface {
	// PackOf returns the ID of the pack that holds blob, if one does.
	PackOf(blob digest.Digest) (digest.Digest, bool)
	// OpenPack opens the whole pack that holds blob.
	OpenPack(ctx context.Context, blob digest.Digest) (io.ReadCloser, error)
}

// PackRangeProvider is a PackProvider that also reads part of a pack, so a
// chain that needs a small share of a pack reads only its own members
// (chainPacks).
type PackRangeProvider interface {
	PackProvider
	// OpenPackRange opens size bytes of the pack that holds blob, from off.
	// A range past the pack's end is cut at its end.
	OpenPackRange(ctx context.Context, blob digest.Digest, off, size int64) (io.ReadCloser, error)
}

// ErrNoPackRanges is a PackRangeProvider's answer when what it serves reads
// no ranges: the pack is then read whole.
var ErrNoPackRanges = errors.New("the provider reads no pack ranges")

const (
	// packHeaderRange is the first range of a pack read for its header:
	// room for about 800 members' lines. A longer header is read again,
	// up to the format's limit.
	packHeaderRange = 64 << 10
	// packSpanGap merges a chain's members of a pack read by ranges: two
	// closer than this are read in one range, the bytes between them read
	// and dropped.
	packSpanGap = 1 << 20
)

// chainPacks is how one chain import reads the packs that hold its blobs: a
// pack the chain needs at least half the bytes of is read whole, as any
// import reads a pack (downloadPackedContent), and from any other only the
// chain's members, in ranges (packPlan). The packs the service fills are an
// upload's missing blobs, up to 16 MiB, so one chain's members can be a
// small share of a pack; on the remote cache's bench, reading whole every
// pack the dev engine's chain touched read 936 MB for its 620 MB.
type chainPacks struct {
	mu    sync.Mutex
	plans map[digest.Digest]*packPlan
}

func (c *chainPacks) plan(id digest.Digest) *packPlan {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.plans == nil {
		c.plans = map[digest.Digest]*packPlan{}
	}
	p := c.plans[id]
	if p == nil {
		p = &packPlan{done: make(chan struct{}), reading: make(chan struct{}, 1)}
		c.plans[id] = p
	}
	return p
}

// packPlan is how one chain reads one pack, decided once from the pack's
// header: whole, or by its spans. A pack the header's range held whole is
// stored from it. Set before done is closed.
//
// The chain reads the pack's spans one after another, through reading: a
// pack is one read of the blob store in flight however it is read, as a
// chain's downloads ahead count it (chainAheadWindow).
type packPlan struct {
	once    sync.Once
	done    chan struct{}
	err     error
	whole   bool
	stored  bool
	spans   map[digest.Digest]*packSpan
	reading chan struct{}
}

// packSpan is a range of a pack holding some of a chain's members, read
// once; done is closed once err is set.
type packSpan struct {
	off, end int64
	members  []spanMember
	once     sync.Once
	done     chan struct{}
	err      error
}

type spanMember struct {
	pack.Member
	off int64
}

// downloadPackedRange puts desc's blob, a member of the pack id, in the
// content store, linked to ctx's lease, by reading only owner's members of
// the pack, when owner needs less than half of it, unless the store has it. It reports false, and
// reads nothing but the pack's header, when the pack is read whole instead.
// The header and the spans are read detached from ctx, under their own
// deadline: other imports of the chain may wait on them.
func (cm *snapshotManager) downloadPackedRange(ctx context.Context, desc ocispecs.Descriptor, provider PackRangeProvider, id digest.Digest, owner *ExportChain) (bool, error) {
	// A read of the pack another import made may have stored it already.
	if ok, err := cm.pinContent(ctx, desc); err != nil || ok {
		return true, err
	}
	plan := owner.packs.plan(id)
	plan.once.Do(func() {
		go func() {
			defer close(plan.done)
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), packReadLimit)
			defer cancel()
			plan.err = cm.planPack(dctx, plan, provider, id, desc.Digest, owner)
		}()
	})
	select {
	case <-plan.done:
	case <-ctx.Done():
		return true, context.Cause(ctx)
	}
	if plan.err != nil {
		var contentErr *packContentError
		if errors.As(plan.err, &contentErr) {
			return true, chainError(ctx, true, desc, contentErr.stage, contentErr.err)
		}
		return true, plan.err
	}
	if plan.stored {
		if ok, err := cm.pinContent(ctx, desc); err != nil || ok {
			return true, err
		}
		return true, chainError(ctx, true, desc, "provider", fmt.Errorf("pack %s does not hold blob %s", id, desc.Digest))
	}
	span := plan.spans[desc.Digest]
	if plan.whole || span == nil {
		return false, nil
	}
	span.once.Do(func() {
		go func() {
			defer close(span.done)
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), packReadLimit)
			defer cancel()
			// The wait for the pack's other spans counts in the span's
			// deadline, as a pack read's wait for its slot does.
			select {
			case plan.reading <- struct{}{}:
				defer func() { <-plan.reading }()
			case <-dctx.Done():
				span.err = &packContentError{stage: "provider", err: context.Cause(dctx)}
				return
			}
			span.err = cm.readPackSpan(dctx, span, provider, id, desc.Digest)
		}()
	})
	select {
	case <-span.done:
	case <-ctx.Done():
		return true, context.Cause(ctx)
	}
	if ok, err := cm.pinContent(ctx, desc); err != nil || ok {
		return true, err
	}
	err := span.err
	if err == nil {
		err = &packContentError{stage: "provider", err: fmt.Errorf("pack %s's blob %s is gone after its range was read", id, desc.Digest)}
	}
	var contentErr *packContentError
	if errors.As(err, &contentErr) {
		return true, chainError(ctx, true, desc, contentErr.stage, contentErr.err)
	}
	return true, err
}

// planPack reads the pack's header by range and decides how owner reads
// it: whole when owner's members are at least half its bytes, else by
// spans of them, merged across gaps under packSpanGap. A pack the header's
// range held whole is stored from it, as a whole read stores it.
func (cm *snapshotManager) planPack(ctx context.Context, plan *packPlan, provider PackRangeProvider, id, blob digest.Digest, owner *ExportChain) error {
	list, headerSize, data, err := readPackHeader(ctx, provider, id, blob)
	if errors.Is(err, ErrNoPackRanges) {
		plan.whole = true
		return nil
	}
	if err != nil {
		return err
	}
	size, err := pack.Size(list)
	if err != nil {
		return err
	}
	if int64(len(data)) >= size {
		if err := cm.storePackMembers(ctx, id, list, bytes.NewReader(data[headerSize:size])); err != nil {
			return err
		}
		plan.stored = true
		return nil
	}
	need := map[digest.Digest]bool{}
	for _, layer := range owner.Layers {
		if at, ok := provider.PackOf(layer.Descriptor.Digest); ok && at == id {
			need[layer.Descriptor.Digest] = true
		}
	}
	var members []spanMember
	var needed int64
	off := headerSize
	for _, m := range list {
		if need[m.Digest] {
			members = append(members, spanMember{Member: m, off: off})
			needed += m.Size
		}
		off += m.Size
	}
	if 2*needed >= size {
		plan.whole = true
		return nil
	}
	plan.spans = map[digest.Digest]*packSpan{}
	var span *packSpan
	for _, m := range members {
		if span == nil || m.off-span.end >= packSpanGap {
			span = &packSpan{off: m.off, done: make(chan struct{})}
		}
		span.members = append(span.members, m)
		span.end = m.off + m.Size
		plan.spans[m.Digest] = span
	}
	return nil
}

// readPackHeader reads a pack's header by range, checked against its ID,
// and returns its members, its size in bytes, and the bytes the range read,
// which hold the whole pack when it is no larger than the range.
func readPackHeader(ctx context.Context, provider PackRangeProvider, id, blob digest.Digest) ([]pack.Member, int64, []byte, error) {
	for _, n := range []int64{packHeaderRange, pack.MaxHeaderBytes} {
		body, err := provider.OpenPackRange(ctx, blob, 0, n)
		if errors.Is(err, ErrNoPackRanges) {
			return nil, 0, nil, err
		}
		if err != nil {
			return nil, 0, nil, &packContentError{stage: "provider", err: err}
		}
		data, err := io.ReadAll(io.LimitReader(body, n))
		_ = body.Close()
		if err != nil {
			return nil, 0, nil, &packContentError{stage: "provider", err: err}
		}
		list, err := pack.ReadHeader(bufio.NewReaderSize(bytes.NewReader(data), pack.MaxHeaderBytes), id)
		if errors.Is(err, io.ErrUnexpectedEOF) && int64(len(data)) == n && n < pack.MaxHeaderBytes {
			continue
		}
		if err != nil {
			return nil, 0, nil, &packContentError{stage: "provider", err: err}
		}
		// The bytes read are this header, as ReadHeader checked.
		header, err := pack.Header(list)
		if err != nil {
			return nil, 0, nil, &packContentError{stage: "provider", err: err}
		}
		return list, int64(len(header)), data, nil
	}
	return nil, 0, nil, &packContentError{stage: "provider", err: fmt.Errorf("pack %s header is over %d bytes", id, pack.MaxHeaderBytes)}
}

// storePackMembers writes every member of the pack id, read in order from
// r, into the content store, each checked against its digest, under a lease
// of its own that keeps them for packLeaseTTL, as a whole read does.
func (cm *snapshotManager) storePackMembers(ctx context.Context, id digest.Digest, list []pack.Member, r io.Reader) error {
	l, err := cm.LeaseManager.Create(ctx, leases.WithRandomID(), leases.WithExpiration(packLeaseTTL), leases.WithLabels(map[string]string{snapshotTransferLeaseLabel: "true"}))
	if err != nil {
		return err
	}
	ctx = leases.WithLease(ctx, l.ID)
	for _, m := range list {
		if _, err := cm.writePackMember(ctx, r, m); err != nil {
			return err
		}
	}
	slog.FromContext(ctx).Info("remote cache pack fetched with its header", "pack", id.String(), "members", len(list))
	return nil
}

// readPackSpan reads a span of the pack id by range and writes its members
// into the content store, each checked against its digest, under a lease of
// its own that keeps them for packLeaseTTL, as a whole read's does.
func (cm *snapshotManager) readPackSpan(ctx context.Context, span *packSpan, provider PackRangeProvider, id, blob digest.Digest) (rerr error) {
	start := time.Now()
	read := &countingReader{}
	stored := 0
	defer func() {
		args := []any{"pack", id.String(), "members", len(span.members), "stored", stored, "bytes", read.n, "duration", time.Since(start)}
		level := slog.LevelInfo
		if rerr != nil {
			level = slog.LevelWarn
			args = append(args, "error", rerr.Error())
		}
		slog.FromContext(ctx).Log(ctx, level, "remote cache pack range fetched", args...)
	}()
	l, err := cm.LeaseManager.Create(ctx, leases.WithRandomID(), leases.WithExpiration(packLeaseTTL), leases.WithLabels(map[string]string{snapshotTransferLeaseLabel: "true"}))
	if err != nil {
		return err
	}
	ctx = leases.WithLease(ctx, l.ID)
	body, err := provider.OpenPackRange(ctx, blob, span.off, span.end-span.off)
	if err != nil {
		return &packContentError{stage: "provider", err: err}
	}
	defer body.Close()
	read.r = body
	r := bufio.NewReaderSize(read, 64<<10)
	at := span.off
	for _, m := range span.members {
		if _, err := io.CopyN(io.Discard, r, m.off-at); err != nil {
			return packReadError(err)
		}
		if _, err := cm.writePackMember(ctx, r, m.Member); err != nil {
			return err
		}
		stored++
		at = m.off + m.Size
	}
	return nil
}

// packFetches reads packs one at a time per pack: an import that needs a
// member of a pack another import is reading joins that read. A read writes
// every member of the pack into the content store under a lease created
// before it, which expires packLeaseTTL later: the imports waiting for the
// read link their members to their own leases once it ends, and the imports
// that need the other members within that time find them in the store. The
// lease is labeled as a transfer, so an engine start drops it.
type packFetches struct {
	mu       sync.Mutex
	inflight map[digest.Digest]*packFetch
	slots    chan struct{}
}

// packFetch is one read of a pack.
type packFetch struct {
	id digest.Digest
	// blob is the member the read was started for, whose address it reads.
	blob     digest.Digest
	provider PackProvider
	lease    leases.Lease
	deadline time.Time
	done     chan struct{}
	cancel   context.CancelCauseFunc

	// Set before done is closed: the read's error, and the members its
	// header listed.
	err    error
	listed map[digest.Digest]bool

	// Under packFetches.mu: the imports waiting on the read, and whether it
	// ended.
	refs     int
	finished bool
}

// packContentError is a read's failure that is the pack's content: its
// provider, its bytes, or a member's digest. Other failures, of the local
// store or leases, are the engine's own and are returned as they are.
type packContentError struct {
	stage string
	err   error
}

func (e *packContentError) Error() string { return e.err.Error() }
func (e *packContentError) Unwrap() error { return e.err }

// beforeJoinPackFetch runs as a download of a packed blob looks for a read
// of its pack to join; tests set it to hold the download there.
var beforeJoinPackFetch = func() {}

// downloadPackedContent puts desc's blob, a member of the pack id, in the
// content store, linked to ctx's lease, by joining a read of the pack in
// progress or by reading it from provider, or by finding it stored by a read
// that ended (joinPackFetch). After each read it links the blob and checks
// for it, whoever read it. A read that fails fails every import that waited
// on it, whoever started it: none reads the pack again.
func (cm *snapshotManager) downloadPackedContent(ctx context.Context, desc ocispecs.Descriptor, provider PackProvider, id digest.Digest) error {
	misses := 0
	for {
		f, present, err := cm.joinPackFetch(ctx, id, provider, desc.Digest)
		if err != nil {
			return err
		}
		if present {
			// A read of the pack that ended stored the blob: link it.
			if ok, err := cm.pinContent(ctx, desc); err != nil || ok {
				return err
			}
			continue
		}
		select {
		case <-f.done:
		case <-ctx.Done():
			cm.leavePackFetch(f)
			return context.Cause(ctx)
		}
		cm.leavePackFetch(f)
		// Linked first, then checked, whichever read stored it.
		if ok, err := cm.pinContent(ctx, desc); err != nil || ok {
			return err
		}
		switch {
		case f.err != nil:
			err = f.err
		case !f.listed[desc.Digest]:
			err = &packContentError{stage: "provider", err: fmt.Errorf("pack %s does not hold blob %s", id, desc.Digest)}
		default:
			// The read stored it, and it went before this import linked it.
			if misses++; misses < packFetchMisses {
				continue
			}
			err = &packContentError{stage: "provider", err: fmt.Errorf("pack %s's blob %s is gone after %d reads", id, desc.Digest, misses)}
		}
		var contentErr *packContentError
		if errors.As(err, &contentErr) {
			return chainError(ctx, true, desc, contentErr.stage, contentErr.err)
		}
		return err
	}
}

// joinPackFetch holds the read of the pack id in progress, or starts one with
// provider, at blob's address. With no read to join it reports present
// instead when the content store has blob: a read of the pack that ended
// stored it.
func (cm *snapshotManager) joinPackFetch(ctx context.Context, id digest.Digest, provider PackProvider, blob digest.Digest) (_ *packFetch, present bool, _ error) {
	beforeJoinPackFetch()
	ps := &cm.packs
	ps.mu.Lock()
	if f := ps.inflight[id]; f != nil {
		f.refs++
		ps.mu.Unlock()
		return f, false, nil
	}
	if cm.storedLocked(ctx, blob) {
		ps.mu.Unlock()
		return nil, true, nil
	}
	ps.mu.Unlock()
	deadline := time.Now().Add(packReadLimit)
	l, err := cm.LeaseManager.Create(ctx, leases.WithRandomID(), leases.WithExpiration(packLeaseTTL), leases.WithLabels(map[string]string{snapshotTransferLeaseLabel: "true"}))
	if err != nil {
		return nil, false, err
	}
	// The read runs detached from the import that started it, while another
	// may wait for it.
	fctx, cancel := context.WithCancelCause(leases.WithLease(context.WithoutCancel(ctx), l.ID))
	read := &packFetch{id: id, blob: blob, provider: provider, lease: l, deadline: deadline, done: make(chan struct{}), cancel: cancel, listed: map[digest.Digest]bool{}, refs: 1}
	ps.mu.Lock()
	if f := ps.inflight[id]; f != nil {
		f.refs++
		ps.mu.Unlock()
		cancel(nil)
		_ = cm.LeaseManager.Delete(context.WithoutCancel(ctx), l)
		return f, false, nil
	}
	if cm.storedLocked(ctx, blob) {
		ps.mu.Unlock()
		cancel(nil)
		_ = cm.LeaseManager.Delete(context.WithoutCancel(ctx), l)
		return nil, true, nil
	}
	if ps.inflight == nil {
		ps.inflight = map[digest.Digest]*packFetch{}
		ps.slots = make(chan struct{}, packReads)
	}
	ps.inflight[id] = read
	slots := ps.slots
	ps.mu.Unlock()
	go cm.runPackFetch(fctx, read, slots)
	return read, false, nil
}

// storedLocked reports whether the content store has blob. Requires the
// packs' lock: a read of a pack writes its members before it leaves
// inflight under that lock, so a read that ended without a successor to
// join has stored what it read.
func (cm *snapshotManager) storedLocked(ctx context.Context, blob digest.Digest) bool {
	_, err := cm.ContentStore.Info(ctx, blob)
	return err == nil
}

// leavePackFetch drops one import's hold of f. The last hold of a read still
// running abandons it: no import joins it after, and it's canceled.
func (cm *snapshotManager) leavePackFetch(f *packFetch) {
	ps := &cm.packs
	ps.mu.Lock()
	f.refs--
	abandon := f.refs == 0 && !f.finished
	if abandon && ps.inflight[f.id] == f {
		delete(ps.inflight, f.id)
	}
	ps.mu.Unlock()
	if abandon {
		f.cancel(errFetchAbandoned)
	}
}

// runPackFetch reads the pack f names with its provider, within its deadline
// and once a slot is free, and writes every member into the content store
// under f's lease.
func (cm *snapshotManager) runPackFetch(ctx context.Context, f *packFetch, slots chan struct{}) {
	ctx, unlabel := wcprof.WithCPUKind(ctx, wcprof.CPUKindFetch)
	defer unlabel()
	start := time.Now()
	// queued is how long the read waited for a slot.
	var queued time.Duration
	read := &countingReader{}
	members, stored, written, present := 0, 0, 0, 0
	err := func() error {
		ctx, cancel := context.WithDeadline(ctx, f.deadline)
		defer cancel()
		select {
		case slots <- struct{}{}:
			queued = time.Since(start)
			defer func() { <-slots }()
		case <-ctx.Done():
			queued = time.Since(start)
			// The read's own deadline is the source's: too slow to answer.
			return &packContentError{stage: "provider", err: context.Cause(ctx)}
		}
		body, err := f.provider.OpenPack(ctx, f.blob)
		if err != nil {
			return &packContentError{stage: "provider", err: err}
		}
		defer body.Close()
		read.r = body
		r := bufio.NewReaderSize(read, 64<<10)
		list, err := pack.ReadHeader(r, f.id)
		if err != nil {
			return &packContentError{stage: "provider", err: err}
		}
		size, err := pack.Size(list)
		if err != nil || size > packMaxBytes {
			return &packContentError{stage: "provider", err: fmt.Errorf("pack %s is over %d bytes", f.id, packMaxBytes)}
		}
		members = len(list)
		for _, m := range list {
			f.listed[m.Digest] = true
		}
		for _, m := range list {
			wrote, err := cm.writePackMember(ctx, r, m)
			if err != nil {
				return err
			}
			stored++
			if wrote {
				written++
			} else {
				present++
			}
		}
		return nil
	}()
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	}
	f.err = err
	if stored == 0 {
		_ = cm.LeaseManager.Delete(context.WithoutCancel(ctx), f.lease)
	}

	outcome := "done"
	switch {
	case errors.Is(err, errFetchAbandoned):
		outcome = "abandoned"
	case err != nil:
		outcome = "error"
	}
	args := []any{"pack", f.id.String(), "members", members, "written", written, "present", present, "bytes", read.n, "duration", time.Since(start), "queued", queued, "outcome", outcome}
	level := slog.LevelInfo
	if outcome == "error" {
		level = slog.LevelWarn
	}
	if err != nil {
		args = append(args, "error", err.Error())
	}
	slog.FromContext(ctx).Log(ctx, level, "remote cache pack fetched", args...)

	ps := &cm.packs
	ps.mu.Lock()
	f.finished = true
	if ps.inflight[f.id] == f {
		delete(ps.inflight, f.id)
	}
	ps.mu.Unlock()
	f.cancel(nil)
	close(f.done)
}

// writePackMember writes the member m, read from r, into the content store
// under ctx's lease, or holds it there when the store has it already, and
// reads past its bytes. It reports whether it wrote the member. A short read
// or a digest that doesn't match is the pack's content; any other failure is
// the store's.
func (cm *snapshotManager) writePackMember(ctx context.Context, r io.Reader, m pack.Member) (_ bool, rerr error) {
	desc := ocispecs.Descriptor{Digest: m.Digest, Size: m.Size}
	// Held before the presence check, so that present content can't go.
	if err := cm.linkContentToContextLease(ctx, desc); err != nil {
		return false, err
	}
	skip := func() (bool, error) {
		if _, err := io.CopyN(io.Discard, r, m.Size); err != nil {
			return false, packReadError(err)
		}
		return false, nil
	}
	if _, err := cm.ContentStore.Info(ctx, m.Digest); err == nil {
		return skip()
	}
	ref := "snapshot-pack-" + rand.Text()
	writer, err := content.OpenWriter(ctx, cm.ContentStore, content.WithRef(ref), content.WithDescriptor(desc))
	if cerrdefs.IsAlreadyExists(err) {
		return skip()
	}
	if err != nil {
		return false, err
	}
	defer func() {
		_ = writer.Close()
		if rerr != nil {
			_ = cm.ContentStore.Abort(context.WithoutCancel(ctx), ref)
		}
	}()
	// The store can hold the member's bytes without a record of it here, as
	// it holds the engine image's blobs in place. Its writer then starts with
	// all of them written, and a write would copy them before the new bytes:
	// the member is only recorded.
	status, err := writer.Status()
	if err != nil {
		return false, err
	}
	if status.Offset == m.Size {
		if _, err := io.CopyN(io.Discard, r, m.Size); err != nil {
			return false, packReadError(err)
		}
		if err := writer.Commit(ctx, m.Size, m.Digest); err != nil && !cerrdefs.IsAlreadyExists(err) {
			return false, fmt.Errorf("commit pack member %s: %w", m.Digest, err)
		}
		return false, nil
	}
	source := &packMemberReader{r: r, digester: digest.SHA256.Digester()}
	if _, err := io.CopyN(writer, source, m.Size); err != nil {
		if source.err != nil {
			return false, packReadError(source.err)
		}
		return false, err
	}
	if got := source.digester.Digest(); got != m.Digest {
		return false, &packContentError{stage: "copy", err: fmt.Errorf("pack member checksum mismatch: expected %s, got %s", m.Digest, got)}
	}
	if err := writer.Commit(ctx, m.Size, m.Digest); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return false, fmt.Errorf("commit pack member %s: %w", m.Digest, err)
	}
	return true, nil
}

// packMemberReader reads a member's bytes from the pack, digesting them and
// keeping the pack's own read error apart from the store's.
type packMemberReader struct {
	r        io.Reader
	digester digest.Digester
	err      error
}

func (p *packMemberReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.digester.Hash().Write(b[:n])
	if err != nil {
		p.err = err
	}
	return n, err
}

// packReadError is a failure reading the pack's bytes: its content. The pack
// ending early is a short read.
func packReadError(err error) error {
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return &packContentError{stage: "copy", err: err}
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
