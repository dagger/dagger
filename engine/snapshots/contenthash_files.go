package snapshots

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/pkg/errors"
)

// contentHashFiles keeps the serialized content-hash trees of snapshots
// (ContentHashMetadataKey) in files, one per snapshot, instead of in memory.
//
// A tree is about a hundred bytes per file of its snapshot, and every snapshot
// hashed has a whole one: each edit to a checkout of thousands of files makes
// a snapshot that is hashed from scratch, so in memory the trees grew with
// edits × files. They are read only when a snapshot's tree is not among the
// few the contenthash package keeps parsed, and at a checkpoint.
//
// The files are a cache of this process: the dagql checkpoint is what keeps
// trees across restarts, and the directory is emptied when the manager is
// made. They hold the same bytes the trees held in memory, so neither the
// checkpoint nor its format change.
type contentHashFiles struct {
	dir string

	mu     sync.Mutex
	ids    map[string]struct{} // snapshots with a tree on disk
	closed bool
}

func newContentHashFiles(dir string) (*contentHashFiles, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, errors.Wrap(err, "clear content hash directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errors.Wrap(err, "create content hash directory")
	}
	return &contentHashFiles{dir: dir, ids: make(map[string]struct{})}, nil
}

func (f *contentHashFiles) path(id string) string {
	return filepath.Join(f.dir, id)
}

// write stores id's tree. A tree written for metadata that was dropped
// meanwhile (gone) is discarded, so a snapshot's tree never outlives it.
func (f *contentHashFiles) write(id string, dt []byte, gone func() bool) error {
	tmp, err := os.CreateTemp(f.dir, ".tmp-"+id+"-")
	if err != nil {
		return errors.Wrap(err, "create content hash file")
	}
	tmpPath := tmp.Name()
	_, err = tmp.Write(dt)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmpPath)
		return errors.Wrap(err, "write content hash file")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || (gone != nil && gone()) {
		os.Remove(tmpPath)
		return nil
	}
	if err := os.Rename(tmpPath, f.path(id)); err != nil {
		os.Remove(tmpPath)
		return errors.Wrap(err, "store content hash file")
	}
	f.ids[id] = struct{}{}
	return nil
}

// read returns id's tree, or ok false when it has none, or when the metadata
// reading it was dropped (gone).
func (f *contentHashFiles) read(id string, gone func() bool) (_ []byte, ok bool, _ error) {
	f.mu.Lock()
	_, ok = f.ids[id]
	if gone != nil && gone() {
		ok = false
	}
	f.mu.Unlock()
	if !ok {
		return nil, false, nil
	}
	dt, err := os.ReadFile(f.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			// Removed since.
			return nil, false, nil
		}
		return nil, false, errors.Wrap(err, "read content hash file")
	}
	return dt, true, nil
}

func (f *contentHashFiles) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.ids[id]
	return ok
}

// remove drops id's tree. markGone runs under the same lock as write's check,
// so a write racing the removal is discarded.
func (f *contentHashFiles) remove(id string, markGone func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if markGone != nil {
		markGone()
	}
	if _, ok := f.ids[id]; !ok {
		return
	}
	delete(f.ids, id)
	os.Remove(f.path(id))
}

func (f *contentHashFiles) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(f.ids))
	for id := range f.ids {
		ids = append(ids, id)
	}
	return ids
}

// close discards later writes. The files stay until the next manager on the
// directory empties it.
func (f *contentHashFiles) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}
