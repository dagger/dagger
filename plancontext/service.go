package plancontext

import (
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/stdlib"
)

var (
	serviceIDPath = cue.MakePath(
		cue.Hid("_service", stdlib.PackageName),
		cue.Str("id"),
	)
)

func IsServiceValue(v *compiler.Value) bool {
	return v.LookupPath(serviceIDPath).Exists()
}

type Service struct {
	id string

	unix  string
	npipe string
}

func (s *Service) ID() string {
	return s.id
}

func (s *Service) Unix() string {
	return s.unix
}

func (s *Service) NPipe() string {
	return s.npipe
}

func (s *Service) Value() *compiler.Value {
	v := compiler.NewValue()
	if err := v.FillPath(serviceIDPath, s.id); err != nil {
		panic(err)
	}
	return v
}

type serviceContext struct {
	l     sync.RWMutex
	store map[string]*Service
}

func (c *serviceContext) New(unix, npipe string) *Service {
	c.l.Lock()
	defer c.l.Unlock()

	s := &Service{
		id:    hashID(unix, npipe),
		unix:  unix,
		npipe: npipe,
	}

	c.store[s.id] = s
	return s
}

func (c *serviceContext) FromValue(v *compiler.Value) (*Service, error) {
	c.l.RLock()
	defer c.l.RUnlock()

	id, err := v.LookupPath(serviceIDPath).String()
	if err != nil {
		return nil, fmt.Errorf("invalid service %q: %w", v.Path(), err)
	}

	s, ok := c.store[id]
	if !ok {
		return nil, fmt.Errorf("service %q not found", id)
	}

	return s, nil
}

func (c *serviceContext) Get(id string) *Service {
	c.l.RLock()
	defer c.l.RUnlock()

	return c.store[id]
}
