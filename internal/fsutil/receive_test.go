package fsutil

import (
	"bytes"
	"context"
	"crypto/sha256"
	"hash"
	"io"
	gofs "io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
)

func TestSendError(t *testing.T) {
	fs := &testErrFS{err: errors.New("foo bar")}

	dest := t.TempDir()

	ts := newNotificationBuffer()
	chs := &changes{fn: ts.HandleChange}

	eg, ctx := errgroup.WithContext(context.Background())
	s1, s2 := sockPairProto(ctx)

	eg.Go(func() error {
		defer s1.(*fakeConnProto).closeSend()
		err := Send(ctx, s1, fs, nil)
		assert.Contains(t, err.Error(), "foo bar")
		return err
	})
	eg.Go(func() error {
		err := Receive(ctx, s2, dest, ReceiveOpt{
			NotifyHashed:  chs.HandleChange,
			ContentHasher: simpleSHA256Hasher,
		})
		assert.Contains(t, err.Error(), "error from sender:")
		assert.Contains(t, err.Error(), "foo bar")
		return err
	})

	errCh := make(chan error)
	go func() {
		errCh <- eg.Wait()
	}()
	select {
	case <-time.After(15 * time.Second):
		t.Fatal("timeout")
	case err := <-errCh:
		assert.Contains(t, err.Error(), "foo bar")
	}
}

func TestCopyWithSubDir(t *testing.T) {
	requiresRoot(t)

	d, err := tmpDir(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar file data1",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	fs, err := NewFS(d)
	assert.NoError(t, err)

	dest := t.TempDir()

	eg, ctx := errgroup.WithContext(context.Background())
	s1, s2 := sockPairProto(ctx)

	eg.Go(func() error {
		defer s1.(*fakeConnProto).closeSend()
		subdir, err := SubDirFS([]Dir{{FS: fs, Stat: types.Stat{Path: "sub", Mode: uint32(os.ModeDir | 0755)}}})
		if err != nil {
			return err
		}
		return Send(ctx, s1, subdir, nil)
	})
	eg.Go(func() error {
		return Receive(ctx, s2, dest, ReceiveOpt{})
	})

	err = eg.Wait()
	assert.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(dest, "sub/foo/bar"))
	assert.NoError(t, err)
	assert.Equal(t, "data1", string(dt))
}

func TestCopyDirectoryTimestamps(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar file data1",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	fs, err := NewFS(d)
	assert.NoError(t, err)

	timestamp := time.Unix(0, 0)
	require.NoError(t, os.Chtimes(filepath.Join(d, "foo"), timestamp, timestamp))

	dest := t.TempDir()

	eg, ctx := errgroup.WithContext(context.Background())
	s1, s2 := sockPairProto(ctx)

	eg.Go(func() error {
		defer s1.(*fakeConnProto).closeSend()
		return Send(ctx, s1, fs, nil)
	})
	eg.Go(func() error {
		return Receive(ctx, s2, dest, ReceiveOpt{})
	})

	err = eg.Wait()
	assert.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(dest, "foo/bar"))
	assert.NoError(t, err)
	assert.Equal(t, "data1", string(dt))

	stat, err := os.Stat(filepath.Join(dest, "foo"))
	require.NoError(t, err)
	assert.Equal(t, timestamp, stat.ModTime())
}

func TestCopySwitchDirToFile(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo file data1",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	dest, err := tmpDir(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar file data2",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(dest)

	copy := func(src, dest string) (*changes, error) {
		ts := newNotificationBuffer()
		chs := &changes{fn: ts.HandleChange}

		eg, ctx := errgroup.WithContext(context.Background())
		s1, s2 := sockPairProto(ctx)

		fs, err := NewFS(src)
		if err != nil {
			return nil, err
		}
		fs, err = NewFilterFS(fs, &FilterOpt{
			Map: func(_ string, s *types.Stat) MapResult {
				s.Uid = 0
				s.Gid = 0
				return MapResultKeep
			},
		})
		if err != nil {
			return nil, err
		}

		eg.Go(func() error {
			defer s1.(*fakeConnProto).closeSend()
			return Send(ctx, s1, fs, nil)
		})
		eg.Go(func() error {
			return Receive(ctx, s2, dest, ReceiveOpt{
				NotifyHashed:  chs.HandleChange,
				ContentHasher: simpleSHA256Hasher,
				Filter: func(_ string, s *types.Stat) bool {
					if runtime.GOOS != "windows" {
						// On Windows, Getuid() and Getgid() always return -1
						// See: https://pkg.go.dev/os#Getgid
						// See: https://pkg.go.dev/os#Geteuid
						s.Uid = uint32(os.Getuid())
						s.Gid = uint32(os.Getgid())
					}
					return true
				},
			})
		})

		if err := eg.Wait(); err != nil {
			return nil, err
		}

		return chs, nil
	}

	chs, err := copy(d, dest)
	require.NoError(t, err)

	k, ok := chs.c["foo"]
	require.True(t, ok)
	require.Equal(t, k, ChangeKindAdd)
	require.Equal(t, len(chs.c), 1)

	b := &bytes.Buffer{}
	err = Walk(context.Background(), dest, nil, bufWalk(b))
	assert.NoError(t, err)

	assert.Equal(t, `file foo
`, b.String())
}

func TestHardlinkFilter(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD bar file data1",
		"ADD foo file >bar",
		"ADD foo2 file >bar",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	assert.NoError(t, err)
	defer os.RemoveAll(d)
	fs, err := NewFS(d)
	assert.NoError(t, err)
	fs, err = NewFilterFS(fs, &FilterOpt{})
	assert.NoError(t, err)
	fs, err = NewFilterFS(fs, &FilterOpt{
		IncludePatterns: []string{"foo*"},
		Map: func(_ string, s *types.Stat) MapResult {
			s.Uid = 0
			s.Gid = 0
			return MapResultKeep
		},
	})
	assert.NoError(t, err)

	dest := t.TempDir()

	eg, ctx := errgroup.WithContext(context.Background())
	s1, s2 := sockPairProto(ctx)

	eg.Go(func() error {
		defer s1.(*fakeConnProto).closeSend()
		return Send(ctx, s1, fs, nil)
	})
	eg.Go(func() error {
		return Receive(ctx, s2, dest, ReceiveOpt{
			Filter: func(p string, s *types.Stat) bool {
				if p == "foo2" {
					require.Equal(t, "foo", s.Linkname)
				}
				if runtime.GOOS != "windows" {
					// On Windows, Getuid() and Getgid() always return -1
					// See: https://pkg.go.dev/os#Getgid
					// See: https://pkg.go.dev/os#Geteuid
					s.Uid = uint32(os.Getuid())
					s.Gid = uint32(os.Getgid())
				}
				return true
			},
		})
	})
	assert.NoError(t, eg.Wait())

	dt, err := os.ReadFile(filepath.Join(dest, "foo"))
	assert.NoError(t, err)
	assert.Equal(t, "data1", string(dt))

	st1, err := os.Stat(filepath.Join(dest, "foo"))
	assert.NoError(t, err)

	st2, err := os.Stat(filepath.Join(dest, "foo2"))
	assert.NoError(t, err)

	assert.True(t, os.SameFile(st1, st2))
}

func TestCopySimple(t *testing.T) {
	d, err := tmpDir(changeStream([]string{
		"ADD foo file data1",
		"ADD foo2 file dat2",
		"ADD zzz dir",
		"ADD zzz/aa file data3",
		"ADD zzz/bb dir",
		"ADD zzz/bb/cc dir",
		"ADD zzz/bb/cc/dd symlink ../../",
		"ADD zzz.aa zzdata",
	}))
	assert.NoError(t, err)
	defer os.RemoveAll(d)
	fs, err := NewFS(d)
	assert.NoError(t, err)
	fs, err = NewFilterFS(fs, &FilterOpt{
		Map: func(_ string, s *types.Stat) MapResult {
			s.Uid = 0
			s.Gid = 0
			return MapResultKeep
		},
	})
	assert.NoError(t, err)

	dest := t.TempDir()

	ts := newNotificationBuffer()
	chs := &changes{fn: ts.HandleChange}

	eg, ctx := errgroup.WithContext(context.Background())
	s1, s2 := sockPairProto(ctx)

	tm := time.Now().Truncate(time.Hour)

	var processCbWasCalled bool
	progressCb := func(size int, last bool) {
		processCbWasCalled = true
	}

	eg.Go(func() error {
		defer s1.(*fakeConnProto).closeSend()
		return Send(ctx, s1, fs, progressCb)
	})
	eg.Go(func() error {
		return Receive(ctx, s2, dest, ReceiveOpt{
			NotifyHashed:  chs.HandleChange,
			ContentHasher: simpleSHA256Hasher,
			Filter: func(p string, s *types.Stat) bool {
				if runtime.GOOS != "windows" {
					// On Windows, Getuid() and Getgid() always return -1
					// See: https://pkg.go.dev/os#Getgid
					// See: https://pkg.go.dev/os#Geteuid
					s.Uid = uint32(os.Getuid())
					s.Gid = uint32(os.Getgid())
				}
				s.ModTime = tm.UnixNano()
				return true
			},
		})
	})

	assert.NoError(t, eg.Wait())
	assert.True(t, processCbWasCalled)

	b := &bytes.Buffer{}
	err = Walk(context.Background(), dest, nil, bufWalk(b))
	assert.NoError(t, err)

	if runtime.GOOS == "windows" {
		assert.Equal(t, `file foo
file foo2
dir zzz
file zzz\aa
dir zzz\bb
dir zzz\bb\cc
symlink:..\..\ zzz\bb\cc\dd
file zzz.aa
`, b.String())
	} else {
		assert.Equal(t, `file foo
file foo2
dir zzz
file zzz/aa
dir zzz/bb
dir zzz/bb/cc
symlink:../../ zzz/bb/cc/dd
file zzz.aa
`, b.String())
	}

	dt, err := os.ReadFile(filepath.Join(dest, "zzz/aa"))
	assert.NoError(t, err)
	assert.Equal(t, "data3", string(dt))

	dt, err = os.ReadFile(filepath.Join(dest, "foo2"))
	assert.NoError(t, err)
	assert.Equal(t, "dat2", string(dt))

	fi, err := os.Stat(filepath.Join(dest, "foo2"))
	require.NoError(t, err)
	assert.Equal(t, tm, fi.ModTime())

	h, ok := ts.Hash(filepath.FromSlash("zzz/aa"))
	assert.True(t, ok)
	if runtime.GOOS == "windows" {
		assert.Equal(t, digest.Digest("sha256:5da6b6a222dca8d9260384da15378d4389f7e16943e812e08c39759b8514b456"), h)
	} else {
		assert.Equal(t, digest.Digest("sha256:99b6ef96ee0572b5b3a4eb28f00b715d820bfd73836e59cc1565e241f4d1bb2f"), h)
	}

	h, ok = ts.Hash("foo2")
	assert.True(t, ok)
	if runtime.GOOS == "windows" {
		assert.Equal(t, digest.Digest("sha256:cd83620e5308f6ddb9953a82b2c7450832eac78521dbf067d2882318cabc1311"), h)
	} else {
		assert.Equal(t, digest.Digest("sha256:dd2529f7749ba45ea55de3b2e10086d6494cc45a94e57650c2882a6a14b4ff32"), h)
	}

	h, ok = ts.Hash(filepath.FromSlash("zzz/bb/cc/dd"))
	assert.True(t, ok)
	if runtime.GOOS == "windows" {
		assert.Equal(t, digest.Digest("sha256:47dc68d117ae85dc688103d6ba2cee54caabbbcf606e54ca62fda6a3d9deae19"), h)
	} else {
		assert.Equal(t, digest.Digest("sha256:eca07e8f2d09bd574ea2496312e6de1685ef15b8e6a49a534ed9e722bcac8adc"), h)
	}

	k, ok := chs.c[filepath.FromSlash("zzz/aa")]
	assert.Equal(t, true, ok)
	assert.Equal(t, ChangeKindAdd, k)

	err = os.WriteFile(filepath.Join(d, "zzz/bb/cc/foo"), []byte("data5"), 0600)
	assert.NoError(t, err)

	err = os.RemoveAll(filepath.Join(d, "foo2"))
	assert.NoError(t, err)

	chs = &changes{fn: ts.HandleChange}

	eg, ctx = errgroup.WithContext(context.Background())
	s1, s2 = sockPairProto(ctx)

	eg.Go(func() error {
		defer s1.(*fakeConnProto).closeSend()
		return Send(ctx, s1, fs, nil)
	})
	eg.Go(func() error {
		return Receive(ctx, s2, dest, ReceiveOpt{
			NotifyHashed:  chs.HandleChange,
			ContentHasher: simpleSHA256Hasher,
			Filter: func(_ string, s *types.Stat) bool {
				if runtime.GOOS != "windows" {
					// On Windows, Getuid() and Getgid() always return -1
					// See: https://pkg.go.dev/os#Getgid
					// See: https://pkg.go.dev/os#Geteuid
					s.Uid = uint32(os.Getuid())
					s.Gid = uint32(os.Getgid())
				}
				s.ModTime = tm.UnixNano()
				return true
			},
		})
	})

	assert.NoError(t, eg.Wait())

	b = &bytes.Buffer{}
	err = Walk(context.Background(), dest, nil, bufWalk(b))
	assert.NoError(t, err)

	if runtime.GOOS == "windows" {
		assert.Equal(t, `file foo
dir zzz
file zzz\aa
dir zzz\bb
dir zzz\bb\cc
symlink:..\..\ zzz\bb\cc\dd
file zzz\bb\cc\foo
file zzz.aa
`, b.String())
	} else {
		assert.Equal(t, `file foo
dir zzz
file zzz/aa
dir zzz/bb
dir zzz/bb/cc
symlink:../../ zzz/bb/cc/dd
file zzz/bb/cc/foo
file zzz.aa
`, b.String())
	}

	dt, err = os.ReadFile(filepath.Join(dest, "zzz/bb/cc/foo"))
	assert.NoError(t, err)
	assert.Equal(t, "data5", string(dt))

	h, ok = ts.Hash(filepath.FromSlash("zzz/bb/cc/dd"))
	assert.True(t, ok)
	if runtime.GOOS == "windows" {
		assert.Equal(t, digest.Digest("sha256:47dc68d117ae85dc688103d6ba2cee54caabbbcf606e54ca62fda6a3d9deae19"), h)
	} else {
		assert.Equal(t, digest.Digest("sha256:eca07e8f2d09bd574ea2496312e6de1685ef15b8e6a49a534ed9e722bcac8adc"), h)
	}

	h, ok = ts.Hash(filepath.FromSlash("zzz/bb/cc/foo"))
	assert.True(t, ok)
	if runtime.GOOS == "windows" {
		assert.Equal(t, digest.Digest("sha256:9184a7db8d056ee43838613279db9a7ab02272e50d5e20d253393521bb34aa46"), h)
	} else {
		assert.Equal(t, digest.Digest("sha256:cd14a931fc2e123ded338093f2864b173eecdee578bba6ec24d0724272326c3a"), h)
	}

	_, ok = ts.Hash("foo2")
	assert.False(t, ok)

	k, ok = chs.c["foo2"]
	assert.Equal(t, true, ok)
	assert.Equal(t, ChangeKindDelete, k)

	k, ok = chs.c[filepath.FromSlash("zzz/bb/cc/foo")]
	assert.Equal(t, true, ok)
	assert.Equal(t, ChangeKindAdd, k)

	_, ok = chs.c[filepath.FromSlash("zzz/aa")]
	assert.Equal(t, false, ok)

	_, ok = chs.c["zzz.aa"]
	assert.Equal(t, false, ok)
}

func sockPairProto(ctx context.Context) (Stream, Stream) {
	c1 := make(chan []byte, 32)
	c2 := make(chan []byte, 32)
	return &fakeConnProto{ctx, c1, c2}, &fakeConnProto{ctx, c2, c1}
}

//nolint:unused
type fakeConn struct {
	ctx      context.Context
	recvChan chan *types.Packet
	sendChan chan *types.Packet
}

//nolint:unused
func (fc *fakeConn) Context() context.Context {
	return fc.ctx
}

//nolint:unused
func (fc *fakeConn) RecvMsg(m interface{}) error {
	p, ok := m.(*types.Packet)
	if !ok {
		return errors.Errorf("invalid msg: %#v", m)
	}
	select {
	case <-fc.ctx.Done():
		return fc.ctx.Err()
	case p2 := <-fc.recvChan:
		*p = *p2
		return nil
	}
}

//nolint:unused
func (fc *fakeConn) SendMsg(m interface{}) error {
	p, ok := m.(*types.Packet)
	if !ok {
		return errors.Errorf("invalid msg: %#v", m)
	}
	p2 := *p
	p2.Data = append([]byte{}, p2.Data...)
	select {
	case <-fc.ctx.Done():
		return fc.ctx.Err()
	case fc.sendChan <- &p2:
		return nil
	}
}

type fakeConnProto struct {
	ctx      context.Context
	recvChan chan []byte
	sendChan chan []byte
}

func (fc *fakeConnProto) Context() context.Context {
	return fc.ctx
}

func (fc *fakeConnProto) RecvMsg(m interface{}) error {
	p, ok := m.(*types.Packet)
	if !ok {
		return errors.Errorf("invalid msg: %#v", m)
	}
	select {
	case <-fc.ctx.Done():
		return fc.ctx.Err()
	case dt, ok := <-fc.recvChan:
		if !ok {
			return io.EOF
		}
		return p.Unmarshal(dt)
	}
}

func (fc *fakeConnProto) SendMsg(m interface{}) error {
	p, ok := m.(*types.Packet)
	if !ok {
		return errors.Errorf("invalid msg: %#v", m)
	}
	dt, err := p.Marshal()
	if err != nil {
		return err
	}
	select {
	case <-fc.ctx.Done():
		return fc.ctx.Err()
	case fc.sendChan <- dt:
		return nil
	}
}

func (fc *fakeConnProto) closeSend() {
	close(fc.sendChan)
}

type changes struct {
	c  map[string]ChangeKind
	fn ChangeFunc
	mu sync.Mutex
}

func (c *changes) HandleChange(kind ChangeKind, p string, fi os.FileInfo, err error) error {
	c.mu.Lock()
	if c.c == nil {
		c.c = make(map[string]ChangeKind)
	}
	c.c[p] = kind
	c.mu.Unlock()
	return c.fn(kind, p, fi, err)
}

func simpleSHA256Hasher(s *types.Stat) (hash.Hash, error) {
	h := sha256.New()
	ss := *s
	ss.ModTime = 0
	// Unlike Linux, on FreeBSD's stat() call returns -1 in st_rdev for regular files
	ss.Devminor = 0
	ss.Devmajor = 0
	if runtime.GOOS == "windows" {
		// On Windows, Getuid() and Getgid() always return -1
		// See: https://pkg.go.dev/os#Getgid
		// See: https://pkg.go.dev/os#Geteuid
		ss.Uid = 0
		ss.Gid = 0
	}

	if os.FileMode(ss.Mode)&os.ModeSymlink != 0 && runtime.GOOS != "windows" {
		ss.Mode = ss.Mode | 0777
	}

	dt, err := ss.Marshal()
	if err != nil {
		return nil, err
	}
	h.Write(dt)
	return h, nil
}

type testErrFS struct {
	err error
}

func (e *testErrFS) Walk(ctx context.Context, p string, fn gofs.WalkDirFunc) error {
	return errors.Wrap(e.err, "invalid walk")
}

func (e *testErrFS) Open(p string) (io.ReadCloser, error) {
	return nil, errors.Wrap(e.err, "invalid open")
}
