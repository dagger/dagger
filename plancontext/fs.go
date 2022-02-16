package plancontext

import (
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	"github.com/google/uuid"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/pkg"
)

var (
	fsIDPath = cue.MakePath(
		cue.Str("$dagger"),
		cue.Str("fs"),
		cue.Hid("_id", pkg.DaggerPackage),
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

func (fs *FS) State() (llb.State, error) {
	if fs.Result() == nil {
		return llb.Scratch(), nil
	}
	return fs.Result().ToState()
}

func (fs *FS) MarshalCUE() *compiler.Value {
	v := compiler.NewValue()
	if fs.result == nil {
		if err := v.FillPath(fsIDPath, nil); err != nil {
			panic(err)
		}
	} else {
		if err := v.FillPath(fsIDPath, fs.id); err != nil {
			panic(err)
		}
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

	if !v.LookupPath(fsIDPath).IsConcrete() {
		return nil, fmt.Errorf("invalid FS at path %q: FS is not set", v.Path())
	}

	// This is #Scratch, so we'll return an empty FS
	if v.LookupPath(fsIDPath).Kind() == cue.NullKind {
		return &FS{}, nil
	}

	id, err := v.LookupPath(fsIDPath).String()
	if err != nil {
		return nil, fmt.Errorf("invalid FS at path %q: %w", v.Path(), err)
	}

	fs, ok := c.store[id]
	if !ok {
		return nil, fmt.Errorf("fs %q not found", id)
	}

	return fs, nil
}
