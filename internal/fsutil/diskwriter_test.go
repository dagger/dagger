package fsutil

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

// requiresRoot skips tests that require root
func requiresRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("skipping test that requires root")
		return
	}
}

func TestWriterSimple(t *testing.T) {
	requiresRoot(t)

	changes := changeStream([]string{
		"ADD bar dir",
		"ADD bar/foo file",
		"ADD bar/foo2 symlink ../foo",
		"ADD foo file",
		"ADD foo2 file >foo",
	})

	dest := t.TempDir()

	dw, err := NewDiskWriter(context.TODO(), dest, DiskWriterOpt{
		SyncDataCb: noOpWriteTo,
	})
	assert.NoError(t, err)

	for _, c := range changes {
		err := dw.HandleChange(c.kind, c.path, c.fi, nil)
		assert.NoError(t, err)
	}

	b := &bytes.Buffer{}
	err = Walk(context.Background(), dest, nil, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, `dir bar
file bar/foo
symlink:../foo bar/foo2
file foo
file foo2 >foo
`, b.String())

}

func TestWriterFileToDir(t *testing.T) {
	requiresRoot(t)

	changes := changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar file data2",
	})

	dest, err := tmpDir(changeStream([]string{
		"ADD foo file data1",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(dest)

	dw, err := NewDiskWriter(context.TODO(), dest, DiskWriterOpt{
		SyncDataCb: noOpWriteTo,
	})
	assert.NoError(t, err)

	for _, c := range changes {
		err := dw.HandleChange(c.kind, c.path, c.fi, nil)
		assert.NoError(t, err)
	}

	b := &bytes.Buffer{}
	err = Walk(context.Background(), dest, nil, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, `dir foo
file foo/bar
`, b.String())
}

func TestWriterDirToFile(t *testing.T) {
	requiresRoot(t)

	changes := changeStream([]string{
		"ADD foo file data1",
	})

	dest, err := tmpDir(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar file data2",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(dest)

	dw, err := NewDiskWriter(context.TODO(), dest, DiskWriterOpt{
		SyncDataCb: noOpWriteTo,
	})
	assert.NoError(t, err)

	for _, c := range changes {
		err := dw.HandleChange(c.kind, c.path, c.fi, nil)
		assert.NoError(t, err)
	}

	b := &bytes.Buffer{}
	err = Walk(context.Background(), dest, nil, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, `file foo
`, b.String())
}

func TestWalkerWriterSimple(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD bar dir",
		"ADD bar/foo file",
		"ADD bar/foo2 symlink ../foo",
		"ADD foo file mydata",
		"ADD foo2 file",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	dest := t.TempDir()

	dw, err := NewDiskWriter(context.TODO(), dest, DiskWriterOpt{
		SyncDataCb: newWriteToFunc(d, 0),
	})
	assert.NoError(t, err)

	err = Walk(context.Background(), d, nil, readAsAdd(dw.HandleChange))
	assert.NoError(t, err)

	b := &bytes.Buffer{}
	err = Walk(context.Background(), dest, nil, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, filepath.FromSlash(`dir bar
file bar/foo
symlink:../foo bar/foo2
file foo
file foo2
`), b.String())

	dt, err := os.ReadFile(filepath.Join(dest, "foo"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("mydata"), dt)
}

func readAsAdd(f HandleChangeFn) filepath.WalkFunc {
	return func(path string, fi os.FileInfo, err error) error {
		return f(ChangeKindAdd, path, fi, err)
	}
}

func noOpWriteTo(_ context.Context, _ string, _ io.WriteCloser) error {
	return nil
}

func newWriteToFunc(baseDir string, delay time.Duration) WriteToFunc {
	return func(ctx context.Context, path string, wc io.WriteCloser) error {
		if delay > 0 {
			time.Sleep(delay)
		}
		f, err := os.Open(filepath.Join(baseDir, path))
		if err != nil {
			return err
		}
		if _, err := io.Copy(wc, f); err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return nil
	}
}

type notificationBuffer struct {
	items map[string]digest.Digest
	sync.Mutex
}

func newNotificationBuffer() *notificationBuffer {
	nb := &notificationBuffer{
		items: map[string]digest.Digest{},
	}
	return nb
}

type hashed interface {
	Digest() digest.Digest
}

func (nb *notificationBuffer) HandleChange(kind ChangeKind, p string, fi os.FileInfo, err error) (retErr error) {
	nb.Lock()
	defer nb.Unlock()
	if kind == ChangeKindDelete {
		delete(nb.items, p)
	} else {
		h, ok := fi.(hashed)
		if !ok {
			return errors.Errorf("invalid FileInfo: %s", p)
		}
		nb.items[p] = h.Digest()
	}
	return nil
}

func (nb *notificationBuffer) Hash(p string) (digest.Digest, bool) {
	nb.Lock()
	v, ok := nb.items[p]
	nb.Unlock()
	return v, ok
}
