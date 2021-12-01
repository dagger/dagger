package plancontext

import (
	"sync"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
)

type FS struct {
	Result bkgw.Reference
}

type fsContext struct {
	l     sync.RWMutex
	store map[ContextKey]*FS
}

func (c *fsContext) Register(fs *FS) ContextKey {
	c.l.Lock()
	defer c.l.Unlock()

	id := hashID(fs)
	c.store[id] = fs
	return id
}

func (c *fsContext) Get(id ContextKey) *FS {
	c.l.RLock()
	defer c.l.RUnlock()

	return c.store[id]
}
