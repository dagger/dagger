package resolver

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/remotes"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/engine/wcprof"
)

// Wall-clock profiling (wcprof) of registry I/O: a pull, resolve or push is
// an io op naming the ref, and each blob a pull downloads or a push uploads
// an op under it, so a profile tells the registry's time from the engine's.
// They're no-ops unless profiling is enabled.

// beginRegistryOp starts the profile op of a registry operation on ident (a
// ref or a blob digest): external work, bound on the registry.
func beginRegistryOp(ctx context.Context, class, ident string) (context.Context, *wcprof.Op) {
	if !wcprof.Enabled(ctx) {
		return ctx, nil
	}
	return wcprof.BeginOp(ctx, wcprof.OpKindIO, class, wcprof.OpOpts{Ident: ident, WorkType: wcprof.WorkTypeExternal})
}

// profFetcher is resolver's fetcher for ref, recording each blob it downloads
// when work under ctx is profiled.
func profFetcher(ctx context.Context, resolver remotes.Resolver, ref string) (remotes.Fetcher, error) {
	fetcher, err := resolver.Fetcher(ctx, ref)
	if err != nil || !wcprof.Enabled(ctx) {
		return fetcher, err
	}
	return profiledFetcher{Fetcher: fetcher}, nil
}

// profiledFetcher records each blob it fetches as an image.fetch op, from the
// request to the body's close, with the blob's digest and the bytes read. A
// fetcher is asked only for content the store doesn't have. Only a pull's
// fetches are recorded: a resolve's run in a flight other resolves of the
// ref may share, which can outlive the caller whose op they'd be under.
type profiledFetcher struct {
	remotes.Fetcher
}

func (f profiledFetcher) Fetch(ctx context.Context, desc ocispecs.Descriptor) (io.ReadCloser, error) {
	start := time.Now()
	body, err := f.Fetcher.Fetch(ctx, desc)
	if err != nil {
		wcprof.RecordOpAt(ctx, wcprof.OpKindIO, "image.fetch", fetchOpts(desc, 0), start, time.Now(), wcprof.OutcomeError)
		return nil, err
	}
	if _, ok := body.(io.ReaderAt); ok {
		// Reads at offsets bypass Read; record the request alone.
		wcprof.RecordOpAt(ctx, wcprof.OpKindIO, "image.fetch", fetchOpts(desc, 0), start, time.Now(), wcprof.OutcomeOK)
		return body, nil
	}
	profiled := &profiledBody{ReadCloser: body, ctx: ctx, desc: desc, start: start}
	if seeker, ok := body.(io.Seeker); ok {
		// A resumed download seeks past what the store has.
		return profiledSeekBody{profiledBody: profiled, seeker: seeker}, nil
	}
	return profiled, nil
}

func fetchOpts(desc ocispecs.Descriptor, read int64) wcprof.OpOpts {
	return wcprof.OpOpts{Ident: desc.Digest.String(), WorkType: wcprof.WorkTypeExternal, Bytes: read}
}

// profiledBody counts a fetched blob's bytes and keeps its read error, and
// records its fetch when it is closed.
type profiledBody struct {
	io.ReadCloser
	ctx   context.Context
	desc  ocispecs.Descriptor
	start time.Time
	read  int64
	err   error
	once  sync.Once
}

func (b *profiledBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read += int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		b.err = err
	}
	return n, err
}

func (b *profiledBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() {
		outcome := wcprof.OutcomeOK
		if b.err != nil || err != nil {
			outcome = wcprof.OutcomeError
		}
		wcprof.RecordOpAt(b.ctx, wcprof.OpKindIO, "image.fetch", fetchOpts(b.desc, b.read), b.start, time.Now(), outcome)
	})
	return err
}

// profiledSeekBody is a profiledBody over a body that seeks.
type profiledSeekBody struct {
	*profiledBody
	seeker io.Seeker
}

func (b profiledSeekBody) Seek(offset int64, whence int) (int64, error) {
	return b.seeker.Seek(offset, whence)
}
