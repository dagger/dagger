package oci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/containers"
	containerdoci "github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/continuity/fs"
	"github.com/moby/sys/user"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

func GetUser(root, username string) (uint32, uint32, []uint32, error) {
	var isDefault bool
	if username == "" {
		username = "0"
		isDefault = true
	}

	// fast path from uid/gid
	if uid, gid, err := ParseUIDGID(username); err == nil {
		return uid, gid, nil, nil
	}

	buf := userFileBufPool.Get().(*bytes.Buffer)
	defer userFileBufPool.Put(buf)
	buf.Reset()
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(username)))
	buf.Write(size[:])
	buf.WriteString(username)
	passwd := readUserFile(buf, root, "/etc/passwd")
	group := readUserFile(buf, root, "/etc/group")
	cacheable := passwd.readErr == nil && group.readErr == nil
	var key [sha256.Size]byte
	if cacheable {
		key = sha256.Sum256(buf.Bytes())
		if u, ok := userCache.get(key); ok {
			return u.uid, u.gid, slices.Clone(u.sgids), nil
		}
	}

	execUser, err := user.GetExecUser(username, nil, passwd.reader(buf), group.reader(buf))
	if err != nil {
		if isDefault {
			return 0, 0, nil, nil
		}
		return 0, 0, nil, err
	}
	var sgids []uint32
	for _, g := range execUser.Sgids {
		sgids = append(sgids, uint32(g))
	}
	if cacheable {
		userCache.put(key, cachedUser{uint32(execUser.Uid), uint32(execUser.Gid), slices.Clone(sgids)})
	}
	return uint32(execUser.Uid), uint32(execUser.Gid), sgids, nil
}

// userCache holds GetUser's results by the user and the contents of the
// image's passwd and group files, which are all that decide them. Without
// it, every exec whose user isn't a numeric user:group parses both files.
var userCache = &userResults{m: map[[sha256.Size]byte]cachedUser{}}

// userCacheMax bounds userCache: it's emptied when it reaches this size.
const userCacheMax = 1024

type userResults struct {
	mu sync.Mutex
	m  map[[sha256.Size]byte]cachedUser
}

type cachedUser struct {
	uid, gid uint32
	sgids    []uint32
}

func (c *userResults) get(key [sha256.Size]byte) (cachedUser, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	u, ok := c.m[key]
	return u, ok
}

func (c *userResults) put(key [sha256.Size]byte, u cachedUser) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= userCacheMax {
		clear(c.m)
	}
	c.m[key] = u
}

// userFileBufPool holds the buffers GetUser reads the user files into.
var userFileBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// userFile is a passwd or group file as GetUser read it into its buffer.
type userFile struct {
	opened     bool
	start, end int   // the contents read, in the buffer
	readErr    error // why reading stopped early
}

// readUserFile appends root's file p to buf, after a header saying whether
// it opened and how long it is, so that buf tells apart every set of files.
func readUserFile(buf *bytes.Buffer, root, p string) userFile {
	var uf userFile
	f, err := openUserFile(root, p)
	if err != nil {
		buf.WriteByte(0)
		return uf
	}
	defer f.Close()
	uf.opened = true
	buf.WriteByte(1)
	lenAt := buf.Len()
	var size [8]byte
	buf.Write(size[:])
	uf.start = buf.Len()
	_, uf.readErr = buf.ReadFrom(f)
	uf.end = buf.Len()
	binary.LittleEndian.PutUint64(buf.Bytes()[lenAt:], uint64(uf.end-uf.start))
	return uf
}

// reader gives user.GetExecUser what reading the file itself would: its
// contents and then any read error, or, for a file that didn't open, the nil
// *os.File GetUser always passed it.
func (uf userFile) reader(buf *bytes.Buffer) io.Reader {
	if !uf.opened {
		return (*os.File)(nil)
	}
	contents := bytes.NewReader(buf.Bytes()[uf.start:uf.end])
	if uf.readErr != nil {
		return io.MultiReader(contents, errReader{uf.readErr})
	}
	return contents
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// ParseUIDGID takes the fast path to parse UID and GID if and only if they are both provided
func ParseUIDGID(str string) (uid uint32, gid uint32, err error) {
	if str == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(str, ":", 2)
	if len(parts) == 1 {
		return 0, 0, errors.New("groups ID is not provided")
	}
	if uid, err = parseUID(parts[0]); err != nil {
		return 0, 0, err
	}
	if gid, err = parseUID(parts[1]); err != nil {
		return 0, 0, err
	}
	return
}

func openUserFile(root, p string) (*os.File, error) {
	p, err := fs.RootPath(root, p)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func parseUID(str string) (uint32, error) {
	if str == "root" {
		return 0, nil
	}
	uid, err := strconv.ParseUint(str, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(uid), nil
}

// WithUIDGID allows the UID and GID for the Process to be set
// FIXME: This is a temporeray fix for the missing supplementary GIDs from containerd
// once the PR in containerd is merged we should remove this function.
func WithUIDGID(uid, gid uint32, sgids []uint32) containerdoci.SpecOpts {
	return func(_ context.Context, _ containerdoci.Client, _ *containers.Container, s *containerdoci.Spec) error {
		defer ensureAdditionalGids(s)
		setProcess(s)
		s.Process.User.UID = uid
		s.Process.User.GID = gid
		s.Process.User.AdditionalGids = sgids
		return nil
	}
}

// setProcess sets Process to empty if unset
// FIXME: Same on this one. Need to be removed after containerd fix merged
func setProcess(s *containerdoci.Spec) {
	if s.Process == nil {
		s.Process = &specs.Process{}
	}
}

// ensureAdditionalGids ensures that the primary GID is also included in the additional GID list.
// From https://github.com/containerd/containerd/blob/v1.7.0-beta.4/oci/spec_opts.go#L124-L133
func ensureAdditionalGids(s *containerdoci.Spec) {
	setProcess(s)
	for _, f := range s.Process.User.AdditionalGids {
		if f == s.Process.User.GID {
			return
		}
	}
	s.Process.User.AdditionalGids = append([]uint32{s.Process.User.GID}, s.Process.User.AdditionalGids...)
}
