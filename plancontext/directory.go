package plancontext

import "sync"

type Directory struct {
	Path    string
	Include []string
	Exclude []string
}

type directoryContext struct {
	l     sync.RWMutex
	store map[ContextKey]*Directory
}

func (c *directoryContext) Register(directory *Directory) ContextKey {
	c.l.Lock()
	defer c.l.Unlock()

	id := hashID(directory)
	c.store[id] = directory
	return id
}

func (c *directoryContext) Get(id ContextKey) *Directory {
	c.l.RLock()
	defer c.l.RUnlock()

	return c.store[id]
}

func (c *directoryContext) List() []*Directory {
	c.l.RLock()
	defer c.l.RUnlock()

	directories := make([]*Directory, 0, len(c.store))
	for _, d := range c.store {
		directories = append(directories, d)
	}

	return directories
}

func (c *directoryContext) Paths() map[string]string {
	c.l.RLock()
	defer c.l.RUnlock()

	directories := make(map[string]string)
	for _, d := range c.store {
		directories[d.Path] = d.Path
	}

	return directories
}
