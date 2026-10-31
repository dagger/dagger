package snapshots

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/remotecache/pack"
	"github.com/dagger/dagger/engine/slog"
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
	// the largest pack it reads.
	packReads    = 4
	packMaxBytes = 8 << 20
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

// downloadPackedContent puts desc's blob, a member of the pack id, in the
// content store, linked to ctx's lease, by joining a read of the pack in
// progress or by reading it from provider. Before it joins or starts a read,
// and after each, it links the blob and checks for it, whoever read it. Another import's failed read is
// not this one's: its next read is its own.
func (cm *snapshotManager) downloadPackedContent(ctx context.Context, desc ocispecs.Descriptor, provider PackProvider, id digest.Digest) error {
	own, misses := false, 0
	for {
		// A read of the pack another import started may have stored the
		// blob since this download found it missing: it reads nothing then.
		if present, err := cm.pinContent(ctx, desc); err != nil || present {
			return err
		}
		f, started, err := cm.joinPackFetch(ctx, id, provider, desc.Digest, own)
		if err != nil {
			return err
		}
		select {
		case <-f.done:
		case <-ctx.Done():
			cm.leavePackFetch(f)
			return context.Cause(ctx)
		}
		cm.leavePackFetch(f)
		// Linked first, then checked, whichever read stored it.
		present, err := cm.pinContent(ctx, desc)
		if err != nil || present {
			return err
		}
		mine := started || sameProvider(f.provider, provider)
		switch {
		case f.err != nil && !mine:
			own = true
			continue
		case f.err != nil:
			err = f.err
		case !f.listed[desc.Digest]:
			err = &packContentError{stage: "provider", err: fmt.Errorf("pack %s does not hold blob %s", id, desc.Digest)}
		default:
			// The read stored it, and it went before this import linked it.
			if misses++; misses < packFetchMisses {
				own = false
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

// sameProvider reports whether a and b are one provider. Providers are
// pointers; any other kind, which may not be comparable, counts as another.
func sameProvider(a, b PackProvider) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	return va.Kind() == reflect.Pointer && vb.Kind() == reflect.Pointer && va.Type() == vb.Type() && va.Pointer() == vb.Pointer()
}

// joinPackFetch holds the read of the pack id in progress, or starts one with
// provider, at blob's address, and reports whether it started it. An own
// read never joins another: it starts one with provider, which other imports
// join when none of theirs runs.
func (cm *snapshotManager) joinPackFetch(ctx context.Context, id digest.Digest, provider PackProvider, blob digest.Digest, own bool) (*packFetch, bool, error) {
	ps := &cm.packs
	ps.mu.Lock()
	if f := ps.inflight[id]; f != nil && !own {
		f.refs++
		ps.mu.Unlock()
		return f, false, nil
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
	started := &packFetch{id: id, blob: blob, provider: provider, lease: l, deadline: deadline, done: make(chan struct{}), cancel: cancel, listed: map[digest.Digest]bool{}, refs: 1}
	ps.mu.Lock()
	if f := ps.inflight[id]; f != nil && !own {
		f.refs++
		ps.mu.Unlock()
		cancel(nil)
		_ = cm.LeaseManager.Delete(context.WithoutCancel(ctx), l)
		return f, false, nil
	}
	if ps.inflight == nil {
		ps.inflight = map[digest.Digest]*packFetch{}
		ps.slots = make(chan struct{}, packReads)
	}
	if ps.inflight[id] == nil {
		ps.inflight[id] = started
	}
	slots := ps.slots
	ps.mu.Unlock()
	go cm.runPackFetch(fctx, started, slots)
	return started, true, nil
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
	start := time.Now()
	read := &countingReader{}
	members, stored, written, present := 0, 0, 0, 0
	err := func() error {
		ctx, cancel := context.WithDeadline(ctx, f.deadline)
		defer cancel()
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-ctx.Done():
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
	args := []any{"pack", f.id.String(), "members", members, "written", written, "present", present, "bytes", read.n, "duration", time.Since(start), "outcome", outcome}
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
