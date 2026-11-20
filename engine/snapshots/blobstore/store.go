// Package blobstore is the engine's store of layer blobs: files named by
// their digest under one directory, which containerd's metadata store
// records and collects (core/metadata's contentStore wraps it).
//
// It writes nothing synchronously. The engine already runs its own
// databases without syncing and keeps no promise across a host crash
// (server.Server.GracefulStop explains why), and a sync made while
// containerd's metadata store holds its one write lock stalls every other
// write behind it on a busy disk. A blob a crash left short or wrong is
// caught where it is read: ReaderAt checks its size, unpacking checks its
// diff ID, and remote-cache uploads check its digest.
//
// A write is never resumed: reopening a write starts it over. Every engine
// writer writes a whole blob under its own ref, or under a ref named by the
// blob's digest that only one writer holds at a time.
//
// The layout is containerd's local store's (blobs/<algorithm>/<encoded>,
// and ingest/<sha256 of the ref>/ holding the write's ref and data), so an
// engine state written by that store reads the same.
//
// A store can also hold blobs it never wrote (WithImageBlobs): the engine
// image's own, read in place from the image's directory. Asked for one, it
// answers as it does for a blob it wrote, so containerd's metadata store
// records it as shared content with no copy; it never removes one, and Walk
// leaves them out, so containerd's collection never asks it to.
//
// containerd's records of those blobs outlive the image: an engine restarted
// on the same state with another image would find a record whose bytes went
// with the old one. ForgetLostImageBlobs removes such records at start.
package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// Store is a content.Store of blobs under root.
type Store struct {
	root string
	// image, when set, is a directory of blobs in the same layout that the
	// store reads in place and never writes.
	image string

	mu sync.Mutex
	// writing holds the refs with an open writer.
	writing map[string]*writer
}

var _ content.Store = (*Store)(nil)

// Option configures a Store.
type Option func(*Store)

// WithImageBlobs makes the store hold the blobs under dir, in its own
// layout, read in place: the engine image's blobs, which live as long as the
// engine. The store never writes or removes them. A dir that doesn't exist
// holds none.
func WithImageBlobs(dir string) Option {
	return func(s *Store) { s.image = dir }
}

// New returns the store under root, creating root when it doesn't exist.
func New(root string, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "ingest"), 0o755); err != nil {
		return nil, err
	}
	s := &Store{root: root, writing: map[string]*writer{}}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// imageBlobPath is where the image directory would hold dgst, which
// blobPath has validated; "" when the store has no image directory.
func (s *Store) imageBlobPath(dgst digest.Digest) string {
	if s.image == "" {
		return ""
	}
	return filepath.Join(s.image, "blobs", dgst.Algorithm().String(), dgst.Encoded())
}

// InPlace reports whether the store holds dgst only in place, as one of the
// image's blobs, which takes none of the store's own disk.
func (s *Store) InPlace(dgst digest.Digest) bool {
	p, err := s.blobPath(dgst)
	if err != nil || s.image == "" {
		return false
	}
	if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	_, err = os.Stat(s.imageBlobPath(dgst))
	return err == nil
}

// imageBlobsFile records, under the store's root, the image blobs the store
// held in place when it last started: one digest a line.
const imageBlobsFile = "image-blobs"

// ForgetLostImageBlobs removes from cs, containerd's metadata store over this
// store, the records of the image blobs the store held in place when it last
// started and holds no longer, then records the ones it holds now. A record
// is the only thing that says a blob is present, so whatever needs a lost
// blob makes it again, as for any blob that is gone. Call it at start, before
// anything reads the store, with ctx in cs's namespace.
func (s *Store) ForgetLostImageBlobs(ctx context.Context, cs content.Manager) error {
	held, err := s.imageBlobs()
	if err != nil {
		return err
	}
	recorded, err := os.ReadFile(filepath.Join(s.root, imageBlobsFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if string(recorded) == strings.Join(held, "\n") {
		return nil
	}
	now := make(map[string]bool, len(held))
	for _, d := range held {
		now[d] = true
	}
	for line := range strings.SplitSeq(string(recorded), "\n") {
		dgst := digest.Digest(line)
		if line == "" || now[line] || dgst.Validate() != nil {
			continue
		}
		if p, _ := s.blobPath(dgst); p != "" {
			if _, err := os.Stat(p); err == nil {
				continue // the store wrote its own copy
			}
		}
		if err := cs.Delete(ctx, dgst); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("forget lost image blob %s: %w", dgst, err)
		}
	}
	tmp := filepath.Join(s.root, imageBlobsFile+".tmp")
	if err := os.WriteFile(tmp, []byte(strings.Join(held, "\n")), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.root, imageBlobsFile))
}

// imageBlobs lists the digests of the image's blobs, sorted.
func (s *Store) imageBlobs() ([]string, error) {
	if s.image == "" {
		return nil, nil
	}
	var out []string
	dir := filepath.Join(s.image, "blobs")
	algs, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, alg := range algs {
		entries, err := os.ReadDir(filepath.Join(dir, alg.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			dgst := digest.NewDigestFromEncoded(digest.Algorithm(alg.Name()), e.Name())
			if dgst.Validate() == nil {
				out = append(out, dgst.String())
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// open opens dgst's blob, the store's own or else the image's.
func (s *Store) open(dgst digest.Digest) (*os.File, error) {
	p, err := s.blobPath(dgst)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		if ip := s.imageBlobPath(dgst); ip != "" {
			if imageFile, imageErr := os.Open(ip); imageErr == nil {
				return imageFile, nil
			}
		}
	}
	return f, err
}

func (s *Store) blobPath(dgst digest.Digest) (string, error) {
	if err := dgst.Validate(); err != nil {
		return "", fmt.Errorf("%s: %w", dgst, errdefs.ErrInvalidArgument)
	}
	return filepath.Join(s.root, "blobs", dgst.Algorithm().String(), dgst.Encoded()), nil
}

func (s *Store) ingestPath(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return filepath.Join(s.root, "ingest", hex.EncodeToString(sum[:]))
}

// Info returns the blob's size; its times are its file's modification time.
func (s *Store) Info(_ context.Context, dgst digest.Digest) (content.Info, error) {
	p, err := s.blobPath(dgst)
	if err != nil {
		return content.Info{}, err
	}
	fi, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		if ip := s.imageBlobPath(dgst); ip != "" {
			if imageInfo, imageErr := os.Stat(ip); imageErr == nil {
				return blobInfo(dgst, imageInfo), nil
			}
		}
	}
	if err != nil {
		return content.Info{}, notFound(err, "content %s", dgst)
	}
	return blobInfo(dgst, fi), nil
}

func blobInfo(dgst digest.Digest, fi fs.FileInfo) content.Info {
	return content.Info{Digest: dgst, Size: fi.Size(), CreatedAt: fi.ModTime(), UpdatedAt: fi.ModTime()}
}

// Update is not supported: containerd's metadata store keeps the labels.
func (s *Store) Update(context.Context, content.Info, ...string) (content.Info, error) {
	return content.Info{}, fmt.Errorf("blob store labels: %w", errdefs.ErrNotImplemented)
}

// Walk calls fn for every blob the store wrote, not the image's: containerd's
// metadata store walks this store only to collect it. It takes no filters,
// since that store filters its own records.
func (s *Store) Walk(ctx context.Context, fn content.WalkFunc, filters ...string) error {
	if len(filters) > 0 {
		return fmt.Errorf("blob store walk filters: %w", errdefs.ErrNotImplemented)
	}
	root := filepath.Join(s.root, "blobs")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		alg, encoded, ok := strings.Cut(filepath.ToSlash(rel), "/")
		if !ok {
			return nil
		}
		dgst := digest.NewDigestFromEncoded(digest.Algorithm(alg), encoded)
		if isBlob := dgst.Validate() == nil; !isBlob {
			// A file not named by a digest isn't a blob.
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		return fn(blobInfo(dgst, fi))
	})
	return err
}

// Delete removes a blob the store wrote; the image's blobs are not its to
// remove.
func (s *Store) Delete(_ context.Context, dgst digest.Digest) error {
	p, err := s.blobPath(dgst)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		return notFound(err, "content %s", dgst)
	}
	return nil
}

// ReaderAt opens the blob desc names. It fails when the stored blob isn't
// desc's size, as a host crash can leave it.
func (s *Store) ReaderAt(_ context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	f, err := s.open(desc.Digest)
	if err != nil {
		return nil, notFound(err, "content %s", desc.Digest)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if desc.Size > 0 && fi.Size() != desc.Size {
		f.Close()
		return nil, fmt.Errorf("content %s: stored with %d bytes, expected %d: %w", desc.Digest, fi.Size(), desc.Size, errdefs.ErrDataLoss)
	}
	return &readerAt{File: f, size: fi.Size()}, nil
}

type readerAt struct {
	*os.File
	size int64
}

func (r *readerAt) Size() int64 { return r.size }

// Status reports the write under ref: its offset is what is written so far.
func (s *Store) Status(_ context.Context, ref string) (content.Status, error) {
	s.mu.Lock()
	w := s.writing[ref]
	s.mu.Unlock()
	if w != nil {
		return w.Status()
	}
	return s.diskStatus(ref, s.ingestPath(ref))
}

// diskStatus is the status of a write no writer holds, from its directory.
func (s *Store) diskStatus(ref, dir string) (content.Status, error) {
	fi, err := os.Stat(filepath.Join(dir, "data"))
	if err != nil {
		return content.Status{}, notFound(err, "ingest %s", ref)
	}
	return content.Status{Ref: ref, Offset: fi.Size(), StartedAt: fi.ModTime(), UpdatedAt: fi.ModTime()}, nil
}

// ListStatuses lists every write. It takes no filters, as Walk.
func (s *Store) ListStatuses(ctx context.Context, filters ...string) ([]content.Status, error) {
	if len(filters) > 0 {
		return nil, fmt.Errorf("blob store status filters: %w", errdefs.ErrNotImplemented)
	}
	var statuses []content.Status
	err := s.WalkStatusRefs(ctx, func(ref string) error {
		st, err := s.Status(ctx, ref)
		if errdefs.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		statuses = append(statuses, st)
		return nil
	})
	return statuses, err
}

// WalkStatusRefs calls fn with the ref of every write, open or left behind.
// containerd's metadata store aborts those it has no record of.
func (s *Store) WalkStatusRefs(ctx context.Context, fn func(string) error) error {
	entries, err := os.ReadDir(filepath.Join(s.root, "ingest"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		ref, err := os.ReadFile(filepath.Join(s.root, "ingest", e.Name(), "ref"))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		if err := fn(string(ref)); err != nil {
			return err
		}
	}
	return nil
}

// Abort removes the write under ref. A writer that still holds it fails
// when it commits.
func (s *Store) Abort(_ context.Context, ref string) error {
	dir := s.ingestPath(ref)
	if _, err := os.Stat(dir); err != nil {
		return notFound(err, "ingest %s", ref)
	}
	return os.RemoveAll(dir)
}

// Writer starts a write under the ref opts name. Only one writer holds a
// ref: another fails as unavailable until it closes, which
// content.OpenWriter waits out. A blob already stored under the descriptor's
// digest fails as already existing.
func (s *Store) Writer(_ context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	var wOpts content.WriterOpts
	for _, opt := range opts {
		if err := opt(&wOpts); err != nil {
			return nil, err
		}
	}
	if wOpts.Ref == "" {
		return nil, fmt.Errorf("blob store writer: no ref: %w", errdefs.ErrInvalidArgument)
	}
	desc := wOpts.Desc
	if desc.Digest != "" {
		p, err := s.blobPath(desc.Digest)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(p); err == nil {
			return nil, fmt.Errorf("content %s: %w", desc.Digest, errdefs.ErrAlreadyExists)
		}
	}
	s.mu.Lock()
	if s.writing[wOpts.Ref] != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("ref %s is being written: %w", wOpts.Ref, errdefs.ErrUnavailable)
	}
	w := &writer{s: s, ref: wOpts.Ref, dir: s.ingestPath(wOpts.Ref), total: desc.Size, expected: desc.Digest, digester: digest.Canonical.Digester()}
	s.writing[wOpts.Ref] = w
	s.mu.Unlock()
	if err := w.open(); err != nil {
		s.release(w)
		return nil, err
	}
	return w, nil
}

func (s *Store) release(w *writer) {
	s.mu.Lock()
	if s.writing[w.ref] == w {
		delete(s.writing, w.ref)
	}
	s.mu.Unlock()
}

// notFound turns a missing file's error into errdefs' not found.
func notFound(err error, format string, args ...any) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf(format+": %w", append(args, errdefs.ErrNotFound)...)
	}
	return err
}

type writer struct {
	s        *Store
	ref      string
	dir      string
	total    int64
	expected digest.Digest

	fp        *os.File
	digester  digest.Digester
	offset    int64
	startedAt time.Time
	updatedAt time.Time
}

// open starts the write over, in a directory of its own.
func (w *writer) open() error {
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(w.dir, "ref"), []byte(w.ref), 0o644); err != nil {
		return err
	}
	fp, err := os.OpenFile(filepath.Join(w.dir, "data"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w.fp = fp
	w.startedAt = time.Now()
	w.updatedAt = w.startedAt
	return nil
}

func (w *writer) Write(p []byte) (int, error) {
	if w.fp == nil {
		return 0, fmt.Errorf("write to %s after close: %w", w.ref, errdefs.ErrFailedPrecondition)
	}
	n, err := w.fp.Write(p)
	w.digester.Hash().Write(p[:n])
	w.offset += int64(n)
	w.updatedAt = time.Now()
	return n, err
}

func (w *writer) Digest() digest.Digest { return w.digester.Digest() }

func (w *writer) Status() (content.Status, error) {
	return content.Status{Ref: w.ref, Offset: w.offset, Total: w.total, Expected: w.expected, StartedAt: w.startedAt, UpdatedAt: w.updatedAt}, nil
}

// Truncate starts the write over; only a size of 0 is supported.
func (w *writer) Truncate(size int64) error {
	if size != 0 {
		return fmt.Errorf("truncate %s to %d: %w", w.ref, size, errdefs.ErrNotImplemented)
	}
	if w.fp == nil {
		return fmt.Errorf("truncate %s after close: %w", w.ref, errdefs.ErrFailedPrecondition)
	}
	if err := w.fp.Truncate(0); err != nil {
		return err
	}
	if _, err := w.fp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	w.digester = digest.Canonical.Digester()
	w.offset = 0
	return nil
}

// Commit checks the write's size and digest and stores it as the blob its
// digest names. A blob already stored under it leaves the stored one and
// fails as already existing. The write ends either way.
func (w *writer) Commit(_ context.Context, size int64, expected digest.Digest, _ ...content.Opt) error {
	defer w.s.release(w)
	fp := w.fp
	if fp == nil {
		return fmt.Errorf("commit %s after close: %w", w.ref, errdefs.ErrFailedPrecondition)
	}
	w.fp = nil
	if err := fp.Close(); err != nil {
		return err
	}
	if size > 0 && size != w.offset {
		return fmt.Errorf("commit %s: wrote %d bytes, expected %d: %w", w.ref, w.offset, size, errdefs.ErrFailedPrecondition)
	}
	dgst := w.digester.Digest()
	if expected != "" && expected != dgst {
		return fmt.Errorf("commit %s: wrote %s, expected %s: %w", w.ref, dgst, expected, errdefs.ErrFailedPrecondition)
	}
	target, err := w.s.blobPath(dgst)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		_ = os.RemoveAll(w.dir)
		return fmt.Errorf("content %s: %w", dgst, errdefs.ErrAlreadyExists)
	}
	if err := os.Rename(filepath.Join(w.dir, "data"), target); err != nil {
		return err
	}
	return os.RemoveAll(w.dir)
}

// Close ends the write without storing it; what it wrote stays for Abort.
func (w *writer) Close() error {
	defer w.s.release(w)
	if w.fp == nil {
		return nil
	}
	err := w.fp.Close()
	w.fp = nil
	return err
}
