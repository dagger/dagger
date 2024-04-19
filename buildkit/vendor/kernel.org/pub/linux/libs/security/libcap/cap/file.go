package cap

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"syscall"
	"unsafe"
)

// uapi/linux/xattr.h defined.
var (
	xattrNameCaps, _ = syscall.BytePtrFromString("security.capability")
)

// uapi/linux/capability.h defined.
const (
	vfsCapRevisionMask   = uint32(0xff000000)
	vfsCapFlagsMask      = ^vfsCapRevisionMask
	vfsCapFlagsEffective = uint32(1)

	vfsCapRevision1 = uint32(0x01000000)
	vfsCapRevision2 = uint32(0x02000000)
	vfsCapRevision3 = uint32(0x03000000)
)

// Data types stored in little-endian order.

type vfsCaps1 struct {
	MagicEtc uint32
	Data     [1]struct {
		Permitted, Inheritable uint32
	}
}

type vfsCaps2 struct {
	MagicEtc uint32
	Data     [2]struct {
		Permitted, Inheritable uint32
	}
}

type vfsCaps3 struct {
	MagicEtc uint32
	Data     [2]struct {
		Permitted, Inheritable uint32
	}
	RootID uint32
}

// ErrBadSize indicates the loaded file capability has
// an invalid number of bytes in it.
var ErrBadSize = errors.New("filecap bad size")

// ErrBadMagic indicates that the kernel preferred magic number for
// capability Set values is not supported by this package. This
// generally implies you are using an exceptionally old
// "../libcap/cap" package. An upgrade is needed, or failing that see
// https://sites.google.com/site/fullycapable/ for how to file a bug.
var ErrBadMagic = errors.New("unsupported magic")

// ErrBadPath indicates a failed attempt to set a file capability on
// an irregular (non-executable) file.
var ErrBadPath = errors.New("file is not a regular executable")

// ErrOutOfRange indicates an erroneous value for MinExtFlagSize.
var ErrOutOfRange = errors.New("flag length invalid for export")

// digestFileCap unpacks a file capability and returns it in a *Set
// form.
func digestFileCap(d []byte, sz int, err error) (*Set, error) {
	if err != nil {
		return nil, err
	}
	var raw1 vfsCaps1
	var raw2 vfsCaps2
	var raw3 vfsCaps3
	if sz < binary.Size(raw1) || sz > binary.Size(raw3) {
		return nil, ErrBadSize
	}
	b := bytes.NewReader(d[:sz])
	var magicEtc uint32
	if err = binary.Read(b, binary.LittleEndian, &magicEtc); err != nil {
		return nil, err
	}

	c := NewSet()
	b.Seek(0, io.SeekStart)
	switch magicEtc & vfsCapRevisionMask {
	case vfsCapRevision1:
		if err = binary.Read(b, binary.LittleEndian, &raw1); err != nil {
			return nil, err
		}
		data := raw1.Data[0]
		c.flat[0][Permitted] = data.Permitted
		c.flat[0][Inheritable] = data.Inheritable
		if raw1.MagicEtc&vfsCapFlagsMask == vfsCapFlagsEffective {
			c.flat[0][Effective] = data.Inheritable | data.Permitted
		}
	case vfsCapRevision2:
		if err = binary.Read(b, binary.LittleEndian, &raw2); err != nil {
			return nil, err
		}
		for i, data := range raw2.Data {
			c.flat[i][Permitted] = data.Permitted
			c.flat[i][Inheritable] = data.Inheritable
			if raw2.MagicEtc&vfsCapFlagsMask == vfsCapFlagsEffective {
				c.flat[i][Effective] = data.Inheritable | data.Permitted
			}
		}
	case vfsCapRevision3:
		if err = binary.Read(b, binary.LittleEndian, &raw3); err != nil {
			return nil, err
		}
		for i, data := range raw3.Data {
			c.flat[i][Permitted] = data.Permitted
			c.flat[i][Inheritable] = data.Inheritable
			if raw3.MagicEtc&vfsCapFlagsMask == vfsCapFlagsEffective {
				c.flat[i][Effective] = data.Inheritable | data.Permitted
			}
		}
		c.nsRoot = int(raw3.RootID)
	default:
		return nil, ErrBadMagic
	}
	return c, nil
}

//go:uintptrescapes

// GetFd returns the file capabilities of an open (*os.File).Fd().
func GetFd(file *os.File) (*Set, error) {
	var raw3 vfsCaps3
	d := make([]byte, binary.Size(raw3))
	sz, _, oErr := multisc.r6(syscall.SYS_FGETXATTR, uintptr(file.Fd()), uintptr(unsafe.Pointer(xattrNameCaps)), uintptr(unsafe.Pointer(&d[0])), uintptr(len(d)), 0, 0)
	var err error
	if oErr != 0 {
		err = oErr
	}
	return digestFileCap(d, int(sz), err)
}

//go:uintptrescapes

// GetFile returns the file capabilities of a named file.
func GetFile(path string) (*Set, error) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	var raw3 vfsCaps3
	d := make([]byte, binary.Size(raw3))
	sz, _, oErr := multisc.r6(syscall.SYS_GETXATTR, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(xattrNameCaps)), uintptr(unsafe.Pointer(&d[0])), uintptr(len(d)), 0, 0)
	if oErr != 0 {
		err = oErr
	}
	return digestFileCap(d, int(sz), err)
}

// GetNSOwner returns the namespace owner UID of the capability Set.
func (c *Set) GetNSOwner() (int, error) {
	if magic < kv3 {
		return 0, ErrBadMagic
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nsRoot, nil
}

// SetNSOwner adds an explicit namespace owner UID to the capability
// Set. This is only honored when generating file capabilities, and is
// generally for use by a setup process when installing binaries that
// use file capabilities to become capable inside a namespace to be
// administered by that UID. If capability aware code within that
// namespace writes file capabilities without explicitly setting such
// a UID, the kernel will fix-up the capabilities to be specific to
// that owner. In this way, the kernel prevents filesystem
// capabilities from leaking out of that restricted namespace.
func (c *Set) SetNSOwner(uid int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nsRoot = uid
}

// packFileCap transforms a system capability into a VFS form. Because
// of the way Linux stores capabilities in the file extended
// attributes, the process is a little lossy with respect to effective
// bits.
func (c *Set) packFileCap() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var magic uint32
	switch words {
	case 1:
		if c.nsRoot != 0 {
			return nil, ErrBadSet // nsRoot not supported for single DWORD caps.
		}
		magic = vfsCapRevision1
	case 2:
		if c.nsRoot == 0 {
			magic = vfsCapRevision2
			break
		}
		magic = vfsCapRevision3
	}
	if magic == 0 {
		return nil, ErrBadSize
	}
	eff := uint32(0)
	for _, f := range c.flat {
		eff |= (f[Permitted] | f[Inheritable]) & f[Effective]
	}
	if eff != 0 {
		magic |= vfsCapFlagsEffective
	}
	b := new(bytes.Buffer)
	binary.Write(b, binary.LittleEndian, magic)
	for _, f := range c.flat {
		binary.Write(b, binary.LittleEndian, f[Permitted])
		binary.Write(b, binary.LittleEndian, f[Inheritable])
	}
	if c.nsRoot != 0 {
		binary.Write(b, binary.LittleEndian, c.nsRoot)
	}
	return b.Bytes(), nil
}

//go:uintptrescapes

// SetFd attempts to set the file capabilities of an open
// (*os.File).Fd(). This function can also be used to delete a file's
// capabilities, by calling with c = nil.
//
// Note, Linux does not store the full Effective Flag in the metadata
// for the file. Only a single Effective bit is stored in this
// metadata. This single bit is non-zero if the Effective Flag has any
// overlapping bits with the Permitted or Inheritable Flags of c. This
// may appear suboptimal, but the reasoning behind it is sound.
// Namely, the purpose of the Effective bit it to support capabability
// unaware binaries that will only work if they magically launch with
// the needed Values already raised (this bit is sometimes referred to
// simply as the 'legacy' bit).
//
// Historical note: without *full* support for runtime capability
// manipulation, as it is provided in this "../libcap/cap" package,
// this was previously the only way for Go programs to make use of
// file capabilities.
//
// The preferred way that a binary will actually manipulate its
// file-acquired capabilities is to carefully and deliberately use
// this package (or libcap, assisted by libpsx, for threaded C/C++
// family code).
func (c *Set) SetFd(file *os.File) error {
	if c == nil {
		if _, _, err := multisc.r6(syscall.SYS_FREMOVEXATTR, uintptr(file.Fd()), uintptr(unsafe.Pointer(xattrNameCaps)), 0, 0, 0, 0); err != 0 {
			return err
		}
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	d, err := c.packFileCap()
	if err != nil {
		return err
	}
	if _, _, err := multisc.r6(syscall.SYS_FSETXATTR, uintptr(file.Fd()), uintptr(unsafe.Pointer(xattrNameCaps)), uintptr(unsafe.Pointer(&d[0])), uintptr(len(d)), 0, 0); err != 0 {
		return err
	}
	return nil
}

//go:uintptrescapes

// SetFile attempts to set the file capabilities of the specified
// filename. This function can also be used to delete a file's
// capabilities, by calling with c = nil.
//
// Note, see the comment for SetFd() for some non-obvious behavior of
// Linux for the Effective Flag on the modified file.
func (c *Set) SetFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := fi.Mode()
	if mode&os.ModeType != 0 {
		return ErrBadPath
	}
	if mode&os.FileMode(0111) == 0 {
		return ErrBadPath
	}
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	if c == nil {
		if _, _, err := multisc.r6(syscall.SYS_REMOVEXATTR, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(xattrNameCaps)), 0, 0, 0, 0); err != 0 {
			return err
		}
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	d, err := c.packFileCap()
	if err != nil {
		return err
	}
	if _, _, err := multisc.r6(syscall.SYS_SETXATTR, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(xattrNameCaps)), uintptr(unsafe.Pointer(&d[0])), uintptr(len(d)), 0, 0); err != 0 {
		return err
	}
	return nil
}

// ExtMagic is the 32-bit (little endian) magic for an external
// capability set. It can be used to transmit capabilities in binary
// format in a Linux portable way. The format is:
// <ExtMagic><byte:length><length-bytes*3-of-cap-data>.
const ExtMagic = uint32(0x5101c290)

// Import imports a Set from a byte array where it has been stored in
// a portable (lossless) way. That is values exported by
// libcap.cap_copy_ext() and Export().
func Import(d []byte) (*Set, error) {
	b := bytes.NewBuffer(d)
	var m uint32
	if err := binary.Read(b, binary.LittleEndian, &m); err != nil {
		return nil, ErrBadSize
	} else if m != ExtMagic {
		return nil, ErrBadMagic
	}
	var n byte
	if err := binary.Read(b, binary.LittleEndian, &n); err != nil {
		return nil, ErrBadSize
	}
	c := NewSet()
	if int(n) > 4*words {
		return nil, ErrBadSize
	}
	f := make([]byte, 3)
	for i := 0; i < words; i++ {
		for j := uint(0); n > 0 && j < 4; j++ {
			n--
			if x, err := b.Read(f); err != nil || x != 3 {
				return nil, ErrBadSize
			}
			sh := 8 * j
			c.flat[i][Effective] |= uint32(f[0]) << sh
			c.flat[i][Permitted] |= uint32(f[1]) << sh
			c.flat[i][Inheritable] |= uint32(f[2]) << sh
		}
	}
	return c, nil
}

// MinExtFlagSize defaults to 8 in order to be equivalent to libcap
// defaults. Setting it to zero can generate smaller external
// representations. Such smaller representations can be imported by
// libcap and the Go package just fine, we just default to the default
// libcap representation for legacy reasons.
var MinExtFlagSize = uint(8)

// Export exports a Set into a lossless byte array format where it is
// stored in a portable way. Note, any namespace owner in the Set
// content is not exported by this function.
//
// Note, Export() generates exported byte streams that are importable
// by libcap.cap_copy_int() as well as Import().
func (c *Set) Export() ([]byte, error) {
	if err := c.good(); err != nil {
		return nil, err
	}
	if MinExtFlagSize > 255 {
		return nil, ErrOutOfRange
	}
	b := new(bytes.Buffer)
	binary.Write(b, binary.LittleEndian, ExtMagic)
	c.mu.RLock()
	defer c.mu.RUnlock()
	var n = uint(0)
	for i, f := range c.flat {
		if nn := 4 * uint(i); nn+4 > n {
			if u := f[Effective] | f[Permitted] | f[Inheritable]; u != 0 {
				n = nn
				for ; u != 0; u >>= 8 {
					n++
				}
			}
		}
	}
	if n < MinExtFlagSize {
		n = MinExtFlagSize
	}
	b.Write([]byte{byte(n)})
	for _, f := range c.flat {
		if n == 0 {
			break
		}
		eff, per, inh := f[Effective], f[Permitted], f[Inheritable]
		for i := 0; n > 0 && i < 4; i++ {
			n--
			b.Write([]byte{
				byte(eff & 0xff),
				byte(per & 0xff),
				byte(inh & 0xff),
			})
			eff >>= 8
			per >>= 8
			inh >>= 8
		}
	}
	for n > 0 {
		n--
		b.Write([]byte{0, 0, 0})
	}
	return b.Bytes(), nil
}
