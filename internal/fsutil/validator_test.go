package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tonistiigi/fsutil/types"
)

func TestValidatorSimpleFiles(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo file",
		"ADD foo2 file",
	}))
	assert.NoError(t, err)
}

func TestValidatorFilesNotInOrder(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo file",
		"ADD foo2 file",
		"ADD bar file",
	}))
	assert.Error(t, err)
}

func TestValidatorFilesNotInOrder2(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo file",
		"ADD foo2 file",
		"ADD foo2 file",
	}))
	assert.Error(t, err)
}

func TestValidatorDirIsFile(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo file",
		"ADD foo2 file",
		"ADD foo2 dir",
	}))
	assert.Error(t, err)
}

func TestValidatorDirIsFile2(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo file",
		"ADD foo2 dir",
		"ADD foo2 file",
	}))
	assert.Error(t, err)
}

func TestValidatorNoParentDir(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD bar file",
		"ADD foo/baz file",
	}))
	assert.Error(t, err)
}

func TestValidatorParentFile(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD bar file",
		"ADD bar/baz file",
	}))
	assert.Error(t, err)
}

func TestValidatorParentFile2(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo/bar file",
	}))
	assert.Error(t, err)
}

func TestValidatorSimpleDir(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar file",
	}))
	assert.NoError(t, err)
}

func TestValidatorSimpleDir2(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar file",
		"ADD foo/bay dir",
		"ADD foo/bay/aa file",
		"ADD foo/bay/ab dir",
		"ADD foo/bay/abb dir",
		"ADD foo/bay/abb/a dir",
		"ADD foo/bay/ba file",
		"ADD foo/baz file",
	}))
	assert.NoError(t, err)
}

func TestValidatorBackToParent(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar file",
		"ADD foo/bay dir",
		"ADD foo/bay/aa file",
		"ADD foo/bay/ab dir",
		"ADD foo/bay/ba file",
		"ADD foo/bay dir",
		"ADD foo/baz file",
	}))
	assert.Error(t, err)
}
func TestValidatorParentOrder(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo dir",
		"ADD foo/bar file",
		"ADD foo/bay dir",
		"ADD foo/bay/aa file",
		"ADD foo/bay/ab dir",
		"ADD foo/bar file",
	}))
	assert.Error(t, err)
}
func TestValidatorBigJump(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo dir",
		"ADD foo/a dir",
		"ADD foo/a/foo dir",
		"ADD foo/a/b/foo dir",
		"ADD foo/a/b/c/foo dir",
		"ADD foo/a/b/c/d/foo dir",
		"ADD zzz dir",
	}))
	assert.Error(t, err)
}
func TestValidatorDot(t *testing.T) {
	// dot is before / in naive sort
	err := checkValid(changeStream([]string{
		"ADD foo dir",
		"ADD foo/a dir",
		"ADD foo.2 dir",
	}))
	assert.NoError(t, err)
}

func TestValidatorDot2(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD foo.a dir",
		"ADD foo/a/a dir",
	}))
	assert.Error(t, err)

	err = checkValid(changeStream([]string{
		"ADD foo dir",
		"ADD foo. dir",
		"ADD foo dir",
	}))
	assert.Error(t, err)
}

func TestValidatorSkipDir(t *testing.T) {
	err := checkValid(changeStream([]string{
		"ADD bar dir",
		"ADD bar/foo/a dir",
	}))
	assert.Error(t, err)
}

func checkValid(inp []*change) error {
	v := &Validator{}
	for _, c := range inp {
		if err := v.HandleChange(c.kind, c.path, c.fi, nil); err != nil {
			return err
		}
	}
	return nil
}

type change struct {
	kind ChangeKind
	path string
	fi   os.FileInfo
	data string
}

func changeStream(dt []string) (changes []*change) {
	for _, s := range dt {
		changes = append(changes, parseChange(s))
	}
	return
}

func parseChange(str string) *change {
	f := strings.Fields(str)
	errStr := fmt.Sprintf("invalid change %q", str)
	if len(f) < 3 {
		panic(errStr)
	}
	c := &change{}
	switch f[0] {
	case "ADD":
		c.kind = ChangeKindAdd
	case "CHG":
		c.kind = ChangeKindModify
	case "DEL":
		c.kind = ChangeKindDelete
	default:
		panic(errStr)
	}
	c.path = filepath.FromSlash(f[1])
	st := &types.Stat{}
	switch f[2] {
	case "file":
		if len(f) > 3 {
			if f[3][0] == '>' {
				st.Linkname = f[3][1:]
			} else {
				c.data = f[3]
			}
		}
	case "dir":
		st.Mode |= uint32(os.ModeDir)
	case "socket":
		st.Mode |= uint32(os.ModeSocket)
	case "symlink":
		if len(f) < 4 {
			panic(errStr)
		}
		st.Mode |= uint32(os.ModeSymlink)
		st.Linkname = f[3]
	}
	c.fi = &StatInfo{st}
	return c
}
