package snapshots

import (
	"bytes"
	stderrors "errors"
	"io"
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
// A tree that can't be written is kept in memory instead (mem), where trees
// were kept before, until a later save of it can write it.
//
// mu covers where each tree is: publishing one (renaming its file into place
// and dropping it from memory, or keeping it in memory and deleting its file)
// and looking one up (taking it from memory, or opening its file). Writing a
// tree's temporary file and reading an opened one happen outside it. So a
// tree is in one place at a time, and a lookup finds it there whatever saves
// run meanwhile.
//
// The files are a cache of this process: the dagql checkpoint is what keeps
// trees across restarts. Each manager keeps its trees in a fresh directory of
// its own under the root it is given, so it starts without any, and it never
// deletes anything it did not write: what earlier processes left under the
// root is for the caller to sweep (the engine moves the root to its trash at
// startup). The files hold the same bytes the trees held in memory, so
// neither the checkpoint nor its format change.
type contentHashFiles struct {
	dir string

	mu sync.Mutex
	// ids are the snapshots whose tree is in a file, and mem those whose
	// tree is in memory; a snapshot is in at most one of them.
	ids map[string]struct{}
	mem map[string][]byte
	// orphans are files of trees that were removed, or replaced by a tree
	// in memory, and are still to delete: not trees, but files under the
	// paths of snapshots that may get a tree again. See purge.
	orphans map[string]struct{}
	closed  bool

	// failWrite, set by tests, fails the writes it returns an error for.
	failWrite func() error
	// afterOpen, set by tests, runs after a lookup opened a tree's file and
	// released mu, before the file is read.
	afterOpen func()
}

func newContentHashFiles(root string) (*contentHashFiles, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, errors.Wrap(err, "create content hash directory")
	}
	dir, err := os.MkdirTemp(root, "trees-")
	if err != nil {
		return nil, errors.Wrap(err, "create content hash directory")
	}
	return &contentHashFiles{
		dir:     dir,
		ids:     make(map[string]struct{}),
		mem:     make(map[string][]byte),
		orphans: make(map[string]struct{}),
	}, nil
}

func (f *contentHashFiles) path(id string) string {
	return filepath.Join(f.dir, id)
}

// store saves id's tree, replacing any older one. A tree that can't be
// written is kept in memory instead, and the error says why. A tree saved for
// metadata that was dropped meanwhile (gone), or after close, is discarded,
// so a snapshot's tree never outlives it.
func (f *contentHashFiles) store(id string, dt []byte, gone func() bool) error {
	tmpPath, err := f.writeTemp(id, dt)

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || (gone != nil && gone()) {
		if err == nil {
			os.Remove(tmpPath)
		}
		return nil
	}
	if err == nil {
		err = os.Rename(tmpPath, f.path(id))
		if err == nil {
			f.ids[id] = struct{}{}
			delete(f.mem, id)
			// Replaced by the tree, if it was left to delete.
			delete(f.orphans, id)
			return nil
		}
		os.Remove(tmpPath)
		err = errors.Wrap(err, "store content hash file")
	}
	// Kept in memory, and the older tree on disk, if any, deleted, so this
	// one is read and checkpointed.
	f.mem[id] = bytes.Clone(dt)
	return stderrors.Join(err, f.unlinkLocked(id))
}

// writeTemp writes dt to a new temporary file next to id's, for store to
// rename into place.
func (f *contentHashFiles) writeTemp(id string, dt []byte) (string, error) {
	if f.failWrite != nil {
		if err := f.failWrite(); err != nil {
			return "", err
		}
	}
	tmp, err := os.CreateTemp(f.dir, ".tmp-"+id+"-")
	if err != nil {
		return "", errors.Wrap(err, "create content hash file")
	}
	tmpPath := tmp.Name()
	_, err = tmp.Write(dt)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmpPath)
		return "", errors.Wrap(err, "write content hash file")
	}
	return tmpPath, nil
}

// unlinkLocked deletes id's tree file, if it has one. A file that can't be
// deleted is left to purge. Requires f.mu.
func (f *contentHashFiles) unlinkLocked(id string) error {
	if _, ok := f.ids[id]; !ok {
		return nil
	}
	delete(f.ids, id)
	if err := os.Remove(f.path(id)); err != nil && !os.IsNotExist(err) {
		f.orphans[id] = struct{}{}
		return errors.Wrap(err, "delete content hash file")
	}
	return nil
}

// read returns id's tree, or ok false when it has none, or when the metadata
// reading it was dropped (gone).
func (f *contentHashFiles) read(id string, gone func() bool) (_ []byte, ok bool, _ error) {
	f.mu.Lock()
	if gone != nil && gone() {
		f.mu.Unlock()
		return nil, false, nil
	}
	if dt, ok := f.mem[id]; ok {
		f.mu.Unlock()
		// Never modified once stored: copied outside the lock.
		return bytes.Clone(dt), true, nil
	}
	if _, ok := f.ids[id]; !ok {
		f.mu.Unlock()
		return nil, false, nil
	}
	// An opened file keeps its tree however the file is replaced or deleted
	// after the lock is released.
	file, err := os.Open(f.path(id))
	f.mu.Unlock()
	if err != nil {
		if os.IsNotExist(err) {
			// The manager deletes a file only under mu, unregistering it:
			// this one was deleted by something else. That is not a tree
			// that was never saved: a rescan of an imported snapshot gives
			// other digests.
			return nil, false, errors.Errorf("content hash file of %s is missing", id)
		}
		return nil, false, errors.Wrap(err, "open content hash file")
	}
	defer file.Close()
	if f.afterOpen != nil {
		f.afterOpen()
	}
	dt, err := io.ReadAll(file)
	if err != nil {
		return nil, false, errors.Wrap(err, "read content hash file")
	}
	return dt, true, nil
}

// has says whether id has a tree, on disk or in memory.
func (f *contentHashFiles) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.mem[id]; ok {
		return true
	}
	_, ok := f.ids[id]
	return ok
}

// remove drops id's tree. markGone runs under the same lock as store's check,
// so a save racing the removal is discarded. The tree's file is left to
// purge, so a caller dropping many trees under its own locks doesn't delete
// their files under them.
func (f *contentHashFiles) remove(id string, markGone func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if markGone != nil {
		markGone()
	}
	delete(f.mem, id)
	if _, ok := f.ids[id]; ok {
		delete(f.ids, id)
		f.orphans[id] = struct{}{}
	}
}

// purge deletes the files of removed trees, each under mu: a save of the
// same snapshot meanwhile renames its tree over the file and takes it off
// the list. A file that can't be deleted is kept on the list for the next
// purge, and the error says why.
func (f *contentHashFiles) purge() error {
	f.mu.Lock()
	ids := make([]string, 0, len(f.orphans))
	for id := range f.orphans {
		ids = append(ids, id)
	}
	f.mu.Unlock()
	var errs []error
	for _, id := range ids {
		f.mu.Lock()
		if _, ok := f.orphans[id]; ok {
			if err := os.Remove(f.path(id)); err != nil && !os.IsNotExist(err) {
				errs = append(errs, errors.Wrap(err, "delete content hash file"))
			} else {
				delete(f.orphans, id)
			}
		}
		f.mu.Unlock()
	}
	return stderrors.Join(errs...)
}

// list returns the snapshots with a tree, on disk or in memory.
func (f *contentHashFiles) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(f.ids)+len(f.mem))
	for id := range f.ids {
		ids = append(ids, id)
	}
	for id := range f.mem {
		ids = append(ids, id)
	}
	return ids
}

// close discards later saves. The files stay for the caller to sweep with
// the root.
func (f *contentHashFiles) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}
