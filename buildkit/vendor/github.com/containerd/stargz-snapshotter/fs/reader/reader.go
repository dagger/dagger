/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

/*
   Copyright 2019 The Go Authors. All rights reserved.
   Use of this source code is governed by a BSD-style
   license that can be found in the NOTICE.md file.
*/

package reader

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/containerd/stargz-snapshotter/cache"
	"github.com/containerd/stargz-snapshotter/estargz"
	commonmetrics "github.com/containerd/stargz-snapshotter/fs/metrics/common"
	"github.com/containerd/stargz-snapshotter/metadata"
	"github.com/hashicorp/go-multierror"
	digest "github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const maxWalkDepth = 10000

type Reader interface {
	OpenFile(id uint32) (io.ReaderAt, error)
	Metadata() metadata.Reader
	Close() error
	LastOnDemandReadTime() time.Time
}

// VerifiableReader produces a Reader with a given verifier.
type VerifiableReader struct {
	r *reader

	lastVerifyErr           error
	lastVerifyErrMu         sync.Mutex
	prohibitVerifyFailure   bool
	prohibitVerifyFailureMu sync.RWMutex

	closed   bool
	closedMu sync.Mutex

	verifier func(uint32, string) (digest.Verifier, error)
}

func (vr *VerifiableReader) storeLastVerifyErr(err error) {
	vr.lastVerifyErrMu.Lock()
	vr.lastVerifyErr = err
	vr.lastVerifyErrMu.Unlock()
}

func (vr *VerifiableReader) loadLastVerifyErr() error {
	vr.lastVerifyErrMu.Lock()
	err := vr.lastVerifyErr
	vr.lastVerifyErrMu.Unlock()
	return err
}

func (vr *VerifiableReader) SkipVerify() Reader {
	return vr.r
}

func (vr *VerifiableReader) VerifyTOC(tocDigest digest.Digest) (Reader, error) {
	if vr.isClosed() {
		return nil, fmt.Errorf("reader is already closed")
	}
	vr.prohibitVerifyFailureMu.Lock()
	vr.prohibitVerifyFailure = true
	lastVerifyErr := vr.loadLastVerifyErr()
	vr.prohibitVerifyFailureMu.Unlock()
	if err := lastVerifyErr; err != nil {
		return nil, fmt.Errorf("content error occurs during caching contents: %w", err)
	}
	if actual := vr.r.r.TOCDigest(); actual != tocDigest {
		return nil, fmt.Errorf("invalid TOC JSON %q; want %q", actual, tocDigest)
	}
	vr.r.verify = true
	return vr.r, nil
}

func (vr *VerifiableReader) Metadata() metadata.Reader {
	// TODO: this shouldn't be called before verified
	return vr.r.r
}

func (vr *VerifiableReader) Cache(opts ...CacheOption) (err error) {
	if vr.isClosed() {
		return fmt.Errorf("reader is already closed")
	}

	var cacheOpts cacheOptions
	for _, o := range opts {
		o(&cacheOpts)
	}

	gr := vr.r
	r := gr.r
	if cacheOpts.reader != nil {
		r, err = r.Clone(cacheOpts.reader)
		if err != nil {
			return err
		}
	}
	rootID := r.RootID()

	filter := func(int64) bool {
		return true
	}
	if cacheOpts.filter != nil {
		filter = cacheOpts.filter
	}

	eg, egCtx := errgroup.WithContext(context.Background())
	eg.Go(func() error {
		return vr.cacheWithReader(egCtx,
			0, eg, semaphore.NewWeighted(int64(runtime.GOMAXPROCS(0))),
			rootID, r, filter, cacheOpts.cacheOpts...)
	})
	return eg.Wait()
}

func (vr *VerifiableReader) cacheWithReader(ctx context.Context, currentDepth int, eg *errgroup.Group, sem *semaphore.Weighted, dirID uint32, r metadata.Reader, filter func(int64) bool, opts ...cache.Option) (rErr error) {
	if currentDepth > maxWalkDepth {
		return fmt.Errorf("tree is too deep (depth:%d)", currentDepth)
	}
	rootID := r.RootID()
	r.ForeachChild(dirID, func(name string, id uint32, mode os.FileMode) bool {
		e, err := r.GetAttr(id)
		if err != nil {
			rErr = err
			return false
		}
		if mode.IsDir() {
			// Walk through all files on this stargz file.

			// Ignore the entry of "./" (formated as "" by stargz lib) on root directory
			// because this points to the root directory itself.
			if dirID == rootID && name == "" {
				return true
			}

			if err := vr.cacheWithReader(ctx, currentDepth+1, eg, sem, id, r, filter, opts...); err != nil {
				rErr = err
				return false
			}
			return true
		} else if !mode.IsRegular() {
			// Only cache regular files
			return true
		} else if dirID == rootID && name == estargz.TOCTarName {
			// We don't need to cache TOC json file
			return true
		}

		offset, err := r.GetOffset(id)
		if err != nil {
			rErr = err
			return false
		}
		if !filter(offset) {
			// This entry need to be filtered out
			return true
		}

		fr, err := r.OpenFileWithPreReader(id, func(nid uint32, chunkOffset, chunkSize int64, chunkDigest string, r io.Reader) (retErr error) {
			return vr.readAndCache(nid, r, chunkOffset, chunkSize, chunkDigest, opts...)
		})
		if err != nil {
			rErr = err
			return false
		}

		var nr int64
		for nr < e.Size {
			chunkOffset, chunkSize, chunkDigestStr, ok := fr.ChunkEntryForOffset(nr)
			if !ok {
				break
			}
			nr += chunkSize

			if err := sem.Acquire(ctx, 1); err != nil {
				rErr = err
				return false
			}

			eg.Go(func() error {
				defer sem.Release(1)
				err := vr.readAndCache(id, io.NewSectionReader(fr, chunkOffset, chunkSize), chunkOffset, chunkSize, chunkDigestStr, opts...)
				if err != nil {
					return fmt.Errorf("failed to read %q (off:%d,size:%d): %w", name, chunkOffset, chunkSize, err)
				}
				return nil
			})
		}

		return true
	})

	return
}

func (vr *VerifiableReader) readAndCache(id uint32, fr io.Reader, chunkOffset, chunkSize int64, chunkDigest string, opts ...cache.Option) (retErr error) {
	gr := vr.r

	if retErr != nil {
		vr.storeLastVerifyErr(retErr)
	}

	// Check if it already exists in the cache
	cacheID := genID(id, chunkOffset, chunkSize)
	if r, err := gr.cache.Get(cacheID); err == nil {
		r.Close()
		return nil
	}

	// missed cache, needs to fetch and add it to the cache
	br := bufio.NewReaderSize(fr, int(chunkSize))
	if _, err := br.Peek(int(chunkSize)); err != nil {
		return fmt.Errorf("cacheWithReader.peek: %v", err)
	}
	w, err := gr.cache.Add(cacheID, opts...)
	if err != nil {
		return err
	}
	defer w.Close()
	v, err := vr.verifier(id, chunkDigest)
	if err != nil {
		vr.prohibitVerifyFailureMu.RLock()
		if vr.prohibitVerifyFailure {
			vr.prohibitVerifyFailureMu.RUnlock()
			return fmt.Errorf("verifier not found: %w", err)
		}
		vr.storeLastVerifyErr(err)
		vr.prohibitVerifyFailureMu.RUnlock()
	}
	tee := io.Discard
	if v != nil {
		tee = io.Writer(v) // verification is required
	}
	if _, err := io.CopyN(w, io.TeeReader(br, tee), chunkSize); err != nil {
		w.Abort()
		return fmt.Errorf("failed to cache file payload: %w", err)
	}
	if v != nil && !v.Verified() {
		err := fmt.Errorf("invalid chunk")
		vr.prohibitVerifyFailureMu.RLock()
		if vr.prohibitVerifyFailure {
			vr.prohibitVerifyFailureMu.RUnlock()
			w.Abort()
			return err
		}
		vr.storeLastVerifyErr(err)
		vr.prohibitVerifyFailureMu.RUnlock()
	}

	return w.Commit()
}

func (vr *VerifiableReader) Close() error {
	vr.closedMu.Lock()
	defer vr.closedMu.Unlock()
	if vr.closed {
		return nil
	}
	vr.closed = true
	return vr.r.Close()
}

func (vr *VerifiableReader) isClosed() bool {
	vr.closedMu.Lock()
	closed := vr.closed
	vr.closedMu.Unlock()
	return closed
}

// NewReader creates a Reader based on the given stargz blob and cache implementation.
// It returns VerifiableReader so the caller must provide a metadata.ChunkVerifier
// to use for verifying file or chunk contained in this stargz blob.
func NewReader(r metadata.Reader, cache cache.BlobCache, layerSha digest.Digest) (*VerifiableReader, error) {
	vr := &reader{
		r:     r,
		cache: cache,
		bufPool: sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
		layerSha: layerSha,
		verifier: digestVerifier,
	}
	return &VerifiableReader{r: vr, verifier: digestVerifier}, nil
}

type reader struct {
	r       metadata.Reader
	cache   cache.BlobCache
	bufPool sync.Pool

	layerSha digest.Digest

	lastReadTime   time.Time
	lastReadTimeMu sync.Mutex

	closed   bool
	closedMu sync.Mutex

	verify   bool
	verifier func(uint32, string) (digest.Verifier, error)
}

func (gr *reader) Metadata() metadata.Reader {
	return gr.r
}

func (gr *reader) setLastReadTime(lastReadTime time.Time) {
	gr.lastReadTimeMu.Lock()
	gr.lastReadTime = lastReadTime
	gr.lastReadTimeMu.Unlock()
}

func (gr *reader) LastOnDemandReadTime() time.Time {
	gr.lastReadTimeMu.Lock()
	t := gr.lastReadTime
	gr.lastReadTimeMu.Unlock()
	return t
}

func (gr *reader) OpenFile(id uint32) (io.ReaderAt, error) {
	if gr.isClosed() {
		return nil, fmt.Errorf("reader is already closed")
	}
	var fr metadata.File
	fr, err := gr.r.OpenFileWithPreReader(id, func(nid uint32, chunkOffset, chunkSize int64, chunkDigest string, r io.Reader) error {
		// Check if it already exists in the cache
		cacheID := genID(nid, chunkOffset, chunkSize)
		if r, err := gr.cache.Get(cacheID); err == nil {
			r.Close()
			return nil
		}

		// Read and cache
		b := gr.bufPool.Get().(*bytes.Buffer)
		b.Reset()
		b.Grow(int(chunkSize))
		ip := b.Bytes()[:chunkSize]
		if _, err := io.ReadFull(r, ip); err != nil {
			gr.putBuffer(b)
			return err
		}
		err := gr.verifyAndCache(nid, ip, chunkDigest, cacheID)
		gr.putBuffer(b)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open file %d: %w", id, err)
	}
	return &file{
		id: id,
		fr: fr,
		gr: gr,
	}, nil
}

func (gr *reader) Close() (retErr error) {
	gr.closedMu.Lock()
	defer gr.closedMu.Unlock()
	if gr.closed {
		return nil
	}
	gr.closed = true
	if err := gr.cache.Close(); err != nil {
		retErr = multierror.Append(retErr, err)
	}
	if err := gr.r.Close(); err != nil {
		retErr = multierror.Append(retErr, err)
	}
	return
}

func (gr *reader) isClosed() bool {
	gr.closedMu.Lock()
	closed := gr.closed
	gr.closedMu.Unlock()
	return closed
}

func (gr *reader) putBuffer(b *bytes.Buffer) {
	b.Reset()
	gr.bufPool.Put(b)
}

type file struct {
	id uint32
	fr metadata.File
	gr *reader
}

// ReadAt reads chunks from the stargz file with trying to fetch as many chunks
// as possible from the cache.
func (sf *file) ReadAt(p []byte, offset int64) (int, error) {
	nr := 0
	for nr < len(p) {
		chunkOffset, chunkSize, chunkDigestStr, ok := sf.fr.ChunkEntryForOffset(offset + int64(nr))
		if !ok {
			break
		}
		var (
			id           = genID(sf.id, chunkOffset, chunkSize)
			lowerDiscard = positive(offset - chunkOffset)
			upperDiscard = positive(chunkOffset + chunkSize - (offset + int64(len(p))))
			expectedSize = chunkSize - upperDiscard - lowerDiscard
		)

		// Check if the content exists in the cache
		if r, err := sf.gr.cache.Get(id); err == nil {
			n, err := r.ReadAt(p[nr:int64(nr)+expectedSize], lowerDiscard)
			if (err == nil || err == io.EOF) && int64(n) == expectedSize {
				nr += n
				r.Close()
				continue
			}
			r.Close()
		}

		// We missed cache. Take it from underlying reader.
		// We read the whole chunk here and add it to the cache so that following
		// reads against neighboring chunks can take the data without decmpression.
		if lowerDiscard == 0 && upperDiscard == 0 {
			// We can directly store the result to the given buffer
			ip := p[nr : int64(nr)+chunkSize]
			n, err := sf.fr.ReadAt(ip, chunkOffset)
			if err != nil && err != io.EOF {
				return 0, fmt.Errorf("failed to read data: %w", err)
			}
			if err := sf.gr.verifyAndCache(sf.id, ip, chunkDigestStr, id); err != nil {
				return 0, err
			}
			nr += n
			continue
		}

		// Use temporally buffer for aligning this chunk
		b := sf.gr.bufPool.Get().(*bytes.Buffer)
		b.Reset()
		b.Grow(int(chunkSize))
		ip := b.Bytes()[:chunkSize]
		if _, err := sf.fr.ReadAt(ip, chunkOffset); err != nil && err != io.EOF {
			sf.gr.putBuffer(b)
			return 0, fmt.Errorf("failed to read data: %w", err)
		}
		if err := sf.gr.verifyAndCache(sf.id, ip, chunkDigestStr, id); err != nil {
			sf.gr.putBuffer(b)
			return 0, err
		}
		n := copy(p[nr:], ip[lowerDiscard:chunkSize-upperDiscard])
		sf.gr.putBuffer(b)
		if int64(n) != expectedSize {
			return 0, fmt.Errorf("unexpected final data size %d; want %d", n, expectedSize)
		}
		nr += n
	}

	commonmetrics.AddBytesCount(commonmetrics.OnDemandBytesServed, sf.gr.layerSha, int64(nr)) // measure the number of on demand bytes served

	return nr, nil
}

func (gr *reader) verifyAndCache(entryID uint32, ip []byte, chunkDigestStr string, cacheID string) error {
	// We can end up doing on demand registry fetch when aligning the chunk
	commonmetrics.IncOperationCount(commonmetrics.OnDemandRemoteRegistryFetchCount, gr.layerSha) // increment the number of on demand file fetches from remote registry
	commonmetrics.AddBytesCount(commonmetrics.OnDemandBytesFetched, gr.layerSha, int64(len(ip))) // record total bytes fetched
	gr.setLastReadTime(time.Now())

	// Verify this chunk
	if err := gr.verifyChunk(entryID, ip, chunkDigestStr); err != nil {
		return fmt.Errorf("invalid chunk: %w", err)
	}

	// Cache this chunk
	if w, err := gr.cache.Add(cacheID); err == nil {
		if cn, err := w.Write(ip); err != nil || cn != len(ip) {
			w.Abort()
		} else {
			w.Commit()
		}
		w.Close()
	}

	return nil
}

func (gr *reader) verifyChunk(id uint32, p []byte, chunkDigestStr string) error {
	if !gr.verify {
		return nil // verification is not required
	}
	v, err := gr.verifier(id, chunkDigestStr)
	if err != nil {
		return fmt.Errorf("invalid chunk: %w", err)
	}
	if _, err := v.Write(p); err != nil {
		return fmt.Errorf("invalid chunk: failed to write to verifier: %w", err)
	}
	if !v.Verified() {
		return fmt.Errorf("invalid chunk: not verified")
	}

	return nil
}

func genID(id uint32, offset, size int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d-%d-%d", id, offset, size)))
	return fmt.Sprintf("%x", sum)
}

func positive(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

type CacheOption func(*cacheOptions)

type cacheOptions struct {
	cacheOpts []cache.Option
	filter    func(int64) bool
	reader    *io.SectionReader
}

func WithCacheOpts(cacheOpts ...cache.Option) CacheOption {
	return func(opts *cacheOptions) {
		opts.cacheOpts = cacheOpts
	}
}

func WithFilter(filter func(int64) bool) CacheOption {
	return func(opts *cacheOptions) {
		opts.filter = filter
	}
}

func WithReader(sr *io.SectionReader) CacheOption {
	return func(opts *cacheOptions) {
		opts.reader = sr
	}
}

func digestVerifier(id uint32, chunkDigestStr string) (digest.Verifier, error) {
	chunkDigest, err := digest.Parse(chunkDigestStr)
	if err != nil {
		return nil, fmt.Errorf("invalid chunk: no digest is recorded(len=%d): %w", len(chunkDigestStr), err)
	}
	return chunkDigest.Verifier(), nil
}
