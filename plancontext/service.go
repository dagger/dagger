package plancontext

import (
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/pkg"
)

var (
	socketIDPath = cue.MakePath(
		cue.Str("$dagger"),
		cue.Str("socket"),
		cue.Hid("_id", pkg.DaggerPackage),
	)
)

func IsSocketValue(v *compiler.Value) bool {
	return v.LookupPath(socketIDPath).Exists()
}

type Socket struct {
	id string

	unix  string
	npipe string
}

func (s *Socket) ID() string {
	return s.id
}

func (s *Socket) Unix() string {
	return s.unix
}

func (s *Socket) NPipe() string {
	return s.npipe
}

func (s *Socket) MarshalCUE() *compiler.Value {
	v := compiler.NewValue()
	if err := v.FillPath(socketIDPath, s.id); err != nil {
		panic(err)
	}
	return v
}

type socketContext struct {
	l     sync.RWMutex
	store map[string]*Socket
}

func (c *socketContext) New(unix, npipe string) *Socket {
	c.l.Lock()
	defer c.l.Unlock()

	s := &Socket{
		id:    hashID(unix, npipe),
		unix:  unix,
		npipe: npipe,
	}

	c.store[s.id] = s
	return s
}

func (c *socketContext) FromValue(v *compiler.Value) (*Socket, error) {
	c.l.RLock()
	defer c.l.RUnlock()

	if !v.LookupPath(socketIDPath).IsConcrete() {
		return nil, fmt.Errorf("invalid socket at path %q: socket is not set", v.Path())
	}

	id, err := v.LookupPath(socketIDPath).String()
	if err != nil {
		return nil, fmt.Errorf("invalid socket at path %q: %w", v.Path(), err)
	}

	s, ok := c.store[id]
	if !ok {
		return nil, fmt.Errorf("socket %q not found", id)
	}

	return s, nil
}

func (c *socketContext) Get(id string) *Socket {
	c.l.RLock()
	defer c.l.RUnlock()

	return c.store[id]
}
