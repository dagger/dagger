package fsutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidHardlinks(t *testing.T) {
	err := checkHardlinks(changeStream([]string{
		"ADD foo file",
		"ADD foo2 file >foo",
	}))
	assert.NoError(t, err)
}

func TestInvalideHardlinks(t *testing.T) {
	err := checkHardlinks(changeStream([]string{
		"ADD foo file >foo2",
		"ADD foo2 file",
	}))
	assert.Error(t, err)
}

func TestInvalideHardlinks2(t *testing.T) {
	err := checkHardlinks(changeStream([]string{
		"ADD foo file",
		"ADD foo2 file >bar",
	}))
	assert.Error(t, err)
}

func TestHardlinkToDir(t *testing.T) {
	err := checkHardlinks(changeStream([]string{
		"ADD foo dir",
		"ADD foo2 file >foo",
	}))
	assert.Error(t, err)
}

func TestHardlinkToSymlink(t *testing.T) {
	err := checkHardlinks(changeStream([]string{
		"ADD foo symlink /",
		"ADD foo2 file >foo",
	}))
	assert.Error(t, err)
}

func checkHardlinks(inp []*change) error {
	h := &Hardlinks{}
	for _, c := range inp {
		if err := h.HandleChange(c.kind, c.path, c.fi, nil); err != nil {
			return err
		}
	}
	return nil
}
