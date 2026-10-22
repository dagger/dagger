package dagql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
)

const (
	// partStreamsEnv sets how many requests a large blob is read with at
	// once; 1 reads every blob on one stream.
	partStreamsEnv = "_EXPERIMENTAL_DAGGER_PART_STREAMS"
	// defaultPartStreams is the requests per large blob, the first included.
	defaultPartStreams = 4
	// partParallelMinBlob is the smallest blob read in parallel, and
	// partChunkSize the range each request reads.
	partParallelMinBlob = 32 << 20
	partChunkSize       = 8 << 20
	// maxPartExtraStreams bounds the requests beyond each blob's first,
	// across the engine. Each holds at most one chunk in memory.
	maxPartExtraStreams = 16
	// partChunkRetries is how many times a chunk is requested again after a
	// network error.
	partChunkRetries = 2
	// maxPartIdleConnsPerHost is the part transport's idle connections per
	// blob host.
	maxPartIdleConnsPerHost = 32
)

// partRangeConfig is how a content source reads large blobs. streams of 1
// disables parallel reads.
type partRangeConfig struct {
	streams int
	minBlob int64
	chunk   int64
	retries int
	// extra holds the streams beyond each blob's first, engine-wide.
	extra *semaphore.Weighted
}

var partStreamsSetting = sync.OnceValue(func() int {
	if n, err := strconv.Atoi(os.Getenv(partStreamsEnv)); err == nil && n >= 1 {
		return n
	}
	return defaultPartStreams
})

func defaultPartRangeConfig() partRangeConfig {
	return partRangeConfig{
		streams: partStreamsSetting(),
		minBlob: partParallelMinBlob,
		chunk:   partChunkSize,
		retries: partChunkRetries,
		extra:   semaphore.NewWeighted(maxPartExtraStreams),
	}
}

// get requests bytes start to end of the blob at url. The stream's context
// is canceled by its close, by ctx and by the idle bound.
func (p *partContentProvider) get(ctx context.Context, url string, start, end int64) (*partHTTPStream, *http.Response, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	if stats := p.stats; stats != nil {
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
			if !info.Reused {
				stats.connsNew.Add(1)
			}
		}})
	}
	stream := &partHTTPStream{ctx: ctx, cancel: cancel}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		stream.close()
		return nil, nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	var resp *http.Response
	_, err = stream.wait(func() (int, error) {
		sent := time.Now()
		var err error
		resp, err = p.client.Do(req) //nolint:bodyclose // The body is handed to the stream wrapper, which closes it in stream.close and its context AfterFunc.
		p.stats.request(sent)
		return 0, err
	})
	if err != nil {
		stream.close()
		return nil, nil, err
	}
	p.stats.status(resp.StatusCode)
	stream.body = resp.Body
	stream.closed = make(chan struct{})
	stream.stop = context.AfterFunc(ctx, func() {
		_ = resp.Body.Close()
		close(stream.closed)
	})
	return stream, resp, nil
}

// rangeReader returns a reader that reads desc in parallel ranged chunks, or
// nil when desc is read on one stream: below the size threshold, with
// parallel reads off, or with no spare stream in the engine.
func (p *partContentProvider) rangeReader(ctx context.Context, desc ocispecs.Descriptor, url string, renewed bool) *partRangeReader {
	if p.source == nil {
		return nil
	}
	cfg := p.source.ranges
	if cfg.streams <= 1 || cfg.extra == nil || desc.Size < cfg.minBlob || cfg.chunk <= 0 {
		return nil
	}
	extra := 0
	for extra < cfg.streams-1 && cfg.extra.TryAcquire(1) {
		extra++
	}
	if extra == 0 {
		return nil
	}
	readerCtx, cancel := context.WithCancelCause(ctx)
	workCtx, stop := context.WithCancelCause(readerCtx)
	r := &partRangeReader{
		provider: p,
		desc:     desc,
		parent:   ctx,
		ctx:      readerCtx,
		cancel:   cancel,
		workCtx:  workCtx,
		stop:     stop,
		chunk:    cfg.chunk,
		chunks:   (desc.Size + cfg.chunk - 1) / cfg.chunk,
		workers:  1 + extra,
		retries:  cfg.retries,
		url:      url,
		renewed:  renewed,
		held:     map[int64]*partRangeChunk{},
	}
	r.release = sync.OnceFunc(func() { cfg.extra.Release(int64(extra)) })
	r.cond = sync.NewCond(&r.mu)
	// Workers waiting for room in the window wake when the work ends.
	context.AfterFunc(workCtx, func() {
		r.mu.Lock()
		r.cond.Broadcast()
		r.mu.Unlock()
	})
	p.stats.usedStreams(r.workers)
	return r
}

// errRangeIgnored is a chunk answered with the whole object: the store
// ignores ranges.
var errRangeIgnored = errors.New("the blob store answered a range with the whole object")

// partRangeReader reads one large blob through several ranged requests at
// once. Its reader still sees the blob in order: workers fetch the chunks
// just ahead of the read offset into memory, at most one per worker, and a
// read waits for the chunk it needs. The writer, the digest check and the
// content commit see one ordered stream, as with partHTTPReader.
//
// A read outside the window of fetched chunks, or a store that answers a
// range with the whole object, moves the rest of the blob onto one stream.
type partRangeReader struct {
	provider *partContentProvider
	desc     ocispecs.Descriptor
	parent   context.Context
	// ctx is the reader's; workCtx the workers', canceled when they stop.
	ctx     context.Context
	cancel  context.CancelCauseFunc
	workCtx context.Context
	stop    context.CancelCauseFunc
	release func()
	chunk   int64
	chunks  int64
	workers int
	retries int
	wg      sync.WaitGroup

	// readMu serializes reads; it is never held while waiting on mu's state
	// to change.
	readMu sync.Mutex

	mu   sync.Mutex
	cond *sync.Cond
	// url is the blob's current address, and renewed whether renewal
	// supplied it.
	url     string
	renewed bool
	started bool
	// next is the next chunk to fetch, low the first chunk still held, and
	// held the chunks claimed and not yet read through.
	next, low int64
	held      map[int64]*partRangeChunk
	// alive counts the running workers. Once none runs, a chunk no worker
	// claimed fails rather than waiting.
	alive int
	// single, once set, reads the rest of the blob.
	single *partHTTPReader
}

type partRangeChunk struct {
	data []byte
	err  error
	// done is closed once data or err is set.
	done chan struct{}
}

func (r *partRangeReader) Size() int64 { return r.desc.Size }

// Close cancels every request and waits for the workers to stop.
func (r *partRangeReader) Close() error {
	r.cancel(nil)
	r.wg.Wait()
	r.release()
	r.mu.Lock()
	single := r.single
	r.mu.Unlock()
	if single != nil {
		return single.Close()
	}
	return nil
}

func (r *partRangeReader) ReadAt(p []byte, off int64) (int, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()
	if err := context.Cause(r.ctx); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 {
		return 0, fmt.Errorf("negative content offset")
	}
	size := r.desc.Size
	if off >= size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && off+int64(n) < size {
		pos := off + int64(n)
		c := r.chunkFor(pos / r.chunk)
		if c != nil {
			select {
			case <-c.done:
			case <-r.ctx.Done():
				return n, partContentError(r.parent, r.desc, "copy", context.Cause(r.ctx))
			}
			if c.err == nil {
				idx := pos / r.chunk
				m := copy(p[n:], c.data[pos-idx*r.chunk:])
				n += m
				if pos+int64(m) == min((idx+1)*r.chunk, size) {
					r.readThrough(idx)
				}
				continue
			}
			if !errors.Is(c.err, errRangeIgnored) {
				return n, c.err
			}
		}
		single := r.toSingle()
		m, err := single.ReadAt(p[n:], pos)
		return n + m, err
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// chunkFor returns chunk idx, starting the workers on the first read, or nil
// when the rest of the blob is read on one stream: idx lies outside the
// window, or that move already happened.
func (r *partRangeReader) chunkFor(idx int64) *partRangeChunk {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.single != nil || idx < r.low || idx >= r.low+int64(r.workers) {
		return nil
	}
	if !r.started {
		r.started = true
		r.alive = r.workers
		r.wg.Add(r.workers)
		for range r.workers {
			go r.work()
		}
	}
	c := r.chunkLocked(idx)
	if r.alive == 0 && idx >= r.next {
		r.failUnclaimedLocked()
	}
	return c
}

// failUnclaimedLocked fails every held chunk no worker claimed, once no
// worker is left to fetch it.
func (r *partRangeReader) failUnclaimedLocked() {
	err := context.Cause(r.workCtx)
	if err == nil {
		err = errors.New("content reads stopped after a failed chunk")
	}
	for idx, c := range r.held {
		if idx >= r.next {
			select {
			case <-c.done:
			default:
				c.err = r.chunkError("copy", err)
				close(c.done)
			}
		}
	}
}

func (r *partRangeReader) chunkLocked(idx int64) *partRangeChunk {
	c := r.held[idx]
	if c == nil {
		c = &partRangeChunk{done: make(chan struct{})}
		r.held[idx] = c
	}
	return c
}

// readThrough drops chunk idx, which the reader has read to its end, making
// room for a worker to fetch another.
func (r *partRangeReader) readThrough(idx int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.held, idx)
	r.low = idx + 1
	r.cond.Broadcast()
}

// toSingle stops the workers and returns the reader for the rest of the
// blob, on one stream from the current address.
func (r *partRangeReader) toSingle() *partHTTPReader {
	r.mu.Lock()
	if r.single != nil {
		single := r.single
		r.mu.Unlock()
		return single
	}
	r.mu.Unlock()
	r.stop(nil)
	r.wg.Wait()
	r.release()
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithCancelCause(r.ctx)
	r.single = &partHTTPReader{provider: r.provider, desc: r.desc, parent: r.parent, ctx: ctx, cancel: cancel, url: r.url, renewed: r.renewed}
	return r.single
}

// work fetches the next chunk in the window until the blob is fetched, the
// work stops, or a chunk fails.
func (r *partRangeReader) work() {
	defer r.wg.Done()
	defer func() {
		r.mu.Lock()
		r.alive--
		if r.alive == 0 {
			r.failUnclaimedLocked()
		}
		r.mu.Unlock()
	}()
	for {
		r.mu.Lock()
		for r.workCtx.Err() == nil && r.next < r.chunks && r.next >= r.low+int64(r.workers) {
			r.cond.Wait()
		}
		if r.workCtx.Err() != nil || r.next >= r.chunks {
			r.mu.Unlock()
			return
		}
		idx := r.next
		r.next++
		c := r.chunkLocked(idx)
		r.mu.Unlock()
		data, err := r.fetch(idx)
		c.data, c.err = data, err
		close(c.done)
		if err != nil {
			return
		}
	}
}

// fetch reads chunk idx. A network error requests the chunk again, up to
// the retry bound; a retry status renews the address as partHTTPReader does.
func (r *partRangeReader) fetch(idx int64) ([]byte, error) {
	start := idx * r.chunk
	end := min(start+r.chunk, r.desc.Size) - 1
	attempts := 0
	retry := func(err error) bool {
		if r.workCtx.Err() != nil || errors.Is(err, errPartContentIdle) || attempts >= r.retries {
			return false
		}
		attempts++
		return true
	}
	for {
		r.mu.Lock()
		url, renewed := r.url, r.renewed
		r.mu.Unlock()
		stream, resp, err := r.provider.get(r.workCtx, url, start, end) //nolint:bodyclose // The stream wrapper closes the body.
		if err != nil {
			if retry(err) {
				continue
			}
			return nil, r.chunkError("provider", err)
		}
		if _, renew := renewalRetryStatuses[resp.StatusCode]; renew {
			stream.close()
			err := fmt.Errorf("content address returned HTTP %d", resp.StatusCode)
			if renewed {
				return nil, r.chunkError("provider", err)
			}
			address, _, renewErr := r.provider.address(r.workCtx, r.desc.Digest, url)
			if renewErr != nil {
				return nil, r.chunkError("provider", errors.Join(err, renewErr))
			}
			r.mu.Lock()
			if r.url == url {
				r.url, r.renewed = address, true
			}
			r.mu.Unlock()
			continue
		}
		switch resp.StatusCode {
		case http.StatusPartialContent:
			want := fmt.Sprintf("bytes %d-%d/%d", start, end, r.desc.Size)
			if got := resp.Header.Get("Content-Range"); got != want {
				stream.close()
				return nil, r.chunkError("provider", fmt.Errorf("content range %q does not cover bytes %d-%d of %d", got, start, end, r.desc.Size))
			}
		case http.StatusOK:
			stream.close()
			return nil, errRangeIgnored
		default:
			stream.close()
			return nil, r.chunkError("provider", fmt.Errorf("content HTTP status %d", resp.StatusCode))
		}
		data := make([]byte, end-start+1)
		_, err = io.ReadFull(stream, data)
		stream.close()
		if err == nil {
			return data, nil
		}
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		if retry(err) {
			continue
		}
		return nil, r.chunkError("copy", err)
	}
}

// chunkError classifies a failed chunk as partHTTPReader's failures are:
// the reader's cancellation stays cancellation.
func (r *partRangeReader) chunkError(stage string, err error) error {
	if cause := context.Cause(r.ctx); cause != nil {
		return cause
	}
	return partContentError(r.parent, r.desc, stage, err)
}

// status counts one response's HTTP status. A nil stats counts nothing.
func (s *partInstallStats) status(code int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statuses == nil {
		s.statuses = map[int]int{}
	}
	s.statuses[code]++
}

// usedStreams notes a blob read with n requests at once. A nil stats counts
// nothing.
func (s *partInstallStats) usedStreams(n int) {
	if s == nil {
		return
	}
	for {
		current := s.streams.Load()
		if int64(n) <= current || s.streams.CompareAndSwap(current, int64(n)) {
			return
		}
	}
}

// httpStatuses summarizes the counted statuses: whether the store answered
// ranges, "206" when every answer to a request was partial content, "200"
// when any was the whole object, "off" when no request was answered with
// either; and each status's count, as "206=5,403=1".
func (s *partInstallStats) httpStatuses() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mode := "off"
	switch {
	case s.statuses[http.StatusOK] > 0:
		mode = "200"
	case s.statuses[http.StatusPartialContent] > 0:
		mode = "206"
	}
	codes := make([]int, 0, len(s.statuses))
	for code := range s.statuses {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	parts := make([]string, len(codes))
	for i, code := range codes {
		parts[i] = fmt.Sprintf("%d=%d", code, s.statuses[code])
	}
	return mode, strings.Join(parts, ",")
}
