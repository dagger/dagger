package plancontext

import (
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	"github.com/google/uuid"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"go.dagger.io/dagger/compiler"
)

var fsIDPath = cue.MakePath(
	cue.Str("$dagger"),
	cue.Str("fs"),
	cue.Str("id"),
)

func IsFSValue(v *compiler.Value) bool {
	return v.LookupPath(fsIDPath).Exists()
}

func IsFSScratchValue(v *compiler.Value) bool {
	return IsFSValue(v) && v.LookupPath(fsIDPath).Kind() == cue.NullKind
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

func (fs *FS) MarshalCUE() map[string]interface{} {
	if fs.result == nil {
		return map[string]interface{}{
			"$dagger": map[string]interface{}{
				"fs": map[string]interface{}{
					"id": nil,
				},
			},
		}
	}
	return map[string]interface{}{
		"$dagger": map[string]interface{}{
			"fs": map[string]interface{}{
				"id": fs.id,
			},
		},
	}
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

	// if !v.LookupPath(fsIDPath).IsConcrete() {
	// 	return nil, fmt.Errorf("invalid FS at path %q: FS is not set", v.Path())
	// }

	// This is #Scratch, so we'll return an empty FS
	if IsFSScratchValue(v) {
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
