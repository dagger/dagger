package plancontext

import "sync"

type serviceContext struct {
	l     sync.RWMutex
	store map[ContextKey]*Service
}

type Service struct {
	Unix  string
	Npipe string
}

func (c *serviceContext) Register(service *Service) ContextKey {
	c.l.Lock()
	defer c.l.Unlock()

	id := hashID(service)
	c.store[id] = service
	return id
}

func (c *serviceContext) Get(id ContextKey) *Service {
	c.l.RLock()
	defer c.l.RUnlock()

	return c.store[id]
}
