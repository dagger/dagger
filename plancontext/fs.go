package plancontext

import (
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	"github.com/google/uuid"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/stdlib"
)

var (
	fsIDPath = cue.MakePath(
		cue.Hid("_fs", stdlib.EnginePackage),
		cue.Str("id"),
	)
)

func IsFSValue(v *compiler.Value) bool {
	return v.LookupPath(fsIDPath).Exists()
}

type FS struct {
	id     string
	result bkgw.Reference
}

func (fs *FS) Result() bkgw.Reference {
	return fs.result
}

func (fs *FS) MarshalCUE() *compiler.Value {
	v := compiler.NewValue()
	if err := v.FillPath(fsIDPath, fs.id); err != nil {
		panic(err)
	}
	return v
}

type fsContext struct {
	l     sync.RWMutex
	store map[string]*FS
}

func (c *fsContext) New(result bkgw.Reference) *FS {
	c.l.Lock()
	defer c.l.Unlock()

	fs := &FS{
		// FIXME: get a hash from result instead
		id:     uuid.New().String(),
		result: result,
	}

	c.store[fs.id] = fs
	return fs
}

func (c *fsContext) FromValue(v *compiler.Value) (*FS, error) {
	c.l.RLock()
	defer c.l.RUnlock()

	id, err := v.LookupPath(fsIDPath).String()
	if err != nil {
		return nil, fmt.Errorf("invalid FS %q: %w", v.Path(), err)
	}

	fs, ok := c.store[id]
	if !ok {
		return nil, fmt.Errorf("fs %q not found", id)
	}

	return fs, nil
}
